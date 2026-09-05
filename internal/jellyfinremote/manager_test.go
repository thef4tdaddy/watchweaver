package jellyfinremote

import (
	"context"
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
