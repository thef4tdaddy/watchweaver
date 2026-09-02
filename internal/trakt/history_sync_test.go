package trakt

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/persistence"
)

type syncImporter struct {
	initialCalls int
	pollCalls    int
	failInitial  bool
	initialError error
}

func (f *syncImporter) ImportInitial(context.Context) (HistoryImportResult, error) {
	f.initialCalls++
	if f.failInitial {
		f.failInitial = false
		if f.initialError != nil {
			return HistoryImportResult{}, f.initialError
		}
		return HistoryImportResult{}, errors.New("baseline unavailable")
	}
	return HistoryImportResult{}, nil
}

func (f *syncImporter) ImportIncrementalSince(context.Context, time.Time) (HistoryImportResult, error) {
	f.pollCalls++
	return HistoryImportResult{}, nil
}

func TestHistorySyncWaitsForAuthorizationThenRunsWithoutRestart(t *testing.T) {
	db := syncDB(t)
	importer := &syncImporter{}
	var tokens []string
	var sleeps int
	syncer := NewHistorySync(db, HistorySyncOptions{
		AuthorizationCheckInterval: time.Second,
		Poller:                     PollerOptions{Interval: time.Minute, Now: func() time.Time { return time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC) }},
		ImporterFactory:            func(token string) HistorySyncImporter { tokens = append(tokens, token); return importer },
		Sleep: func(_ context.Context, _ time.Duration) error {
			sleeps++
			if sleeps == 1 {
				_, err := db.Exec(`INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt','access_token','new-token')`)
				return err
			}
			return context.Canceled
		},
	})
	if err := syncer.Run(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error=%v", err)
	}
	if importer.initialCalls != 1 || importer.pollCalls != 1 || len(tokens) != 1 || tokens[0] != "new-token" {
		t.Fatalf("initial=%d polls=%d tokens=%v", importer.initialCalls, importer.pollCalls, tokens)
	}
	status, err := NewPoller(db, nil, PollerOptions{}).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "polling" || status.LastSuccess == nil {
		t.Fatalf("status=%+v", status)
	}
}

func TestHistorySyncRetriesInterruptedBaselineBeforePolling(t *testing.T) {
	db := syncDB(t)
	_, _ = db.Exec(`INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt','access_token','token')`)
	importer := &syncImporter{failInitial: true}
	var sleeps int
	syncer := NewHistorySync(db, HistorySyncOptions{
		Poller:          PollerOptions{Interval: time.Minute},
		ImporterFactory: func(string) HistorySyncImporter { return importer },
		Sleep: func(context.Context, time.Duration) error {
			sleeps++
			if sleeps == 1 {
				return nil
			}
			return context.Canceled
		},
	})
	if err := syncer.Run(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error=%v", err)
	}
	if importer.initialCalls != 2 || importer.pollCalls != 1 {
		t.Fatalf("initial=%d polls=%d", importer.initialCalls, importer.pollCalls)
	}
	var failures string
	if err := db.QueryRow(`SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='history_poll_failures'`).Scan(&failures); err != nil || failures != "0" {
		t.Fatalf("failures=%q err=%v", failures, err)
	}
}

func TestHistorySyncHonorsRateLimitDuringBaseline(t *testing.T) {
	db := syncDB(t)
	_, _ = db.Exec(`INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt','access_token','token')`)
	importer := &syncImporter{failInitial: true, initialError: &RetryableError{StatusCode: 429, RetryAfter: 30 * time.Second}}
	var delay time.Duration
	syncer := NewHistorySync(db, HistorySyncOptions{
		AuthorizationCheckInterval: time.Second,
		Poller:                     PollerOptions{Interval: time.Minute},
		ImporterFactory:            func(string) HistorySyncImporter { return importer },
		Sleep:                      func(_ context.Context, d time.Duration) error { delay = d; return context.Canceled },
	})
	if err := syncer.Run(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error=%v", err)
	}
	if delay != 30*time.Second {
		t.Fatalf("delay=%v", delay)
	}
}

func syncDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "sync.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
