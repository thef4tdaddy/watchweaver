package config

import (
	"os"
	"strings"
	"time"
)

const (
	defaultListenAddr        = ":8080"
	defaultShutdownTimeout   = 10 * time.Second
	defaultDatabasePath      = "/data/watchweaver.db"
	defaultTraktPollInterval = 5 * time.Minute
	defaultTraktPollOverlap  = 10 * time.Minute
)

type Config struct {
	ListenAddr        string
	ShutdownTimeout   time.Duration
	DatabasePath      string
	TraktClientID     string
	TraktClientSecret string
	TraktBaseURL      string
	TraktPollInterval time.Duration
	TraktPollOverlap  time.Duration
	DiscordWebhookURL string
}

func Load() Config {
	listenAddr := strings.TrimSpace(os.Getenv("WATCHWEAVER_LISTEN_ADDR"))
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}

	shutdownTimeout := durationEnv("WATCHWEAVER_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)

	databasePath := strings.TrimSpace(os.Getenv("WATCHWEAVER_DATABASE"))
	if databasePath == "" {
		databasePath = defaultDatabasePath
	}

	return Config{
		ListenAddr:        listenAddr,
		ShutdownTimeout:   shutdownTimeout,
		DatabasePath:      databasePath,
		TraktClientID:     strings.TrimSpace(os.Getenv("TRAKT_CLIENT_ID")),
		TraktClientSecret: strings.TrimSpace(os.Getenv("TRAKT_CLIENT_SECRET")),
		TraktBaseURL:      strings.TrimSpace(os.Getenv("TRAKT_BASE_URL")),
		TraktPollInterval: durationEnv("TRAKT_POLL_INTERVAL", defaultTraktPollInterval),
		TraktPollOverlap:  durationEnv("TRAKT_POLL_OVERLAP", defaultTraktPollOverlap),
		DiscordWebhookURL: strings.TrimSpace(os.Getenv("DISCORD_WEBHOOK_URL")),
	}
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
