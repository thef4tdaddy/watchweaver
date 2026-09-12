package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebBoundaryOverNetwork(t *testing.T) {
	f := newAPIFixture(t, nil)
	web := strings.Repeat("w", 32)
	bot := strings.Repeat("b", 32)
	h, err := ProtectWebAccess(f.handler, func() (string, error) { return web, nil }, func() (string, error) { return bot, nil })
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	for _, auth := range []string{"none", "bearer", "bot-basic", "web-basic"} {
		req, _ := http.NewRequest("GET", srv.URL+"/api/settings", nil)
		switch auth {
		case "bearer":
			req.Header.Set("Authorization", "Bearer "+bot)
		case "bot-basic":
			req.SetBasicAuth("watchweaver", bot)
		case "web-basic":
			req.SetBasicAuth("watchweaver", web)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		want := 401
		if auth == "web-basic" {
			want = 200
		}
		if resp.StatusCode != want {
			t.Fatalf("%s: %d", auth, resp.StatusCode)
		}
	}
	req, _ := http.NewRequest("PUT", srv.URL+"/api/settings", strings.NewReader(`{}`))
	req.SetBasicAuth("watchweaver", web)
	req.Header.Set("Origin", "https://other.example")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal(resp.StatusCode)
	}
}
func TestBotCredentialRotation(t *testing.T) {
	f := newAPIFixture(t, nil)
	path := filepath.Join(t.TempDir(), "secret")
	old := strings.Repeat("a", 32)
	fresh := strings.Repeat("b", 32)
	if err := os.WriteFile(path, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := NewBotHandler(f.api, BotConfig{TokenProvider: BotTokenFile(path), Users: []string{"123"}, PublicURL: "https://ww.example"})
	if err != nil {
		t.Fatal(err)
	}
	check := func(token string, want int) {
		r := httptest.NewRequest("GET", "/api/bot/v1/capabilities", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Discord-User-ID", "123")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	check(old, 200)
	if err = os.WriteFile(path, []byte(fresh), 0600); err != nil {
		t.Fatal(err)
	}
	check(old, 401)
	check(fresh, 200)
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	check(fresh, 503)
}
