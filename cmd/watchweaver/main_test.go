package main

import (
	"path/filepath"
	"testing"
	"testing/fstest"

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
