package trakt

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thef4tdaddy/watchweaver/internal/persistence"
)

func authDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "auth.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestStatusStatesAndSecretRedaction(t *testing.T) {
	db := authDB(t)
	ctx := context.Background()
	s := NewService(db, Config{})
	if got := s.Status(ctx).Status; got != StatusNotConfigured {
		t.Fatalf("got %s", got)
	}
	s = NewService(db, Config{ClientID: "id", ClientSecret: "secret"})
	if got := s.Status(ctx).Status; got != StatusNotAuthorized {
		t.Fatalf("got %s", got)
	}
	_, _ = db.Exec(`INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt','reauth_required','1')`)
	if got := s.Status(ctx).Status; got != StatusReauth {
		t.Fatalf("got %s", got)
	}
	_, _ = db.Exec(`INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt','access_token','ACCESS')`)
	st := s.Status(ctx)
	if st.Status != StatusConnected {
		t.Fatalf("got %s", st.Status)
	}
	b, _ := json.Marshal(st)
	for _, secret := range []string{"ACCESS", "secret"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("status leaked %q", secret)
		}
	}
}

func TestDeviceAuthorizationPendingSlowDownSuccessAndRestart(t *testing.T) {
	db := authDB(t)
	ctx := context.Background()
	tokenCalls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/device/code":
			json.NewEncoder(w).Encode(DeviceCode{DeviceCode: "DEVICE-SECRET", UserCode: "ABCD", VerificationURL: "https://example.test/activate", ExpiresIn: 600, Interval: 1})
		case "/oauth/device/token":
			tokenCalls++
			if tokenCalls == 1 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if tokenCalls == 2 {
				w.WriteHeader(http.StatusConflict)
				return
			}
			json.NewEncoder(w).Encode(token{AccessToken: "ACCESS", RefreshToken: "REFRESH", ExpiresIn: 3600, CreatedAt: 1})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	s := NewService(db, Config{ClientID: "id", ClientSecret: "secret", BaseURL: ts.URL, HTTPClient: ts.Client()})
	st, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != StatusPending || st.UserCode != "ABCD" || strings.Contains(st.VerificationURL, "DEVICE-SECRET") {
		t.Fatalf("bad public pending status: %+v", st)
	}
	if _, err = s.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if s.PollInterval().Seconds() != 6 {
		t.Fatalf("slow-down interval = %v", s.PollInterval())
	}
	st, err = s.Poll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != StatusConnected {
		t.Fatalf("got %s", st.Status)
	}
	restarted := NewService(db, Config{ClientID: "id", ClientSecret: "secret"})
	if restarted.Status(ctx).Status != StatusConnected {
		t.Fatal("token state did not survive service restart")
	}
	var refresh string
	if err := db.QueryRow(`SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='refresh_token'`).Scan(&refresh); err != nil || refresh != "REFRESH" {
		t.Fatalf("refresh persistence: %q %v", refresh, err)
	}
}

func TestRefreshSuccessAndFailureRequiresReauth(t *testing.T) {
	db := authDB(t)
	ctx := context.Background()
	_, _ = db.Exec(`INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt','refresh_token','OLD')`)
	fail := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(token{AccessToken: "NEW-A", RefreshToken: "NEW-R"})
	}))
	defer ts.Close()
	s := NewService(db, Config{ClientID: "id", ClientSecret: "secret", BaseURL: ts.URL, HTTPClient: ts.Client()})
	if err := s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	var got string
	_ = db.QueryRow(`SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='access_token'`).Scan(&got)
	if got != "NEW-A" {
		t.Fatalf("access=%q", got)
	}
	fail = true
	if err := s.Refresh(ctx); err == nil {
		t.Fatal("expected refresh failure")
	}
	if s.Status(ctx).Status != StatusConnected { // existing token remains locally usable until callers decide it is expired
		t.Fatalf("unexpected status %s", s.Status(ctx).Status)
	}
	_, _ = db.Exec(`DELETE FROM integration_state WHERE integration='trakt' AND state_key='access_token'`)
	if s.Status(ctx).Status != StatusReauth {
		t.Fatalf("expected reauth, got %s", s.Status(ctx).Status)
	}
}

func TestRemoteErrorsMalformedAndCancellation(t *testing.T) {
	db := authDB(t)
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"malformed", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("not-json")) }},
		{"server", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(tc.handler)
			defer ts.Close()
			s := NewService(db, Config{ClientID: "id", ClientSecret: "secret", BaseURL: ts.URL, HTTPClient: ts.Client()})
			if _, err := s.Start(context.Background()); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := NewService(db, Config{ClientID: "id", ClientSecret: "secret", BaseURL: "http://127.0.0.1:1", HTTPClient: http.DefaultClient})
	if _, err := s.Start(ctx); err == nil {
		t.Fatal("expected cancellation/network error")
	}
}

func TestDeniedAndExpiredClearPending(t *testing.T) {
	for _, code := range []int{http.StatusGone, http.StatusTeapot} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			db := authDB(t)
			calls := 0
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls == 0 {
					calls++
					json.NewEncoder(w).Encode(DeviceCode{DeviceCode: "d", UserCode: "u", VerificationURL: "v", ExpiresIn: 10, Interval: 1})
					return
				}
				w.WriteHeader(code)
			}))
			defer ts.Close()
			s := NewService(db, Config{ClientID: "id", ClientSecret: "secret", BaseURL: ts.URL, HTTPClient: ts.Client()})
			if _, err := s.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Poll(context.Background()); err == nil {
				t.Fatal("expected terminal auth error")
			}
			if s.Status(context.Background()).Status != StatusNotAuthorized {
				t.Fatal("pending not cleared")
			}
		})
	}
}
