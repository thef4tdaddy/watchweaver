package trakt

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/persistence"
	localratings "github.com/thef4tdaddy/watchweaver/internal/ratings"
)

func ratingTestDB(t *testing.T) (*sql.DB, int64) {
	t.Helper()
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "ratings-sync.db")})
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { db.Close() })
	res, err := db.Exec(`INSERT INTO media_items(media_type,title,year) VALUES('movie','Movie',2026)`)
	if err != nil { t.Fatal(err) }
	id, _ := res.LastInsertId()
	if _, err := db.Exec(`INSERT INTO external_ids(media_id,provider,external_id) VALUES(?,'trakt','42')`, id); err != nil { t.Fatal(err) }
	return db, id
}

func TestRatingInitialBaselineAndHeaders(t *testing.T) {
	db, movieID := ratingTestDB(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/sync/ratings/all" { t.Fatalf("path=%s", r.URL.Path) }
		if r.Header.Get("trakt-api-version") != "2" || r.Header.Get("trakt-api-key") != "client" || r.Header.Get("Authorization") != "Bearer token" { t.Fatalf("headers=%v", r.Header) }
		w.Header().Set("X-Pagination-Page-Count", "1")
		json.NewEncoder(w).Encode([]any{map[string]any{"rated_at":"2026-09-01T12:00:00Z","rating":8,"type":"movie","movie":map[string]any{"title":"Movie","year":2026,"ids":map[string]any{"trakt":42}}}})
	}))
	defer server.Close()
	sync := NewRatingSync(db, server.URL, server.Client(), "client", "token")
	if err := sync.ImportInitial(context.Background()); err != nil { t.Fatal(err) }
	var value int
	var source string
	if err := db.QueryRow(`SELECT rating,source FROM ratings WHERE media_id=?`, movieID).Scan(&value, &source); err != nil { t.Fatal(err) }
	if value != 8 || source != "trakt" { t.Fatalf("rating=%d source=%s", value, source) }
	if err := sync.ImportInitial(context.Background()); err != nil { t.Fatal(err) }
	if calls != 1 { t.Fatalf("baseline fetched %d times", calls) }
	var prompts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM prompt_tasks`).Scan(&prompts); err != nil { t.Fatal(err) }
	if prompts != 0 { t.Fatalf("baseline created %d prompts", prompts) }
}

func TestLocalRatingFlushesToTraktAndEchoIsSuppressed(t *testing.T) {
	db, movieID := ratingTestDB(t)
	local := localratings.NewService(db)
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	local.SetNow(func() time.Time { return now })
	if err := local.SetLocal(context.Background(), movieID, 9); err != nil { t.Fatal(err) }
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			var payload map[string][]map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil { t.Fatal(err) }
			if got := int(payload["movies"][0]["rating"].(float64)); got != 9 { t.Fatalf("rating=%d", got) }
			if payload["movies"][0]["ids"].(map[string]any)["trakt"] != "42" { t.Fatalf("payload=%v", payload) }
			w.WriteHeader(http.StatusCreated)
			return
		}
		json.NewEncoder(w).Encode([]any{map[string]any{"rated_at":now.Format(time.RFC3339),"rating":9,"type":"movie","movie":map[string]any{"ids":map[string]any{"trakt":42}}}})
	}))
	defer server.Close()
	sync := NewRatingSync(db, server.URL, server.Client(), "client", "token")
	sync.SetNow(func() time.Time { return now })
	if err := sync.FlushPending(context.Background()); err != nil { t.Fatal(err) }
	if posts != 1 { t.Fatalf("posts=%d", posts) }
	if err := sync.Reconcile(context.Background()); err != nil { t.Fatal(err) }
	var pending sql.NullInt64
	if err := db.QueryRow(`SELECT pending_rating FROM rating_sync_state WHERE media_id=?`, movieID).Scan(&pending); err != nil { t.Fatal(err) }
	if pending.Valid { t.Fatalf("echo left pending rating %d", pending.Int64) }
}

func TestNewerRemoteWinsAndStaleRemoteIsIgnored(t *testing.T) {
	db, movieID := ratingTestDB(t)
	local := localratings.NewService(db)
	localAt := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	local.SetNow(func() time.Time { return localAt })
	if err := local.SetLocal(context.Background(), movieID, 7); err != nil { t.Fatal(err) }
	remoteAt := localAt.Add(time.Minute)
	currentRemote := 10
	currentAt := remoteAt
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]any{map[string]any{"rated_at":currentAt.Format(time.RFC3339),"rating":currentRemote,"type":"movie","movie":map[string]any{"ids":map[string]any{"trakt":42}}}})
	}))
	defer server.Close()
	sync := NewRatingSync(db, server.URL, server.Client(), "client", "token")
	if err := sync.Reconcile(context.Background()); err != nil { t.Fatal(err) }
	var value int
	if err := db.QueryRow(`SELECT rating FROM ratings WHERE media_id=?`, movieID).Scan(&value); err != nil { t.Fatal(err) }
	if value != 10 { t.Fatalf("newer remote rating=%d", value) }
	currentRemote = 3
	currentAt = remoteAt.Add(-time.Hour)
	if err := sync.Reconcile(context.Background()); err != nil { t.Fatal(err) }
	if err := db.QueryRow(`SELECT rating FROM ratings WHERE media_id=?`, movieID).Scan(&value); err != nil { t.Fatal(err) }
	if value != 10 { t.Fatalf("stale remote overwrote rating: %d", value) }
}

func TestFailedOutboundPersistsForRetryAndRestart(t *testing.T) {
	db, movieID := ratingTestDB(t)
	local := localratings.NewService(db)
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	local.SetNow(func() time.Time { return now })
	if err := local.SetLocal(context.Background(), movieID, 6); err != nil { t.Fatal(err) }
	fail := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail { w.Header().Set("Retry-After", "30"); http.Error(w, "busy", http.StatusTooManyRequests); return }
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	sync := NewRatingSync(db, server.URL, server.Client(), "client", "token")
	sync.SetNow(func() time.Time { return now })
	if err := sync.FlushPending(context.Background()); err == nil { t.Fatal("expected failure") }
	var pending, attempts int
	var next string
	if err := db.QueryRow(`SELECT pending_rating,attempt_count,next_attempt_at FROM rating_sync_state WHERE media_id=?`, movieID).Scan(&pending, &attempts, &next); err != nil { t.Fatal(err) }
	if pending != 6 || attempts != 1 { t.Fatalf("pending=%d attempts=%d", pending, attempts) }
	nextAt, err := time.Parse(time.RFC3339Nano, next)
	if err != nil { t.Fatal(err) }
	if nextAt.Before(now.Add(30*time.Second)) { t.Fatalf("retry too early: %s", nextAt) }
	var localValue int
	if err := db.QueryRow(`SELECT rating FROM ratings WHERE media_id=?`, movieID).Scan(&localValue); err != nil || localValue != 6 { t.Fatalf("local rating=%d err=%v", localValue, err) }
	fail = false
	now = now.Add(31 * time.Second)
	restarted := NewRatingSync(db, server.URL, server.Client(), "client", "token")
	restarted.SetNow(func() time.Time { return now })
	if err := restarted.FlushPending(context.Background()); err != nil { t.Fatal(err) }
	var stillPending sql.NullInt64
	if err := db.QueryRow(`SELECT pending_rating FROM rating_sync_state WHERE media_id=?`, movieID).Scan(&stillPending); err != nil { t.Fatal(err) }
	if stillPending.Valid { t.Fatalf("pending survived successful restart retry: %d", stillPending.Int64) }
}

func TestDeleteRatingUsesRemoveEndpoint(t *testing.T) {
	db, movieID := ratingTestDB(t)
	local := localratings.NewService(db)
	if err := local.SetLocal(context.Background(), movieID, 5); err != nil { t.Fatal(err) }
	if err := local.DeleteLocal(context.Background(), movieID); err != nil { t.Fatal(err) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sync/ratings/remove" { t.Fatalf("path=%s", r.URL.Path) }
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	sync := NewRatingSync(db, server.URL, server.Client(), "client", "token")
	if err := sync.FlushPending(context.Background()); err != nil { t.Fatal(err) }
}
