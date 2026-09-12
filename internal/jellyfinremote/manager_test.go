package jellyfinremote

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/jellyfin"
)

type accepter struct{ events []jellyfin.Event }

func (a *accepter) Accept(_ context.Context, e jellyfin.Event) (jellyfin.Result, error) {
	a.events = append(a.events, e)
	return jellyfin.Result{}, nil
}

func TestConnectionErrorsUseStableSafeCodesAndGuidance(t *testing.T) {
	tests := []struct {
		err           error
		code, message string
	}{
		{errors.New("Jellyfin event stream returned HTTP 401"), "authentication_failed", "API key"},
		{errors.New("Jellyfin event stream returned HTTP 404"), "plugin_endpoint_missing", "plugin"},
		{errors.New("Get https://private.example/api/watchweaver/events: connection refused"), "network_unreachable", "could not reach"},
	}
	for _, test := range tests {
		if got := errorCode(test.err); got != test.code {
			t.Errorf("code=%q want=%q", got, test.code)
		}
		message := safeError(test.err)
		if !strings.Contains(message, test.message) {
			t.Errorf("message=%q missing %q", message, test.message)
		}
		if strings.Contains(message, "private.example") {
			t.Errorf("safe message leaked URL: %q", message)
		}
	}
}

func TestParseSSEIgnoresControlFramesAndAcceptsEvent(t *testing.T) {
	var names []string
	err := parseSSE(strings.NewReader("event: hello\ndata: {}\n\nevent: watchweaver.event\ndata: {\"event_id\":\"one\"}\n\nevent: ping\ndata: {}\n\n"), func(name string, data []byte) error { names = append(names, name); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "hello,watchweaver.event,ping" {
		t.Fatalf("unexpected frames: %v", names)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestConnectionUsesJellyfinAPIKey(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://jellyfin.example/System/Info" || r.Header.Get("X-Emby-Token") != "secret" {
			t.Fatalf("unexpected request")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"Version":"10.11.11"}`)), Header: make(http.Header)}, nil
	})}
	m := New(client, &accepter{})
	version, err := m.Test(context.Background(), Config{URL: "https://jellyfin.example", APIKey: "secret"})
	if err != nil || version != "10.11.11" {
		t.Fatalf("version=%q err=%v", version, err)
	}
}

func TestConfigureNormalizesAndClearsConnectionFailure(t *testing.T) {
	m := New(nil, &accepter{})
	now := time.Now()
	m.status.Connected = true
	m.status.LastErrorCode = "network_unreachable"
	m.status.LastError = "old error"
	m.status.NextRetryAt = &now
	m.Configure(Config{Enabled: true, URL: " https://jellyfin.example/base/ ", UserID: " user ", APIKey: "secret"})
	status := m.Status()
	if !status.Configured || !status.Enabled || status.Connected || status.URL != "https://jellyfin.example/base" || status.UserID != "user" {
		t.Fatalf("unexpected status: %#v", status)
	}
	if status.LastErrorCode != "" || status.LastError != "" || status.NextRetryAt != nil {
		t.Fatalf("stale failure was not cleared: %#v", status)
	}
}

func TestConsumeAcceptsOnlyWatchWeaverEventsAndTracksStatus(t *testing.T) {
	accepted := &accepter{}
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://jellyfin.example/api/watchweaver/events" || r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("X-Emby-Token") != "secret" {
			t.Fatalf("unexpected stream request: %s", r.URL)
		}
		body := "event: ping\ndata: {}\n\nevent: watchweaver.event\ndata: {\"event_id\":\"event-1\"}\n\n"
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	m := New(client, accepted)
	err := m.consume(context.Background(), Config{URL: "https://jellyfin.example", APIKey: "secret"})
	if !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if len(accepted.events) != 1 || accepted.events[0].EventID != "event-1" {
		t.Fatalf("events=%#v", accepted.events)
	}
	status := m.Status()
	if !status.Connected || status.LastAttemptAt == nil || status.LastConnectedAt == nil || status.LastEventAt == nil || status.EventsReceived != 1 {
		t.Fatalf("unexpected stream status: %#v", status)
	}
}

func TestConnectionTestReportsInvalidResponseAndHTTPStatus(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, want string
	}{
		{http.StatusUnauthorized, `{}`, "HTTP 401"},
		{http.StatusOK, `{`, "read Jellyfin response"},
	} {
		client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
		})}
		_, err := New(client, &accepter{}).Test(context.Background(), Config{URL: "https://jellyfin.example", APIKey: "secret"})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("error=%v want %q", err, tc.want)
		}
	}
}

func TestErrorClassificationCoversSafeFallbacks(t *testing.T) {
	cases := map[string]string{
		"Jellyfin URL must be an absolute HTTP or HTTPS URL": "invalid_url",
		"decode Jellyfin stream event: bad JSON":             "invalid_stream",
		"accept Jellyfin stream event: database busy":        "ingestion_failed",
		"Jellyfin event stream returned HTTP 500":            "server_error",
		"unexpected EOF": "network_unreachable",
	}
	for message, want := range cases {
		if got := errorCode(errors.New(message)); got != want {
			t.Errorf("errorCode(%q)=%q want=%q", message, got, want)
		}
	}
	if got := errorCode(io.EOF); got != "stream_closed" {
		t.Fatalf("EOF code=%q", got)
	}
	if got := safeError(nil); got != "connection closed" {
		t.Fatalf("nil safe error=%q", got)
	}
}
