package server

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestNewServerConstruction(t *testing.T) {
	h := NewHandler(NewReadiness())
	s := New(":1234", h)

	if s.Addr != ":1234" {
		t.Fatalf("expected addr :1234, got %q", s.Addr)
	}
	if s.Handler != h {
		t.Fatal("expected handler to be assigned")
	}
}

func TestServeGracefulShutdownOnContextCancel(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}

	s := New(listener.Addr().String(), NewHandler(NewReadiness()))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Serve(ctx, s, listener, 2*time.Second)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shutdown in time")
	}
}
