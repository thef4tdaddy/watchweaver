package server

import (
	"github.com/thef4tdaddy/watchweaver/internal/credentials"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestBotPairingHandshakeAndRevocation(t *testing.T) {
	f := newAPIFixture(t, nil)
	store, err := credentials.Open(f.db, filepath.Join(t.TempDir(), "key"), credentials.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	f.api.SetCredentialStore(store)
	f.api.ConfigureBotAccess(true, false)
	h, err := NewBotHandler(f.api, BotConfig{TokenProvider: f.api.StoredBotToken, AllowUnpaired: true, Users: []string{"123"}, PublicURL: "https://ww.example"})
	if err != nil {
		t.Fatal(err)
	}
	created := f.request("POST", "/api/integrations/discord-bot", "")
	if created.Code != 201 {
		t.Fatal(created.Body.String())
	}
	token := decodeMap(t, created)["token"].(string)
	status := f.request("GET", "/api/integrations/discord-bot", "")
	if strings.Contains(status.Body.String(), token) {
		t.Fatal("token leaked")
	}
	handshake := func(protocol string, want int) {
		r := httptest.NewRequest("POST", "/api/bot/v1/handshake", strings.NewReader(`{"protocol_version":"`+protocol+`"}`))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Discord-User-ID", "123")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), token) {
			t.Fatal("handshake leaked token")
		}
	}
	handshake("2", 409)
	handshake("1", 200)
	replacement := f.request("POST", "/api/integrations/discord-bot", "")
	if replacement.Code != 201 {
		t.Fatal(replacement.Body.String())
	}
	handshake("1", 401)
	token = decodeMap(t, replacement)["token"].(string)
	handshake("1", 200)
	revoked := f.request("DELETE", "/api/integrations/discord-bot", "")
	if revoked.Code != 204 {
		t.Fatal(revoked.Body.String())
	}
	handshake("1", 503)
}

func TestBotPairingDisabledAndDeploymentOverride(t *testing.T) {
	f := newAPIFixture(t, nil)
	store, err := credentials.Open(f.db, filepath.Join(t.TempDir(), "key"), credentials.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	f.api.SetCredentialStore(store)
	for _, method := range []string{"POST", "DELETE"} {
		if r := f.request(method, "/api/integrations/discord-bot", ""); r.Code != 409 {
			t.Fatal(r.Code)
		}
	}
	f.api.ConfigureBotAccess(true, true)
	for _, method := range []string{"POST", "DELETE"} {
		if r := f.request(method, "/api/integrations/discord-bot", ""); r.Code != 409 {
			t.Fatal(r.Code)
		}
	}
	status := decodeMap(t, f.request("GET", "/api/integrations/discord-bot", ""))
	if status["overridden"] != true || status["configured"] != true {
		t.Fatal(status)
	}
	f.api.ConfigureBotAccess(true, false)
	if r := f.request("PUT", "/api/integrations/discord-bot", ""); r.Code != 405 {
		t.Fatal(r.Code)
	}
}
