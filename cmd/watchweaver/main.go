package main

import (
	"context"
	"errors"
	"log"
	"net"
	"os/signal"
	"syscall"

	"github.com/thef4tdaddy/watchweaver/internal/config"
	"github.com/thef4tdaddy/watchweaver/internal/server"
)

func main() {
	cfg := config.Load()

	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		log.Fatalf("listen failed: %v", err)
	}

	httpServer := server.New(cfg.ListenAddr, server.NewHandler())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("watchweaver listening on %s", listener.Addr().String())

	if err := server.Serve(ctx, httpServer, listener, cfg.ShutdownTimeout); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("server failed: %v", err)
	}
}
