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
	"github.com/thef4tdaddy/watchweaver/internal/trakt"
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

	traktService := trakt.NewService(db, trakt.Config{ClientID: cfg.TraktClientID, ClientSecret: cfg.TraktClientSecret, BaseURL: cfg.TraktBaseURL})
	httpServer := server.New(cfg.ListenAddr, server.NewHandlerWithAPI(readiness, server.NewAPI(db, traktService)))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startTraktPoller(ctx, db, cfg)

	log.Printf("watchweaver listening on %s", listener.Addr().String())

	if err := server.Serve(ctx, httpServer, listener, cfg.ShutdownTimeout); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("server failed: %v", err)
	}
}

func startTraktPoller(ctx context.Context, db *sql.DB, cfg config.Config) {
	if cfg.TraktClientID == "" || cfg.TraktClientSecret == "" {
		return
	}

	syncer := trakt.NewHistorySync(db, trakt.HistorySyncOptions{
		Poller: trakt.PollerOptions{
			Interval: cfg.TraktPollInterval,
			Overlap:  cfg.TraktPollOverlap,
		},
		ImporterFactory: func(accessToken string) trakt.HistorySyncImporter {
			return trakt.NewHistoryImporter(db, cfg.TraktBaseURL, nil, accessToken)
		},
	})

	go func() {
		if err := syncer.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("trakt history poller stopped: %v", err)
		}
	}()
}

func initialize(readiness *server.Readiness, options persistence.Options) (*sql.DB, error) {
	db, err := persistence.OpenAndMigrate(options)
	if err != nil {
		return nil, err
	}
	readiness.MarkReady()
	return db, nil
}
