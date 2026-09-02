package trakt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/persistence"
)

func TestSyncManagerRunsCombinedCycleAndPersistsStatus(t *testing.T) {
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "sync-manager.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	requests := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("trakt-api-key") != "client" {
			t.Errorf("missing Trakt headers")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Pagination-Page-Count", "1")
		fmt.Fprint(w, `[]`)
	}))
	defer remote.Close()
	now := time.Date(2026, 9, 2, 19, 0, 0, 0, time.UTC)
	manager := NewSyncManager(db, SyncManagerOptions{BaseURL: remote.URL, HTTPClient: remote.Client(), ClientID: "client", AccessToken: func(context.Context) (string, error) { return "token", nil }, Now: func() time.Time { return now }})
	if err := manager.SyncNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Running || status.LastResult == nil || !status.LastResult.CompletedAt.Equal(now) || status.LastError != "" {
		t.Fatalf("status=%+v", status)
	}
	if requests < 3 {
		t.Fatalf("expected history and rating requests, got %d", requests)
	}
}

func TestSyncManagerReportsRetryAndRejectsConcurrentRun(t *testing.T) {
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "sync-manager.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	manager := NewSyncManager(db, SyncManagerOptions{AccessToken: func(context.Context) (string, error) { return "", nil }})
	if err := manager.SyncNow(context.Background()); err == nil {
		t.Fatal("expected authorization error")
	}
	status, err := manager.Status(context.Background())
	if err != nil || !status.RetryAllowed || status.LastError == "" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	manager.running.Store(true)
	if err := manager.SyncNow(context.Background()); !errors.Is(err, ErrSyncInProgress) {
		t.Fatalf("err=%v", err)
	}
}
