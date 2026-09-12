package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("WATCHWEAVER_LISTEN_ADDR", "")
	t.Setenv("WATCHWEAVER_SHUTDOWN_TIMEOUT", "")
	t.Setenv("WATCHWEAVER_DATABASE", "")
	t.Setenv("DISCORD_WEBHOOK_URL", "")
	t.Setenv("WATCHWEAVER_LOG_LEVEL", "")

	cfg := Load()

	if cfg.ListenAddr != ":8080" {
		t.Fatalf("expected default listen addr :8080, got %q", cfg.ListenAddr)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("expected default shutdown timeout 10s, got %s", cfg.ShutdownTimeout)
	}
	if cfg.DatabasePath != "/data/watchweaver.db" {
		t.Fatalf("expected default database path /data/watchweaver.db, got %q", cfg.DatabasePath)
	}
	if cfg.DiscordWebhookURL != "" {
		t.Fatal("Discord should be disabled by default")
	}
	if cfg.DebugLogging {
		t.Fatal("debug logging should be disabled by default")
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("WATCHWEAVER_LISTEN_ADDR", "127.0.0.1:9090")
	t.Setenv("WATCHWEAVER_SHUTDOWN_TIMEOUT", "3s")
	t.Setenv("WATCHWEAVER_DATABASE", "/tmp/watchweaver-test.db")
	t.Setenv("DISCORD_WEBHOOK_URL", "https://discord.invalid/webhook-secret")
	t.Setenv("WATCHWEAVER_LOG_LEVEL", "debug")

	cfg := Load()

	if cfg.ListenAddr != "127.0.0.1:9090" {
		t.Fatalf("expected overridden listen addr, got %q", cfg.ListenAddr)
	}
	if cfg.ShutdownTimeout != 3*time.Second {
		t.Fatalf("expected overridden shutdown timeout, got %s", cfg.ShutdownTimeout)
	}
	if cfg.DatabasePath != "/tmp/watchweaver-test.db" {
		t.Fatalf("expected overridden database path, got %q", cfg.DatabasePath)
	}
	if cfg.DiscordWebhookURL != "https://discord.invalid/webhook-secret" {
		t.Fatal("Discord webhook override missing")
	}
	if !cfg.DebugLogging {
		t.Fatal("debug log level override missing")
	}
}
