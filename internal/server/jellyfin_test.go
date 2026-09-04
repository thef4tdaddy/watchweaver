package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thef4tdaddy/watchweaver/internal/credentials"
)

const validJellyfinEvent = `{"schema_version":1,"event_id":"event-1","event_type":"played","occurred_at":"2026-09-03T16:00:00Z","server":{"id":"server-a","version":"10.11.0"},"plugin":{"version":"0.1.0","target_abi":"10.11.0.0"},"user":{"id":"user-a"},"item":{"id":"item-a","type":"movie","title":"Movie","year":2026,"provider_ids":{"tmdb":"123"}},"playback":{"played":true,"progress_percent":100}}`

func jellyfinFixture(t *testing.T) (*apiFixture, string) {
	t.Helper()
	f := newAPIFixture(t, nil)
	store, err := credentials.Open(f.db, filepath.Join(t.TempDir(), "key"), credentials.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	f.api.SetCredentialStore(store)
	rr := f.request(http.MethodPost, "/api/integrations/jellyfin", "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("token: %d %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Token string `json:"token"`
	}
	if err = json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return f, body.Token
}
func ingestRequest(f *apiFixture, token, key, body string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/jellyfin/events", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	req.Header.Set("Content-Type", "application/json")
	f.handler.ServeHTTP(rr, req)
	return rr
}

func TestJellyfinTokenLifecycleAndDurableIdempotency(t *testing.T) {
	f, token := jellyfinFixture(t)
	var promptsBefore int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM prompt_tasks`).Scan(&promptsBefore)
	rr := ingestRequest(f, token, "event-1", validJellyfinEvent)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("accept: %d %s", rr.Code, rr.Body.String())
	}
	rr = ingestRequest(f, token, "event-1", validJellyfinEvent)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"duplicate":true`) {
		t.Fatalf("duplicate: %d %s", rr.Code, rr.Body.String())
	}
	var watches, prompts int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM watch_events WHERE source='jellyfin'`).Scan(&watches)
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM prompt_tasks`).Scan(&prompts)
	if watches != 1 || prompts != promptsBefore+1 {
		t.Fatalf("watches=%d prompts=%d before=%d", watches, prompts, promptsBefore)
	}
	rot := f.request(http.MethodPost, "/api/integrations/jellyfin", "")
	var next struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rot.Body.Bytes(), &next)
	if ingestRequest(f, token, "event-1", validJellyfinEvent).Code != http.StatusUnauthorized {
		t.Fatal("old token survived rotation")
	}
	if ingestRequest(f, next.Token, "event-1", validJellyfinEvent).Code != http.StatusOK {
		t.Fatal("rotated token failed")
	}
	if f.request(http.MethodDelete, "/api/integrations/jellyfin", "").Code != http.StatusNoContent {
		t.Fatal("revoke failed")
	}
	if ingestRequest(f, next.Token, "event-1", validJellyfinEvent).Code != http.StatusUnauthorized {
		t.Fatal("revoked token accepted")
	}
}
func TestJellyfinRejectsConflictAndHeaderMismatch(t *testing.T) {
	f, token := jellyfinFixture(t)
	if ingestRequest(f, token, "wrong", validJellyfinEvent).Code != http.StatusBadRequest {
		t.Fatal("mismatched idempotency key accepted")
	}
	if ingestRequest(f, token, "event-1", validJellyfinEvent).Code != http.StatusAccepted {
		t.Fatal("first event failed")
	}
	changed := strings.Replace(validJellyfinEvent, `"item-a"`, `"item-b"`, 1)
	rr := ingestRequest(f, token, "event-1", changed)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "event_conflict") {
		t.Fatalf("conflict: %d %s", rr.Code, rr.Body.String())
	}
}
func TestJellyfinCompletesSetupWithoutTraktAndRedactsToken(t *testing.T) {
	f, token := jellyfinFixture(t)
	rr := f.request(http.MethodGet, "/api/setup", "")
	if !strings.Contains(rr.Body.String(), `"complete":true`) || !strings.Contains(rr.Body.String(), `"jellyfin"`) {
		t.Fatalf("setup: %s", rr.Body.String())
	}
	for _, path := range []string{"/api/integrations", "/api/status", "/api/diagnostics"} {
		rr = f.request(http.MethodGet, path, "")
		if strings.Contains(rr.Body.String(), token) {
			t.Fatalf("token leaked from %s", path)
		}
	}
}

func TestOperationalStatusTreatsTraktAsOptionalWithJellyfin(t *testing.T) {
	f, _ := jellyfinFixture(t)
	rr := f.request(http.MethodGet, "/api/status", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"trakt":{"state":"disabled"`) {
		t.Fatalf("expected optional Trakt to be disabled, body=%s", rr.Body.String())
	}
}

func TestJellyfinAuthenticatedConnectionProbe(t *testing.T) {
	f, token := jellyfinFixture(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/api/v1/ingest/jellyfin/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	f.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent || rr.Header().Get("X-WatchWeaver-Protocol-Version") != "1" {
		t.Fatalf("probe: %d %#v", rr.Code, rr.Header())
	}
}

func TestJellyfinConnectionProbeRejectsInvalidRotatedAndRevokedTokens(t *testing.T) {
	f, token := jellyfinFixture(t)
	probe := func(value string) int {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodHead, "/api/v1/ingest/jellyfin/events", nil)
		if value != "" {
			req.Header.Set("Authorization", "Bearer "+value)
		}
		f.handler.ServeHTTP(rr, req)
		return rr.Code
	}
	if probe("") != http.StatusUnauthorized || probe("wrong") != http.StatusUnauthorized || probe(token) != http.StatusNoContent {
		t.Fatal("initial probe authentication failed")
	}
	rotated := f.request(http.MethodPost, "/api/integrations/jellyfin", "")
	var next struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rotated.Body.Bytes(), &next)
	if probe(token) != http.StatusUnauthorized || probe(next.Token) != http.StatusNoContent {
		t.Fatal("rotated probe authentication failed")
	}
	if f.request(http.MethodDelete, "/api/integrations/jellyfin", "").Code != http.StatusNoContent || probe(next.Token) != http.StatusUnauthorized {
		t.Fatal("revoked probe authentication failed")
	}
}
