package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/persistence"
	"github.com/thef4tdaddy/watchweaver/internal/trakt"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

type apiFixture struct {
	t       *testing.T
	db      *sql.DB
	handler http.Handler
	movieID int64
	taskID  int64
}

func newAPIFixture(t *testing.T, traktService *trakt.Service) *apiFixture {
	t.Helper()
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "api.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	movie := mustExecID(t, db, `INSERT INTO media_items(media_type,title,year) VALUES('movie','First Movie',2025)`)
	if _, err = db.Exec(`INSERT INTO external_ids(media_id,provider,external_id) VALUES(?,'trakt','101')`, movie); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at) VALUES(?,'trakt','2','2026-01-02T00:00:00Z','2026-01-02T00:00:00Z'),(?,'trakt','1','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, movie, movie); err != nil {
		t.Fatal(err)
	}
	task := mustExecID(t, db, `INSERT INTO prompt_tasks(media_id,task_type,state) VALUES(?,'rating_review','pending')`, movie)
	h := newHandlerWithAPI(NewReadiness(), fstest.MapFS{"index.html": {Data: []byte("SPA")}}, NewAPI(db, traktService))
	return &apiFixture{t: t, db: db, handler: h, movieID: movie, taskID: task}
}

func mustExecID(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	result, err := db.Exec(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *apiFixture) request(method, path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rr
}

func decodeMap(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return body
}

func TestHistoryPaginationPreservesRewatchesAndIdentity(t *testing.T) {
	f := newAPIFixture(t, nil)
	rr := f.request(http.MethodGet, "/api/history?page=1&per_page=1", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeMap(t, rr)
	if body["total"] != float64(2) || body["total_pages"] != float64(2) {
		t.Fatalf("unexpected page: %#v", body)
	}
	items := body["items"].([]any)
	first := items[0].(map[string]any)
	if first["source_event_id"] != "2" {
		t.Fatalf("history not deterministically newest first: %#v", first)
	}
	media := first["media"].(map[string]any)
	if media["title"] != "First Movie" || media["external_ids"].(map[string]any)["trakt"] != "101" {
		t.Fatalf("missing media identity: %#v", media)
	}
	rr = f.request(http.MethodGet, "/api/history?page=2&per_page=1", "")
	second := decodeMap(t, rr)["items"].([]any)[0].(map[string]any)
	if second["source_event_id"] != "1" {
		t.Fatalf("rewatch missing: %#v", second)
	}
}

func TestHistoryIncludesTVHierarchy(t *testing.T) {
	f := newAPIFixture(t, nil)
	show := mustExecID(t, f.db, `INSERT INTO media_items(media_type,title,year) VALUES('show','Example Show',2024)`)
	season := mustExecID(t, f.db, `INSERT INTO media_items(media_type,title,parent_id,season_number) VALUES('season','Example Show Season 2',?,2)`, show)
	episode := mustExecID(t, f.db, `INSERT INTO media_items(media_type,title,parent_id,episode_number) VALUES('episode','Finale',?,8)`, season)
	if _, err := f.db.Exec(`INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at) VALUES(?,'trakt','tv-1','2026-02-01T00:00:00Z','2026-02-01T00:00:00Z')`, episode); err != nil {
		t.Fatal(err)
	}
	rr := f.request(http.MethodGet, "/api/history?per_page=1", "")
	media := decodeMap(t, rr)["items"].([]any)[0].(map[string]any)["media"].(map[string]any)
	if media["type"] != "episode" || media["show_title"] != "Example Show" || media["season_number"] != float64(2) || media["episode_number"] != float64(8) {
		t.Fatalf("incomplete TV identity: %#v", media)
	}
}

func TestInboxActionsAndTransactionalCompletion(t *testing.T) {
	f := newAPIFixture(t, nil)
	rr := f.request(http.MethodGet, "/api/inbox", "")
	if rr.Code != http.StatusOK || decodeMap(t, rr)["total"] != float64(1) {
		t.Fatalf("inbox: %d %s", rr.Code, rr.Body.String())
	}
	rr = f.request(http.MethodPost, fmt.Sprintf("/api/tasks/%d/complete", f.taskID), `{"rating":8,"review":"  Excellent.  "}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("complete: %d %s", rr.Code, rr.Body.String())
	}
	var state, review string
	var rating int
	if err := f.db.QueryRow(`SELECT state FROM prompt_tasks WHERE id=?`, f.taskID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT rating FROM ratings WHERE media_id=?`, f.movieID).Scan(&rating); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT body FROM reviews WHERE media_id=?`, f.movieID).Scan(&review); err != nil {
		t.Fatal(err)
	}
	if state != "completed" || rating != 8 || review != "Excellent." {
		t.Fatalf("completion not atomic: %s %d %q", state, rating, review)
	}
	var pending int
	if err := f.db.QueryRow(`SELECT pending_rating FROM rating_sync_state WHERE media_id=?`, f.movieID).Scan(&pending); err != nil || pending != 8 {
		t.Fatalf("sync state: %d %v", pending, err)
	}
	if rr = f.request(http.MethodPost, fmt.Sprintf("/api/tasks/%d/skip", f.taskID), ""); rr.Code != http.StatusConflict {
		t.Fatalf("resolved transition should conflict: %d", rr.Code)
	}
}

func TestSnoozeSkipMissingAndValidation(t *testing.T) {
	f := newAPIFixture(t, nil)
	until := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	rr := f.request(http.MethodPost, fmt.Sprintf("/api/tasks/%d/snooze", f.taskID), fmt.Sprintf(`{"until":%q}`, until))
	if rr.Code != http.StatusOK {
		t.Fatalf("snooze: %d %s", rr.Code, rr.Body.String())
	}
	rr = f.request(http.MethodPost, fmt.Sprintf("/api/tasks/%d/skip", f.taskID), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("skip: %d %s", rr.Code, rr.Body.String())
	}
	if rr = f.request(http.MethodPost, "/api/tasks/9999/skip", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("missing task: %d", rr.Code)
	}
	if rr = f.request(http.MethodGet, "/api/history?page=0", ""); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad pagination: %d", rr.Code)
	}
	if rr = f.request(http.MethodPost, "/api/tasks/nope/skip", ""); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad ID: %d", rr.Code)
	}
}

func TestRatingAndReviewCRUDUsesOneCurrentRecord(t *testing.T) {
	f := newAPIFixture(t, nil)
	rr := f.request(http.MethodPut, fmt.Sprintf("/api/media/%d/rating", f.movieID), `{"stars":3.5}`)
	if rr.Code != http.StatusOK || decodeMap(t, rr)["rating"] != float64(7) {
		t.Fatalf("set stars: %d %s", rr.Code, rr.Body.String())
	}
	rr = f.request(http.MethodPut, fmt.Sprintf("/api/media/%d/rating", f.movieID), `{"rating":9}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("update rating: %d", rr.Code)
	}
	var count int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM ratings WHERE media_id=?`, f.movieID).Scan(&count)
	if count != 1 {
		t.Fatalf("expected one rating, got %d", count)
	}
	rr = f.request(http.MethodGet, fmt.Sprintf("/api/media/%d/rating", f.movieID), "")
	if decodeMap(t, rr)["rating"] != float64(9) {
		t.Fatal("rating was not updated")
	}
	if rr = f.request(http.MethodPut, fmt.Sprintf("/api/media/%d/rating", f.movieID), `{"stars":3.7}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("arbitrary float accepted: %d", rr.Code)
	}
	rr = f.request(http.MethodPut, fmt.Sprintf("/api/media/%d/review", f.movieID), `{"body":"Good"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("review set: %d %s", rr.Code, rr.Body.String())
	}
	rr = f.request(http.MethodPut, fmt.Sprintf("/api/media/%d/review", f.movieID), `{"body":"Better"}`)
	if rr.Code != http.StatusOK {
		t.Fatal("review update failed")
	}
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM reviews WHERE media_id=?`, f.movieID).Scan(&count)
	if count != 1 {
		t.Fatalf("expected one review, got %d", count)
	}
	if rr = f.request(http.MethodDelete, fmt.Sprintf("/api/media/%d/review", f.movieID), ""); rr.Code != http.StatusNoContent {
		t.Fatalf("delete review: %d", rr.Code)
	}
	if rr = f.request(http.MethodGet, fmt.Sprintf("/api/media/%d/review", f.movieID), ""); rr.Code != http.StatusNotFound {
		t.Fatalf("deleted review found: %d", rr.Code)
	}
	if rr = f.request(http.MethodDelete, fmt.Sprintf("/api/media/%d/rating", f.movieID), ""); rr.Code != http.StatusNoContent {
		t.Fatalf("delete rating: %d", rr.Code)
	}
	var pendingDelete int
	if err := f.db.QueryRow(`SELECT pending_delete FROM rating_sync_state WHERE media_id=?`, f.movieID).Scan(&pendingDelete); err != nil || pendingDelete != 1 {
		t.Fatalf("pending delete: %d %v", pendingDelete, err)
	}
	if rr = f.request(http.MethodGet, "/api/media/999/rating", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("missing media: %d", rr.Code)
	}
	show := mustExecID(t, f.db, `INSERT INTO media_items(media_type,title) VALUES('show','Unsupported Show')`)
	if rr = f.request(http.MethodDelete, fmt.Sprintf("/api/media/%d/review", show), ""); rr.Code != http.StatusBadRequest {
		t.Fatalf("unsupported target: %d", rr.Code)
	}
}

func TestSettingsDefaultsUpdateValidationAndSecretRedaction(t *testing.T) {
	f := newAPIFixture(t, nil)
	rr := f.request(http.MethodGet, "/api/settings", "")
	body := decodeMap(t, rr)
	if body["timezone"] != "UTC" || body["serializd_reminder_changes"] != float64(20) {
		t.Fatalf("defaults: %#v", body)
	}
	rr = f.request(http.MethodPut, "/api/settings", `{"timezone":"America/Chicago","serializd_enabled":true,"serializd_reminder_changes":10,"serializd_reminder_days":7}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", rr.Code, rr.Body.String())
	}
	if rr = f.request(http.MethodPut, "/api/settings", `{"timezone":"Mars/Olympus","serializd_enabled":true,"serializd_reminder_changes":10,"serializd_reminder_days":7}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid timezone: %d", rr.Code)
	}
	_, _ = f.db.Exec(`INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt','access_token','super-secret'),('trakt','refresh_token','also-secret')`)
	rr = f.request(http.MethodGet, "/api/integrations", "")
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "secret") || strings.Contains(rr.Body.String(), "token") {
		t.Fatalf("secret leaked or status failed: %d %s", rr.Code, rr.Body.String())
	}
}

func TestTraktAuthorizationEndpoints(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Path {
		case "/oauth/device/code":
			body = `{"device_code":"device-secret","user_code":"ABCD","verification_url":"https://trakt.tv/activate","expires_in":600,"interval":1}`
		case "/oauth/device/token":
			body = `{"access_token":"access-secret","refresh_token":"refresh-secret"}`
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}}, nil
	})}
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "trakt.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := trakt.NewService(db, trakt.Config{ClientID: "client", ClientSecret: "client-secret", BaseURL: "https://trakt.invalid", HTTPClient: client})
	f := newAPIFixture(t, service)
	rr := f.request(http.MethodPost, "/api/integrations/trakt/authorize", "")
	if rr.Code != http.StatusAccepted || !strings.Contains(rr.Body.String(), "ABCD") || strings.Contains(rr.Body.String(), "device-secret") {
		t.Fatalf("authorize: %d %s", rr.Code, rr.Body.String())
	}
	rr = f.request(http.MethodPost, "/api/integrations/trakt/authorize/poll", "")
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "secret") {
		t.Fatalf("poll leaked secret: %d %s", rr.Code, rr.Body.String())
	}
	rr = f.request(http.MethodGet, "/api/integrations", "")
	if !strings.Contains(rr.Body.String(), `"status":"connected"`) || strings.Contains(rr.Body.String(), "secret") {
		t.Fatalf("status: %s", rr.Body.String())
	}
}

func TestAPIRoutesDoNotBreakHealthOrSPAFallback(t *testing.T) {
	f := newAPIFixture(t, nil)
	if rr := f.request(http.MethodGet, "/healthz", ""); rr.Code != http.StatusOK {
		t.Fatalf("health: %d", rr.Code)
	}
	if rr := f.request(http.MethodGet, "/dashboard", ""); rr.Code != http.StatusOK || rr.Body.String() != "SPA" {
		t.Fatalf("SPA: %d %q", rr.Code, rr.Body.String())
	}
	if rr := f.request(http.MethodGet, "/api/not-real", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown API should not fall through to SPA: %d", rr.Code)
	}
}
