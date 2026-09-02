package persistence

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

const defaultMigrationsDir = "migrations"

//go:embed migrations/*.up.sql
var embeddedMigrations embed.FS

type Options struct {
	Path          string
	MigrationsFS  fs.FS
	MigrationsDir string
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

	db, err := openSQLite(opts.Path)
	if err != nil {
		return nil, err
	}

	if err := RunMigrations(db, migrationsFS, migrationsDir); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
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
	query.Add("_pragma", "busy_timeout(5000)")
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
