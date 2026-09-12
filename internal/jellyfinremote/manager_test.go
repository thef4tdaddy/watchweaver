package jellyfinremote

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

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
