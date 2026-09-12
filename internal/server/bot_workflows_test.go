package server

import (
	"context"
	"fmt"
	"github.com/thef4tdaddy/watchweaver/internal/trakt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBotSearchAndExactMediaHierarchy(t *testing.T) {
	f := newAPIFixture(t, nil)
	show := mustExecID(t, f.db, `INSERT INTO media_items(media_type,title) VALUES('show','Unique Series')`)
	season := mustExecID(t, f.db, `INSERT INTO media_items(media_type,title,parent_id,season_number) VALUES('season','Season 2',?,2)`, show)
	episode := mustExecID(t, f.db, `INSERT INTO media_items(media_type,title,parent_id,episode_number) VALUES('episode','Finale',?,7)`, season)
	r := f.request("GET", "/api/media?q=Unique&type=episode", "")
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	body := decodeMap(t, r)
	if body["total"] != float64(1) {
		t.Fatal(body)
	}
	r = f.request("GET", fmt.Sprintf("/api/media/%d", episode), "")
	media := decodeMap(t, r)["media"].(map[string]any)
	if media["show_title"] != "Unique Series" || media["season_number"] != float64(2) || media["episode_number"] != float64(7) {
		t.Fatal(media)
	}
	r = f.request("GET", fmt.Sprintf("/api/media/%d", show), "")
	if len(decodeMap(t, r)["children"].([]any)) != 1 {
		t.Fatal(r.Body.String())
	}
	if r = f.request("GET", "/api/media?type=invalid", ""); r.Code != 400 {
		t.Fatal(r.Code)
	}
	if r = f.request("GET", "/api/media/99999", ""); r.Code != 404 {
		t.Fatal(r.Code)
	}
}
func TestWebEditorAtomicSaveAndConflict(t *testing.T) {
	f := newAPIFixture(t, nil)
	path := fmt.Sprintf("/api/media/%d/edit", f.movieID)
	r := f.request("POST", path, `{"rating":8,"review":"Good","revision":1}`)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r = f.request("POST", path, `{"rating":3,"review":"Stale","revision":1}`); r.Code != 409 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = f.request("GET", fmt.Sprintf("/api/media/%d", f.movieID), "")
	body := decodeMap(t, r)
	if body["rating"] != float64(8) || body["review"] != "Good" {
		t.Fatal(body)
	}
	if r = f.request("POST", path, `{"rating":9,"review":"x"}`); r.Code != 400 {
		t.Fatal(r.Code)
	}
	if r = f.request("GET", path, ""); r.Code != 405 {
		t.Fatal(r.Code)
	}
}
func TestBotSyncJobPersistenceAndOwner(t *testing.T) {
	f := newAPIFixture(t, nil)
	f.api.SetTraktSyncManager(trakt.NewSyncManager(f.db, trakt.SyncManagerOptions{AccessToken: func(context.Context) (string, error) { return "", nil }}))
	token := strings.Repeat("t", 32)
	h, err := NewBotHandler(f.api, BotConfig{TokenProvider: func() (string, error) { return token, nil }, Users: []string{"123", "456"}, PublicURL: "https://ww.example"})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, user, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Discord-User-ID", user)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if r := request("POST", "/api/bot/v1/sync/trakt", "123", ""); r.Code != 400 {
		t.Fatal(r.Code)
	}
	for i := 0; i < 2; i++ {
		if r := request("POST", "/api/bot/v1/sync/trakt", "123", "one"); r.Code != 202 {
			t.Fatal(r.Body.String())
		}
	}
	var count int
	f.db.QueryRow(`SELECT COUNT(*) FROM bot_sync_jobs`).Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
	if r := request("GET", "/api/bot/v1/jobs/123:one", "456", ""); r.Code != 404 {
		t.Fatal(r.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.api.RunBotWorker(ctx, false) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r := request("GET", "/api/bot/v1/jobs/123:one", "123", "")
		state := decodeMap(t, r)["state"]
		if state == "failed" || state == "completed" {
			cancel()
			<-done
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("durable job never settled")
}
