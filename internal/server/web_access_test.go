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

func TestWebProtectionFailsClosedAndPreservesHealthAndIngestion(t *testing.T) {
	web, bot := strings.Repeat("w", 32), strings.Repeat("b", 32)
	getWeb := func() (string, error) { return web, nil }
	getBot := func() (string, error) { return bot, nil }
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	if _, err := ProtectWebAccess(next, nil, getBot); err == nil {
		t.Fatal("missing web provider accepted")
	}
	web = "short"
	if _, err := ProtectWebAccess(next, getWeb, getBot); err == nil {
		t.Fatal("short web credential accepted")
	}
	web = bot
	if _, err := ProtectWebAccess(next, getWeb, getBot); err == nil {
		t.Fatal("shared credential accepted")
	}
	web = strings.Repeat("w", 32)
	h, err := ProtectWebAccess(next, getWeb, getBot)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, site, origin string
		auth                       bool
		want                       int
	}{
		{"GET", "/healthz", "", "", false, 204},
		{"GET", "/readyz", "", "", false, 204},
		{"POST", "/api/v1/ingest/jellyfin/events", "", "", false, 204},
		{"PUT", "/api/settings", "cross-site", "", true, 403},
		{"PUT", "/api/settings", "same-origin", "http://ww.example", true, 204},
		{"PUT", "/api/settings", "", "", false, 401},
	} {
		r := httptest.NewRequest(tc.method, "http://ww.example"+tc.path, nil)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		r.Header.Set("Origin", tc.origin)
		if tc.auth {
			r.SetBasicAuth("watchweaver", web)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%+v: %d", tc, w.Code)
		}
	}
	// Rotation to a colliding or invalid web secret must deny requests immediately.
	for _, invalid := range []string{bot, "short"} {
		web = invalid
		r := httptest.NewRequest("GET", "/api/settings", nil)
		r.SetBasicAuth("watchweaver", web)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 503 {
			t.Fatal(w.Code)
		}
	}
}
