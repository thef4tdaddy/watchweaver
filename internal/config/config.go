package config

import (
	"os"
	"strings"
	"time"
)

const (
	defaultListenAddr     = ":8080"
	defaultShutdownTimout = 10 * time.Second
)

type Config struct {
	ListenAddr      string
	ShutdownTimeout time.Duration
}

func Load() Config {
	listenAddr := strings.TrimSpace(os.Getenv("WATCHWEAVER_LISTEN_ADDR"))
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}

	shutdownTimeout := defaultShutdownTimout
	if raw := strings.TrimSpace(os.Getenv("WATCHWEAVER_SHUTDOWN_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			shutdownTimeout = parsed
		}
	}

	return Config{
		ListenAddr:      listenAddr,
		ShutdownTimeout: shutdownTimeout,
	}
}
