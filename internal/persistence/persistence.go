package persistence

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const defaultMigrationsDir = "migrations"
const defaultMigrationBackupRetention = 5

//go:embed migrations/*.up.sql
var embeddedMigrations embed.FS

type Options struct {
	Path                     string
	MigrationsFS             fs.FS
	MigrationsDir            string
	MigrationBackupDir       string
	CredentialKeyPath        string
	MigrationBackupRetention int
}

func OpenAndMigrate(opts Options) (*sql.DB, error) {
	migrationsFS := opts.MigrationsFS
	if migrationsFS == nil {
		migrationsFS = embeddedMigrations
	}

	migrationsDir := opts.MigrationsDir
	if migrationsDir == "" {
		migrationsDir = defaultMigrationsDir
	}

	migrations, err := loadMigrations(migrationsFS, migrationsDir)
	if err != nil {
		return nil, err
	}
	databaseExisted := regularNonEmptyFile(opts.Path)
	db, err := openSQLite(opts.Path)
	if err != nil {
		return nil, err
	}

	if databaseExisted {
		pending, err := pendingMigrations(db, migrations)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		if len(pending) > 0 {
			backupPath, err := createMigrationBackup(db, opts, pending[len(pending)-1].version)
			if err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("create pre-migration safety backup: %w", err)
			}
			log.Printf("pre-migration safety backup created: %s", backupPath)
		}
	}

	if err := RunMigrations(db, migrationsFS, migrationsDir); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

func regularNonEmptyFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func pendingMigrations(db *sql.DB, migrations []migration) ([]migration, error) {
	var tableExists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&tableExists); err != nil {
		return nil, fmt.Errorf("inspect migration state: %w", err)
	}
	if tableExists == 0 {
		return migrations, nil
	}
	pending := make([]migration, 0)
	for _, item := range migrations {
		var exists int
		err := db.QueryRow("SELECT 1 FROM schema_migrations WHERE version = ?", item.version).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			pending = append(pending, item)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect migration version %d: %w", item.version, err)
		}
	}
	return pending, nil
}

func createMigrationBackup(db *sql.DB, opts Options, targetVersion int64) (string, error) {
	backupDir := opts.MigrationBackupDir
	if backupDir == "" {
		backupDir = filepath.Join(filepath.Dir(opts.Path), "backups")
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	destination := filepath.Join(backupDir, fmt.Sprintf("watchweaver-pre-migration-v%06d-%s.db", targetVersion, stamp))
	if err := Backup(db, destination); err != nil {
		return "", err
	}
	cleanup := func() {
		_ = os.Remove(destination + ".key")
		_ = os.Remove(destination)
	}
	if err := VerifyBackup(destination); err != nil {
		cleanup()
		return "", err
	}
	keyPath := opts.CredentialKeyPath
	if keyPath == "" {
		keyPath = filepath.Join(filepath.Dir(opts.Path), ".watchweaver.key")
	}
	if _, err := os.Stat(keyPath); err == nil {
		if err := copyExclusive(keyPath, destination+".key", 0o600); err != nil {
			cleanup()
			return "", fmt.Errorf("copy credential key: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		cleanup()
		return "", fmt.Errorf("inspect credential key: %w", err)
	}
	retention := opts.MigrationBackupRetention
	if retention == 0 {
		retention = defaultMigrationBackupRetention
	}
	if retention > 0 {
		if err := retainMigrationBackups(backupDir, retention); err != nil {
			return "", err
		}
	}
	return destination, nil
}

func VerifyBackup(path string) error {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return fmt.Errorf("open backup for verification: %w", err)
	}
	defer db.Close()
	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("verify backup integrity: %w", err)
	}
	if !strings.EqualFold(result, "ok") {
		return fmt.Errorf("verify backup integrity: %s", result)
	}
	return nil
}

func copyExclusive(source, destination string, mode fs.FileMode) error {
	raw, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func retainMigrationBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("inspect migration backups: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "watchweaver-pre-migration-") && strings.HasSuffix(entry.Name(), ".db") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names[:max(0, len(names)-keep)] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("remove expired migration backup: %w", err)
		}
		if err := os.Remove(filepath.Join(dir, name+".key")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove expired migration backup key: %w", err)
		}
	}
	return nil
}

// Backup writes a transactionally consistent SQLite snapshot. The destination
// must not already exist, which prevents an operator from overwriting a known-
// good backup by mistake.
func Backup(db *sql.DB, destination string) error {
	if strings.TrimSpace(destination) == "" {
		return fmt.Errorf("backup destination is required")
	}
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("backup destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect backup destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	if _, err := db.Exec("VACUUM INTO ?", destination); err != nil {
		return fmt.Errorf("create consistent SQLite backup: %w", err)
	}
	return nil
}

func openSQLite(dbPath string) (*sql.DB, error) {
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	// modernc.org/sqlite applies each _pragma parameter when a new connection is
	// opened. This keeps foreign-key enforcement enabled across database/sql's
	// connection pool instead of relying on a one-time, connection-local PRAGMA.
	query := url.Values{}
	query.Add("_pragma", "foreign_keys(1)")
	// Normal application work can briefly overlap with the background Trakt
	// writer. Wait for that writer instead of surfacing an avoidable SQLITE_BUSY.
	query.Add("_pragma", "busy_timeout(30000)")
	// Reserve the WAL writer when a transaction begins. Without this, an export
	// can read first and fail when it upgrades to a writer while Trakt commits.
	query.Add("_txlock", "immediate")
	dsn := "file:" + filepath.ToSlash(dbPath) + "?" + query.Encode()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite database: %w", err)
	}

	var foreignKeysEnabled int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeysEnabled); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("verify foreign keys: %w", err)
	}
	if foreignKeysEnabled != 1 {
		_ = db.Close()
		return nil, fmt.Errorf("foreign keys not enabled")
	}

	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode = WAL").Scan(&journalMode); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set wal mode: %w", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		_ = db.Close()
		return nil, fmt.Errorf("wal mode not enabled: got %q", journalMode)
	}

	return db, nil
}

type migration struct {
	version int64
	name    string
	sql     string
}

func RunMigrations(db *sql.DB, migrationsFS fs.FS, migrationsDir string) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("ensure schema_migrations table: %w", err)
	}

	migrations, err := loadMigrations(migrationsFS, migrationsDir)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		var exists int
		err := db.QueryRow("SELECT 1 FROM schema_migrations WHERE version = ?", m.version).Scan(&exists)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("check migration version %d: %w", m.version, err)
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("start migration %d transaction: %w", m.version, err)
		}

		if _, err := tx.Exec(m.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d (%s): %w", m.version, m.name, err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, name) VALUES (?, ?)", m.version, m.name); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d (%s): %w", m.version, m.name, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d (%s): %w", m.version, m.name, err)
		}
	}

	return nil
}

func loadMigrations(migrationsFS fs.FS, migrationsDir string) ([]migration, error) {
	entries, err := fs.ReadDir(migrationsFS, migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}

		version, err := parseMigrationVersion(entry.Name())
		if err != nil {
			return nil, err
		}

		migrationPath := filepath.Join(migrationsDir, entry.Name())
		content, err := fs.ReadFile(migrationsFS, migrationPath)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}

		migrations = append(migrations, migration{
			version: version,
			name:    entry.Name(),
			sql:     string(content),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	return migrations, nil
}

func parseMigrationVersion(fileName string) (int64, error) {
	parts := strings.SplitN(fileName, "_", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid migration name %q", fileName)
	}

	version, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid migration version in %q: %w", fileName, err)
	}

	return version, nil
}
