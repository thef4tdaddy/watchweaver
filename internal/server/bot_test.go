package server

import (
	"context"
	"fmt"
	"github.com/thef4tdaddy/watchweaver/internal/workflow"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBotAuthorizationAndScope(t *testing.T) {
	f := newAPIFixture(t, nil)
	token := strings.Repeat("x", 32)
	h, err := NewBotHandler(f.api, BotConfig{TokenProvider: func() (string, error) { return token, nil }, Users: []string{"123"}, PublicURL: "https://ww.example"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		method, path, auth, user string
		code                     int
	}{
		{"GET", "/api/bot/v1/inbox", "", "123", 401},
		{"GET", "/api/bot/v1/inbox", token, "456", 403},
		{"GET", "/api/bot/v1/inbox", token, "123", 200},
		{"PUT", "/api/settings", token, "123", 404},
		{"POST", "/api/bot/v1/serializd/mark-synced", token, "123", 404},
		{"GET", "/api/bot/v1/letterboxd/batches/1/files/1", token, "123", 404},
		{"POST", "/api/bot/v1/integrations/trakt/authorize", token, "123", 404},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		r.Header.Set("Authorization", "Bearer "+c.auth)
		r.Header.Set("X-Discord-User-ID", c.user)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.code {
			t.Errorf("%s: %d %s", c.path, w.Code, w.Body.String())
		}
	}
}
func TestBotWorkflowIdempotencyAndWebConflict(t *testing.T) {
	f := newAPIFixture(t, nil)
	s := workflow.New(f.db)
	v := int64(1)
	rating := 8
	c := workflow.Command{Target: "task", ID: f.taskID, Action: "complete", Rating: &rating, Revision: &v, MediaRevision: &v}
	first, err := s.Apply(context.Background(), c, "123", "interaction")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Apply(context.Background(), c, "123", "interaction")
	if err != nil || first != again {
		t.Fatalf("retry: %+v %v", again, err)
	}
	rating = 9
	if _, err = s.Apply(context.Background(), c, "123", "interaction"); err != workflow.ErrConflict {
		t.Fatalf("payload reuse: %v", err)
	}
	media := workflow.Command{Target: "media", ID: f.movieID, Action: "rating", Rating: &rating, Revision: &v}
	if _, err = s.Apply(context.Background(), media, "123", "next"); err != workflow.ErrConflict {
		t.Fatalf("stale media: %v", err)
	}
}
func TestDeepLinkResourcesAndFilters(t *testing.T) {
	f := newAPIFixture(t, nil)
	for _, path := range []string{fmt.Sprintf("/api/media/%d", f.movieID), fmt.Sprintf("/api/tasks/%d", f.taskID), "/media/1", "/tasks/1", "/settings/trakt", "/letterboxd"} {
		if r := f.request("GET", path, ""); r.Code != 200 {
			t.Errorf("%s: %d", path, r.Code)
		}
	}
	r := f.request("GET", "/api/inbox?type=episode", "")
	if got := decodeMap(t, r)["total"]; got != float64(0) {
		t.Fatal(got)
	}
}
func TestBotOutboxLeaseAndAck(t *testing.T) {
	f := newAPIFixture(t, nil)
	if _, err := f.db.Exec(`INSERT INTO app_settings(setting_key,setting_value) VALUES('bot_task_baseline','0')`); err != nil {
		t.Fatal(err)
	}
	if err := f.api.produceBotNotifications(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	f.api.claimBotNotifications(w, httptest.NewRequest("POST", "/", nil))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	b := decodeMap(t, w)
	items := b["items"].([]any)
	if len(items) != 1 {
		t.Fatal(b)
	}
	item := items[0].(map[string]any)
	req := httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"lease":%q,"message_id":"456"}`, item["lease"])))
	req.SetPathValue("id", fmt.Sprint(item["id"]))
	ack := httptest.NewRecorder()
	f.api.ackBotNotification(ack, req)
	if ack.Code != http.StatusNoContent {
		t.Fatal(ack.Code, ack.Body.String())
	}
	if err := f.api.produceBotNotifications(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	f.db.QueryRow(`SELECT COUNT(*) FROM bot_notifications`).Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
}

func TestBotWorkerEstablishesBaselineAndStops(t *testing.T) {
	f := newAPIFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.api.RunBotWorker(ctx, true) }()
	// Observe a persisted baseline rather than timing the worker startup.
	for i := 0; i < 100; i++ {
		var baseline string
		err := f.db.QueryRow(`SELECT setting_value FROM app_settings WHERE setting_key='bot_task_baseline'`).Scan(&baseline)
		if err == nil {
			break
		}
		time.Sleep(time.Millisecond * 5)
	}
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatal(err)
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM bot_notifications`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("baseline history was notified")
	}
}
