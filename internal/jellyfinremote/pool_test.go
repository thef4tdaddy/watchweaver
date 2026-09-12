package jellyfinremote

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestPoolManagesIndependentConnections(t *testing.T) {
	p := NewPool(&http.Client{}, &accepter{})
	p.Configure("local", Config{Enabled: true, URL: " http://local.example/ ", APIKey: "one", UserID: " user "})
	p.Configure("seedbox", Config{Enabled: false, URL: "https://seedbox.example", APIKey: "two"})

	local, ok := p.Status("local")
	if !ok || !local.Configured || !local.Enabled || local.URL != "http://local.example" || local.UserID != "user" {
		t.Fatalf("unexpected local status: %#v ok=%t", local, ok)
	}
	seedbox, ok := p.Status("seedbox")
	if !ok || !seedbox.Configured || seedbox.Enabled {
		t.Fatalf("unexpected seedbox status: %#v ok=%t", seedbox, ok)
	}
	if _, ok := p.Status("missing"); ok {
		t.Fatal("missing connection unexpectedly exists")
	}

	p.Remove("local")
	if _, ok := p.Status("local"); ok {
		t.Fatal("removed connection still exists")
	}
	p.Remove("missing")
}

func TestPoolRunStartsConnectionsAddedAfterStartup(t *testing.T) {
	p := NewPool(&http.Client{}, &accepter{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	p.Configure("disabled", Config{})
	if _, ok := p.Status("disabled"); !ok {
		t.Fatal("connection added while running was not registered")
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pool did not stop after context cancellation")
	}
}
