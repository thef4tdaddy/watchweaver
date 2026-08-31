package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("WATCHWEAVER_LISTEN_ADDR", "")
	t.Setenv("WATCHWEAVER_SHUTDOWN_TIMEOUT", "")

	cfg := Load()

	if cfg.ListenAddr != ":8080" {
		t.Fatalf("expected default listen addr :8080, got %q", cfg.ListenAddr)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("expected default shutdown timeout 10s, got %s", cfg.ShutdownTimeout)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("WATCHWEAVER_LISTEN_ADDR", "127.0.0.1:9090")
	t.Setenv("WATCHWEAVER_SHUTDOWN_TIMEOUT", "3s")

	cfg := Load()

	if cfg.ListenAddr != "127.0.0.1:9090" {
		t.Fatalf("expected overridden listen addr, got %q", cfg.ListenAddr)
	}
	if cfg.ShutdownTimeout != 3*time.Second {
		t.Fatalf("expected overridden shutdown timeout, got %s", cfg.ShutdownTimeout)
	}
}
