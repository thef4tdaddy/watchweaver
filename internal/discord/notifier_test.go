package discord

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/persistence"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTaskAnnouncementSummaryAndRestartDeduplication(t *testing.T) {
	db, movie := notifierDB(t)
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(`INSERT INTO prompt_tasks(media_id,task_type,state) VALUES(?,'rating','pending')`, movie); err != nil {
			t.Fatal(err)
		}
	}
	var calls int
	var payload string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(r.Body)
		payload = string(body)
		return response(204, ""), nil
	})}
	notifier := NewNotifier(db, Options{WebhookURL: "https://discord.invalid/api/webhooks/secret", HTTPClient: client})
	if err := notifier.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(payload, "3 new rating/review tasks") {
		t.Fatalf("calls=%d payload=%s", calls, payload)
	}
	restarted := NewNotifier(db, Options{WebhookURL: "https://discord.invalid/api/webhooks/secret", HTTPClient: client})
	if err := restarted.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("restart duplicated notification: %d", calls)
	}
}

func TestTransientFailurePersistsAndRetriesConservatively(t *testing.T) {
	db, movie := notifierDB(t)
	result, _ := db.Exec(`INSERT INTO prompt_tasks(media_id,task_type,state) VALUES(?,'rating','pending')`, movie)
	taskID, _ := result.LastInsertId()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	fail := true
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if fail {
			resp := response(429, "busy")
			resp.Header.Set("Retry-After", "120")
			return resp, nil
		}
		return response(204, ""), nil
	})}
	notifier := NewNotifier(db, Options{WebhookURL: "https://discord.invalid/secret", HTTPClient: client, Now: func() time.Time { return now }})
	if err := notifier.Poll(context.Background()); err == nil {
		t.Fatal("expected failure")
	}
	var attempts int
	var next, lastError string
	if err := db.QueryRow(`SELECT attempt_count,next_attempt_at,last_error FROM discord_task_notifications WHERE prompt_task_id=?`, taskID).Scan(&attempts, &next, &lastError); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || strings.Contains(lastError, "secret") {
		t.Fatalf("attempts=%d error=%q", attempts, lastError)
	}
	nextAt, _ := time.Parse(time.RFC3339Nano, next)
	if nextAt.Before(now.Add(2 * time.Minute)) {
		t.Fatalf("retry too early: %v", nextAt)
	}
	if err := notifier.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("retried early: %d", calls)
	}
	fail = false
	now = now.Add(2 * time.Minute)
	if err := notifier.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("retry calls=%d", calls)
	}
	var state string
	_ = db.QueryRow(`SELECT state FROM discord_task_notifications WHERE prompt_task_id=?`, taskID).Scan(&state)
	if state != "sent" {
		t.Fatalf("state=%s", state)
	}
}

func TestDisabledNotifierAndBaselineDoNothing(t *testing.T) {
	db, episode := notifierEpisodeDB(t)
	stamp := "2026-09-01T00:00:00Z"
	_, _ = db.Exec(`INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at,is_baseline) VALUES(?,'trakt','base',?,?,1)`, episode, stamp, stamp)
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return response(204, ""), nil })}
	if err := NewNotifier(db, Options{HTTPClient: client}).Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("disabled calls=%d", calls)
	}
}

func TestSerializdDueAnnouncementOnce(t *testing.T) {
	db, episode := notifierEpisodeDB(t)
	_, _ = db.Exec(`INSERT INTO app_settings(setting_key,setting_value) VALUES('serializd_enabled','true'),('serializd_reminder_changes','1'),('serializd_reminder_days','14')`)
	stamp := "2026-09-01T00:00:00Z"
	_, _ = db.Exec(`INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at,is_baseline) VALUES(?,'trakt','new',?,?,0)`, episode, stamp, stamp)
	calls := 0
	var payload string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(r.Body)
		payload = string(body)
		return response(204, ""), nil
	})}
	notifier := NewNotifier(db, Options{WebhookURL: "https://discord.invalid/secret", HTTPClient: client})
	if err := notifier.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(payload, "Serializd") {
		t.Fatalf("calls=%d payload=%s", calls, payload)
	}
	if err := notifier.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("duplicate Serializd call=%d", calls)
	}
}

func TestSerializdFailureUsesDurableBackoff(t *testing.T) {
	db, episode := notifierEpisodeDB(t)
	_, _ = db.Exec(`INSERT INTO app_settings(setting_key,setting_value) VALUES('serializd_enabled','true'),('serializd_reminder_changes','1'),('serializd_reminder_days','14')`)
	stamp := "2026-09-01T00:00:00Z"
	_, _ = db.Exec(`INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at,is_baseline) VALUES(?,'trakt','new',?,?,0)`, episode, stamp, stamp)
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	fail := true
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if fail {
			resp := response(429, "busy")
			resp.Header.Set("Retry-After", "180")
			return resp, nil
		}
		return response(204, ""), nil
	})}
	notifier := NewNotifier(db, Options{WebhookURL: "https://discord.invalid/secret", HTTPClient: client, Now: func() time.Time { return now }})
	if err := notifier.Poll(context.Background()); err == nil {
		t.Fatal("expected failure")
	}
	if err := notifier.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("retried early: %d", calls)
	}
	var stored string
	if err := db.QueryRow(`SELECT state_value FROM integration_state WHERE integration='discord' AND state_key='serializd_last_error'`).Scan(&stored); err != nil || strings.Contains(stored, "secret") {
		t.Fatalf("stored=%q err=%v", stored, err)
	}
	fail = false
	now = now.Add(3 * time.Minute)
	restarted := NewNotifier(db, Options{WebhookURL: "https://discord.invalid/secret", HTTPClient: client, Now: func() time.Time { return now }})
	if err := restarted.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("retry calls=%d", calls)
	}
	if err := restarted.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("duplicate after success=%d", calls)
	}
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func notifierDB(t *testing.T) (*sql.DB, int64) {
	t.Helper()
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "discord.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	result, err := db.Exec(`INSERT INTO media_items(media_type,title) VALUES('movie','Movie')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	return db, id
}
func notifierEpisodeDB(t *testing.T) (*sql.DB, int64) {
	t.Helper()
	db, _ := notifierDB(t)
	show := insertMedia(t, db, `INSERT INTO media_items(media_type,title) VALUES('show','Show')`)
	season := insertMedia(t, db, `INSERT INTO media_items(media_type,title,parent_id,season_number) VALUES('season','Season',?,1)`, show)
	episode := insertMedia(t, db, `INSERT INTO media_items(media_type,title,parent_id,episode_number) VALUES('episode','Episode',?,1)`, season)
	return db, episode
}
func insertMedia(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	result, err := db.Exec(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	return id
}
