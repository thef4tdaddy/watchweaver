package credentials

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thef4tdaddy/watchweaver/internal/persistence"
)

func TestEncryptedPersistenceRestartAndRedaction(t *testing.T) {
	dir := t.TempDir()
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(dir, "app.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	keyPath := filepath.Join(dir, ".watchweaver.key")
	store, err := Open(db, keyPath, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "trakt", "client_secret", "secret-value"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-value") {
		t.Fatal("database contains plaintext credential")
	}
	restarted, err := Open(db, keyPath, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	value, err := restarted.Get(context.Background(), "trakt", "client_secret")
	if err != nil || value != "secret-value" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	info, err := os.Stat(keyPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode=%v err=%v", info.Mode().Perm(), err)
	}
}

func TestEnvironmentOverrideWins(t *testing.T) {
	dir := t.TempDir()
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(dir, "app.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := Open(db, filepath.Join(dir, "key"), Overrides{TraktClientID: "environment"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "trakt", "client_id", "database"); err != nil {
		t.Fatal(err)
	}
	value, _ := store.Get(context.Background(), "trakt", "client_id")
	if value != "environment" || !store.IsOverridden("trakt", "client_id") {
		t.Fatalf("value=%q", value)
	}
}

func TestBackupKeyIsOwnerOnlyAndNonOverwriting(t *testing.T) {
	dir := t.TempDir()
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(dir, "app.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source := filepath.Join(dir, "key")
	if _, err := Open(db, source, Overrides{}); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "backup.key")
	if err := BackupKey(source, destination); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(destination)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	if err := BackupKey(source, destination); err == nil {
		t.Fatal("expected existing key backup to be rejected")
	}
}

func TestLegacyOAuthTokensMigrateToCiphertext(t *testing.T) {
	dir := t.TempDir()
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(dir, "app.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt','access_token','legacy-access'),('trakt','refresh_token','legacy-refresh')`); err != nil {
		t.Fatal(err)
	}
	store, err := Open(db, filepath.Join(dir, "key"), Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Get(context.Background(), "trakt", "access_token")
	if err != nil || value != "legacy-access" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM integration_state WHERE integration='trakt' AND state_key IN ('access_token','refresh_token')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("plaintext token count=%d err=%v", count, err)
	}
}
