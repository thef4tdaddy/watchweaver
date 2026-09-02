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
	"github.com/thef4tdaddy/watchweaver/internal/credentials"
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
		keyPath := credentials.DefaultKeyPath(cfg.DatabasePath)
		if _, err := os.Stat(keyPath); err == nil {
			if err := credentials.BackupKey(keyPath, destination+".key"); err != nil {
				log.Fatalf("credential key backup failed: %v", err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			log.Fatalf("inspect credential key for backup: %v", err)
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

	credentialStore, err := credentials.Open(db, credentials.DefaultKeyPath(cfg.DatabasePath), credentials.Overrides{TraktClientID: cfg.TraktClientID, TraktClientSecret: cfg.TraktClientSecret, DiscordWebhookURL: cfg.DiscordWebhookURL})
	if err != nil {
		log.Fatalf("credential storage initialization failed: %v", err)
	}
	traktClientID, err := credentialStore.Get(context.Background(), "trakt", "client_id")
	if err != nil {
		log.Fatalf("load Trakt client ID failed: %v", err)
	}
	traktClientSecret, err := credentialStore.Get(context.Background(), "trakt", "client_secret")
	if err != nil {
		log.Fatalf("load Trakt client secret failed: %v", err)
	}
	discordWebhook, err := credentialStore.Get(context.Background(), "discord", "webhook_url")
	if err != nil {
		log.Fatalf("load Discord webhook failed: %v", err)
	}
	traktService := trakt.NewService(db, trakt.Config{ClientID: traktClientID, ClientSecret: traktClientSecret, BaseURL: cfg.TraktBaseURL, SecretStore: credentialStore})
	discordNotifier := discord.NewNotifier(db, discord.Options{})
	discordEnabled, discordPreferenceSet := server.DiscordPreference(context.Background(), db)
	if discordEnabled || (!discordPreferenceSet && cfg.DiscordWebhookURL != "") {
		discordNotifier.Configure(discordWebhook)
	}
	api := server.NewAPI(db, traktService)
	api.SetCredentialStore(credentialStore)
	api.SetDiscordNotifier(discordNotifier)
	httpServer := server.New(cfg.ListenAddr, server.NewHandlerWithAPI(readiness, api))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startTraktPoller(ctx, db, cfg, credentialStore)
	startTraktRatingSync(ctx, db, cfg, credentialStore)
	startDiscordNotifier(ctx, discordNotifier)

	log.Printf("watchweaver listening on %s", listener.Addr().String())

	if err := server.Serve(ctx, httpServer, listener, cfg.ShutdownTimeout); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("server failed: %v", err)
	}
}

func startDiscordNotifier(ctx context.Context, notifier *discord.Notifier) {
	go func() { _ = notifier.Run(ctx) }()
}

func startTraktPoller(ctx context.Context, db *sql.DB, cfg config.Config, credentialStore *credentials.Store) {
	syncer := trakt.NewHistorySync(db, trakt.HistorySyncOptions{
		Poller: trakt.PollerOptions{
			Interval: cfg.TraktPollInterval,
			Overlap:  cfg.TraktPollOverlap,
		},
		ImporterFactory: func(accessToken string) trakt.HistorySyncImporter {
			importer := trakt.NewHistoryImporter(db, cfg.TraktBaseURL, nil, accessToken)
			clientID, _ := credentialStore.Get(context.Background(), "trakt", "client_id")
			importer.SetClientID(clientID)
			return importer
		},
		AccessToken: func(ctx context.Context) (string, error) { return credentialStore.Get(ctx, "trakt", "access_token") },
		PollInterval: func(ctx context.Context) time.Duration {
			return applicationPollInterval(ctx, db, cfg.TraktPollInterval)
		},
	})

	go func() {
		if err := syncer.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("trakt history poller stopped: %v", err)
		}
	}()
}

func startTraktRatingSync(ctx context.Context, db *sql.DB, cfg config.Config, credentialStore *credentials.Store) {
	go func() {
		for {
			clientID, _ := credentialStore.Get(ctx, "trakt", "client_id")
			accessToken, _ := credentialStore.Get(ctx, "trakt", "access_token")
			if clientID != "" && accessToken != "" {
				syncer := trakt.NewRatingSync(db, cfg.TraktBaseURL, nil, clientID, accessToken)
				_ = syncer.ImportInitial(ctx)
				_ = syncer.FlushPending(ctx)
				_ = syncer.Reconcile(ctx)
			}
			timer := time.NewTimer(applicationPollInterval(ctx, db, cfg.TraktPollInterval))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

func applicationPollInterval(ctx context.Context, db *sql.DB, fallback time.Duration) time.Duration {
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT setting_value FROM app_settings WHERE setting_key='trakt_poll_minutes'`).Scan(&raw); err == nil {
		if minutes, err := time.ParseDuration(raw + "m"); err == nil && minutes >= time.Minute && minutes <= 24*time.Hour {
			return minutes
		}
	}
	return fallback
}

func initialize(readiness *server.Readiness, options persistence.Options) (*sql.DB, error) {
	db, err := persistence.OpenAndMigrate(options)
	if err != nil {
		return nil, err
	}
	readiness.MarkReady()
	return db, nil
}
