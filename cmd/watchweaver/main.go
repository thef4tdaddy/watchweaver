package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/config"
	"github.com/thef4tdaddy/watchweaver/internal/discord"
	"github.com/thef4tdaddy/watchweaver/internal/persistence"
	"github.com/thef4tdaddy/watchweaver/internal/server"
	"github.com/thef4tdaddy/watchweaver/internal/trakt"
)

func main() {
	cfg := config.Load()
	if len(os.Args) > 1 {
		if os.Args[1] != "backup" {
			log.Fatalf("unknown command %q (supported: backup)", os.Args[1])
		}
		destination := ""
		if len(os.Args) > 2 {
			destination = os.Args[2]
		} else {
			destination = filepath.Join(filepath.Dir(cfg.DatabasePath), "backups", "watchweaver-"+time.Now().UTC().Format("20060102T150405Z")+".db")
		}
		db, err := persistence.OpenAndMigrate(persistence.Options{Path: cfg.DatabasePath})
		if err != nil {
			log.Fatalf("open database for backup: %v", err)
		}
		defer db.Close()
		if err := persistence.Backup(db, destination); err != nil {
			log.Fatalf("backup failed: %v", err)
		}
		log.Printf("backup created: %s", destination)
		return
	}
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
	api := server.NewAPI(db, traktService)
	api.SetDiscordConfigured(cfg.DiscordWebhookURL != "")
	httpServer := server.New(cfg.ListenAddr, server.NewHandlerWithAPI(readiness, api))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startTraktPoller(ctx, db, cfg)
	startDiscordNotifier(ctx, db, cfg)

	log.Printf("watchweaver listening on %s", listener.Addr().String())

	if err := server.Serve(ctx, httpServer, listener, cfg.ShutdownTimeout); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("server failed: %v", err)
	}
}

func startDiscordNotifier(ctx context.Context, db *sql.DB, cfg config.Config) {
	if cfg.DiscordWebhookURL == "" {
		return
	}
	notifier := discord.NewNotifier(db, discord.Options{WebhookURL: cfg.DiscordWebhookURL})
	go func() { _ = notifier.Run(ctx) }()
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
