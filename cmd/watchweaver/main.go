package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net"
	"os/signal"
	"syscall"

	"github.com/thef4tdaddy/watchweaver/internal/config"
	"github.com/thef4tdaddy/watchweaver/internal/persistence"
	"github.com/thef4tdaddy/watchweaver/internal/server"
)

func main() {
	cfg := config.Load()
	readiness := server.NewReadiness()

	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		log.Fatalf("listen failed: %v", err)
	}

	db, err := initialize(readiness, persistence.Options{Path: cfg.DatabasePath})
	if err != nil {
		log.Fatalf("startup initialization failed: %v", err)
	}
	defer db.Close()

	httpServer := server.New(cfg.ListenAddr, server.NewHandler(readiness))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("watchweaver listening on %s", listener.Addr().String())

	if err := server.Serve(ctx, httpServer, listener, cfg.ShutdownTimeout); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("server failed: %v", err)
	}
}

func initialize(readiness *server.Readiness, options persistence.Options) (*sql.DB, error) {
	db, err := persistence.OpenAndMigrate(options)
	if err != nil {
		return nil, err
	}
	readiness.MarkReady()
	return db, nil
}
