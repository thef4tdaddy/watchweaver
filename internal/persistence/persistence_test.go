package persistence

import (
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestOpenAndMigrateFreshDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "watchweaver.db")

	db, err := OpenAndMigrate(Options{Path: dbPath})
	if err != nil {
		t.Fatalf("expected open and migrate to succeed, got %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	assertTableExists(t, db, "app_metadata")
	assertTableExists(t, db, "schema_migrations")

	expected := embeddedMigrationCount(t)
	var migrationCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&migrationCount); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if migrationCount != expected {
		t.Fatalf("expected %d applied migrations, got %d", expected, migrationCount)
	}
}

func TestOpenAndMigrateIsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "watchweaver.db")

	first, err := OpenAndMigrate(Options{Path: dbPath})
	if err != nil {
		t.Fatalf("initial open and migrate failed: %v", err)
	}
	_ = first.Close()

	second, err := OpenAndMigrate(Options{Path: dbPath})
	if err != nil {
		t.Fatalf("second open and migrate failed: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	expected := embeddedMigrationCount(t)
	var migrationCount int
	if err := second.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&migrationCount); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if migrationCount != expected {
		t.Fatalf("expected %d migration records after restart, got %d", expected, migrationCount)
	}
}

func TestRunMigrationsAppliesInVersionOrder(t *testing.T) {
	db := openTestDB(t)

	migrationsFS := fstest.MapFS{
		"migrations/000002_second.up.sql": &fstest.MapFile{Data: []byte(`
INSERT INTO ordered_markers(id, label) VALUES (2, 'second');
`)},
		"migrations/000001_first.up.sql": &fstest.MapFile{Data: []byte(`
INSERT INTO ordered_markers(id, label) VALUES (1, 'first');
`)},
		"migrations/000000_setup.up.sql": &fstest.MapFile{Data: []byte(`
CREATE TABLE ordered_markers (
	id INTEGER PRIMARY KEY,
	label TEXT NOT NULL
);
`)},
	}

	if err := RunMigrations(db, migrationsFS, "migrations"); err != nil {
		t.Fatalf("run migrations failed: %v", err)
	}

	rows, err := db.Query("SELECT id, label FROM ordered_markers ORDER BY id")
	if err != nil {
		t.Fatalf("query ordered markers failed: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var id int
		var label string
		if err := rows.Scan(&id, &label); err != nil {
			t.Fatalf("scan row failed: %v", err)
		}
		got = append(got, label)
	}

	joined := strings.Join(got, ",")
	if joined != "first,second" {
		t.Fatalf("expected first,second order, got %q", joined)
	}
}

func TestRunMigrationsRollbackOnFailure(t *testing.T) {
	db := openTestDB(t)

	migrationsFS := fstest.MapFS{
		"migrations/000001_good.up.sql": &fstest.MapFile{Data: []byte(`
CREATE TABLE ok_table (
	id INTEGER PRIMARY KEY
);
`)},
		"migrations/000002_bad.up.sql": &fstest.MapFile{Data: []byte(`
CREATE TABLE should_rollback (
	id INTEGER PRIMARY KEY
);
INSERT INTO missing_table(id) VALUES (1);
`)},
	}

	err := RunMigrations(db, migrationsFS, "migrations")
	if err == nil {
		t.Fatal("expected migration failure, got nil")
	}

	assertTableExists(t, db, "ok_table")
	assertTableMissing(t, db, "should_rollback")

	var appliedVersions int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&appliedVersions); err != nil {
		t.Fatalf("count applied versions failed: %v", err)
	}
	if appliedVersions != 1 {
		t.Fatalf("expected only first migration recorded, got %d", appliedVersions)
	}
}

func TestOpenAndMigrateReturnsErrorForBadPath(t *testing.T) {
	_, err := OpenAndMigrate(Options{Path: "/dev/null/watchweaver.db"})
	if err == nil {
		t.Fatal("expected open and migrate failure for invalid path")
	}
}

func TestOpenAndMigrateEnablesForeignKeys(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "watchweaver.db")
	db, err := OpenAndMigrate(Options{Path: dbPath})
	if err != nil {
		t.Fatalf("open and migrate failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`
CREATE TABLE parent (
	id INTEGER PRIMARY KEY
);
CREATE TABLE child (
	id INTEGER PRIMARY KEY,
	parent_id INTEGER NOT NULL REFERENCES parent(id)
);
`); err != nil {
		t.Fatalf("create fk tables failed: %v", err)
	}

	_, err = db.Exec("INSERT INTO child(id, parent_id) VALUES (1, 99)")
	if err == nil {
		t.Fatal("expected foreign key insert failure, got nil")
	}
}

func TestOpenAndMigrateRequestsWALMode(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "watchweaver.db")
	db, err := OpenAndMigrate(Options{Path: dbPath})
	if err != nil {
		t.Fatalf("open and migrate failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("read journal mode failed: %v", err)
	}
	if strings.ToLower(mode) != "wal" {
		t.Fatalf("expected journal mode wal, got %q", mode)
	}
	var busyTimeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("expected 5000ms busy timeout, got %d", busyTimeout)
	}
}

func TestWritesWaitForConcurrentWriter(t *testing.T) {
	db, err := OpenAndMigrate(Options{Path: filepath.Join(t.TempDir(), "watchweaver.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO app_metadata(key,value) VALUES('writer-one','active')`); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		_, execErr := db.Exec(`INSERT INTO app_metadata(key,value) VALUES('writer-two','waited')`)
		result <- execErr
	}()
	<-started
	time.Sleep(50 * time.Millisecond)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatalf("second writer did not wait: %v", err)
	}
}

func TestRunMigrationsErrorsForInvalidMigrationName(t *testing.T) {
	db := openTestDB(t)
	migrationsFS := fstest.MapFS{
		"migrations/not-numbered.up.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
	}

	err := RunMigrations(db, migrationsFS, "migrations")
	if err == nil {
		t.Fatal("expected invalid migration name error")
	}
	if !strings.Contains(err.Error(), "invalid migration") {
		t.Fatalf("expected invalid migration error, got %v", err)
	}
}

func embeddedMigrationCount(t *testing.T) int {
	t.Helper()
	migrations, err := loadMigrations(embeddedMigrations, defaultMigrationsDir)
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}
	return len(migrations)
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestBackupCreatesConsistentRestorableSnapshot(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenAndMigrate(Options{Path: filepath.Join(dir, "live.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`INSERT INTO app_metadata(key, value) VALUES ('backup-test', 'preserved')`); err != nil {
		t.Fatal(err)
	}

	backupPath := filepath.Join(dir, "backups", "snapshot.db")
	if err := Backup(db, backupPath); err != nil {
		t.Fatal(err)
	}
	restored, err := sql.Open("sqlite", "file:"+filepath.ToSlash(backupPath))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var value string
	if err := restored.QueryRow(`SELECT value FROM app_metadata WHERE key = 'backup-test'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "preserved" {
		t.Fatalf("expected preserved value, got %q", value)
	}
	if err := Backup(db, backupPath); err == nil {
		t.Fatal("expected existing destination to be rejected")
	}
}

func assertTableExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	var tableName string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&tableName)
	if err != nil {
		t.Fatalf("expected table %s to exist: %v", name, err)
	}
}

func assertTableMissing(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	var tableName string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&tableName)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected table %s to be missing, got %q err=%v", name, tableName, err)
	}
}

var _ fs.FS = fstest.MapFS{}
