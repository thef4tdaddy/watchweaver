package config

import (
	"os"
	"strings"
	"time"
)

const (
	defaultListenAddr      = ":8080"
	defaultShutdownTimeout = 10 * time.Second
	defaultDatabasePath    = "/data/watchweaver.db"
)

type Config struct {
	ListenAddr      string
	ShutdownTimeout time.Duration
	DatabasePath    string
}

func Load() Config {
	listenAddr := strings.TrimSpace(os.Getenv("WATCHWEAVER_LISTEN_ADDR"))
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}

	shutdownTimeout := defaultShutdownTimeout
	if raw := strings.TrimSpace(os.Getenv("WATCHWEAVER_SHUTDOWN_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			shutdownTimeout = parsed
		}
	}

	databasePath := strings.TrimSpace(os.Getenv("WATCHWEAVER_DATABASE"))
	if databasePath == "" {
		databasePath = defaultDatabasePath
	}

	return Config{
		ListenAddr:      listenAddr,
		ShutdownTimeout: shutdownTimeout,
		DatabasePath:    databasePath,
	}
}
