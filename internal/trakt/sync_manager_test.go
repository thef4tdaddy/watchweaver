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

func TestSyncManagerWaitsOnlyForRemainingIntervalAfterRestart(t *testing.T) {
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "sync-manager.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, 9, 2, 19, 0, 0, 0, time.UTC)
	completed := now.Add(-2 * time.Minute).Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt','sync_last_completed',?)`, completed); err != nil {
		t.Fatal(err)
	}
	manager := NewSyncManager(db, SyncManagerOptions{Now: func() time.Time { return now }})
	if delay := manager.nextDelay(context.Background(), 5*time.Minute); delay != 3*time.Minute {
		t.Fatalf("delay=%s", delay)
	}
	if delay := manager.nextDelay(context.Background(), time.Minute); delay != 0 {
		t.Fatalf("overdue delay=%s", delay)
	}
}

func TestSyncManagerCreatesSeasonPromptFromTraktFinaleAndBackfillsOnce(t *testing.T) {
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "finale.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt','initial_history_complete','1'),('trakt','initial_ratings_complete','1')`); err != nil {
		t.Fatal(err)
	}
	historyRequests := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Pagination-Page-Count", "1")
		if r.URL.Path == "/sync/history" {
			historyRequests++
			fmt.Fprint(w, `[{"id":901,"watched_at":"2026-09-03T03:00:00Z","type":"episode","episode":{"season":3,"number":10,"title":"Finale","episode_type":"season_finale","ids":{"trakt":3010}},"show":{"title":"Example Show","year":2026,"ids":{"trakt":3000}}}]`)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer remote.Close()
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	manager := NewSyncManager(db, SyncManagerOptions{BaseURL: remote.URL, HTTPClient: remote.Client(), ClientID: "client", AccessToken: func(context.Context) (string, error) { return "token", nil }, Now: func() time.Time { return now }})
	if err := manager.SyncNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	var prompts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM prompt_tasks t JOIN media_items m ON m.id=t.media_id WHERE m.media_type='season' AND t.state='pending'`).Scan(&prompts); err != nil {
		t.Fatal(err)
	}
	if prompts != 1 {
		t.Fatalf("season prompts=%d want 1", prompts)
	}
	var finaleType, provider string
	if err := db.QueryRow(`SELECT finale_type,provider FROM episode_metadata`).Scan(&finaleType, &provider); err != nil || finaleType != "season" || provider != "trakt" {
		t.Fatalf("episode metadata finale=%q provider=%q err=%v", finaleType, provider, err)
	}
	firstRequests := historyRequests
	if err := manager.SyncNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if historyRequests != firstRequests+1 {
		t.Fatalf("history requests=%d want %d; backfill repeated", historyRequests, firstRequests+1)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM prompt_tasks`).Scan(&prompts); err != nil || prompts != 1 {
		t.Fatalf("duplicate prompts=%d err=%v", prompts, err)
	}
}
