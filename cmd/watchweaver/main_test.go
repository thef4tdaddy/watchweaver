package main

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/persistence"
	"github.com/thef4tdaddy/watchweaver/internal/server"
)

func TestInitializeMarksReadinessOnSuccess(t *testing.T) {
	readiness := server.NewReadiness()
	dbPath := filepath.Join(t.TempDir(), "watchweaver.db")

	db, err := initialize(readiness, persistence.Options{Path: dbPath})
	if err != nil {
		t.Fatalf("expected initialize success, got %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if !readiness.IsReady() {
		t.Fatal("expected readiness true after successful initialization")
	}
}

func TestApplicationPollIntervalUsesPersistedPreference(t *testing.T) {
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "poll.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if got := applicationPollInterval(context.Background(), db, 9*time.Minute); got != 9*time.Minute {
		t.Fatalf("default=%v", got)
	}
	if _, err := db.ExecContext(context.Background(), `INSERT INTO app_settings(setting_key,setting_value) VALUES('trakt_poll_minutes','17')`); err != nil {
		t.Fatal(err)
	}
	if got := applicationPollInterval(context.Background(), db, 9*time.Minute); got != 17*time.Minute {
		t.Fatalf("persisted=%v", got)
	}
}

func TestInitializeLeavesReadinessFalseWhenMigrationsFail(t *testing.T) {
	readiness := server.NewReadiness()
	dbPath := filepath.Join(t.TempDir(), "watchweaver.db")

	migrationsFS := fstest.MapFS{
		"migrations/000001_bad.up.sql": &fstest.MapFile{
			Data: []byte("CREATE TABLE broken (;"),
		},
	}

	db, err := initialize(readiness, persistence.Options{
		Path:          dbPath,
		MigrationsFS:  migrationsFS,
		MigrationsDir: "migrations",
	})
	if err == nil {
		if db != nil {
			_ = db.Close()
		}
		t.Fatal("expected initialize to fail when migration fails")
	}

	if readiness.IsReady() {
		t.Fatal("expected readiness false when initialization fails")
	}
}
