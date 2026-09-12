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
	"strings"
	"syscall"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/config"
	"github.com/thef4tdaddy/watchweaver/internal/credentials"
	"github.com/thef4tdaddy/watchweaver/internal/discord"
	"github.com/thef4tdaddy/watchweaver/internal/jellyfin"
	"github.com/thef4tdaddy/watchweaver/internal/jellyfinremote"
	"github.com/thef4tdaddy/watchweaver/internal/logging"
	"github.com/thef4tdaddy/watchweaver/internal/persistence"
	"github.com/thef4tdaddy/watchweaver/internal/server"
	"github.com/thef4tdaddy/watchweaver/internal/trakt"
)

var version = "dev"
var revision = ""

func main() {
	cfg := config.Load()
	logging.SetDebug(cfg.DebugLogging)
	if cfg.DebugLogging {
		log.Printf("debug logging enabled; sensitive values remain redacted")
	}
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
	traktSync := trakt.NewSyncManager(db, trakt.SyncManagerOptions{
		BaseURL: cfg.TraktBaseURL, ClientID: traktClientID, Overlap: cfg.TraktPollOverlap,
		AccessToken:          func(ctx context.Context) (string, error) { return credentialStore.Get(ctx, "trakt", "access_token") },
		RefreshAuthorization: traktService.Refresh,
		ClientIDProvider:     func(ctx context.Context) (string, error) { return credentialStore.Get(ctx, "trakt", "client_id") },
		Interval: func(ctx context.Context) time.Duration {
			return applicationPollInterval(ctx, db, cfg.TraktPollInterval)
		},
	})
	discordNotifier := discord.NewNotifier(db, discord.Options{SkipTaskNotifications: strings.TrimSpace(os.Getenv("WATCHWEAVER_BOT_LISTEN_ADDR")) != "" && os.Getenv("WATCHWEAVER_BOT_NOTIFICATIONS") == "true"})
	discordEnabled, discordPreferenceSet := server.DiscordPreference(context.Background(), db)
	if discordEnabled || (!discordPreferenceSet && cfg.DiscordWebhookURL != "") {
		discordNotifier.Configure(discordWebhook)
	}
	api := server.NewAPI(db, traktService)
	api.SetBuildInfo(version, revision)
	api.SetCredentialStore(credentialStore)
	api.SetDiscordNotifier(discordNotifier)
	api.SetTraktSyncManager(traktSync)
	jellyfinRemotes := jellyfinremote.NewPool(nil, jellyfin.NewService(db))
	if err := server.LoadJellyfinRemoteSources(context.Background(), db, credentialStore, jellyfinRemotes); err != nil {
		log.Fatalf("load remote Jellyfin connections failed: %v", err)
	}
	api.SetJellyfinRemotePool(jellyfinRemotes)
	httpServer := server.New(cfg.ListenAddr, server.NewHandlerWithAPI(readiness, api))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if addr := strings.TrimSpace(os.Getenv("WATCHWEAVER_BOT_LISTEN_ADDR")); addr != "" {
		handler, err := server.NewBotHandler(api, server.BotConfig{
			Notifications: os.Getenv("WATCHWEAVER_BOT_NOTIFICATIONS") == "true",
			TokenProvider: server.BotTokenFile(os.Getenv("WATCHWEAVER_BOT_TOKEN_FILE")),
			Users:         strings.Split(os.Getenv("WATCHWEAVER_BOT_USER_IDS"), ","),
			PublicURL:     os.Getenv("WATCHWEAVER_PUBLIC_URL"),
		})
		if err != nil {
			log.Fatalf("bot API configuration: %v", err)
		}
		botListener, err := net.Listen("tcp", addr)
		if err != nil {
			log.Fatalf("bot API listen failed: %v", err)
		}
		go func() {
			for ctx.Err() == nil {
				if err := api.RunBotWorker(ctx, os.Getenv("WATCHWEAVER_BOT_NOTIFICATIONS") == "true"); err != nil && ctx.Err() == nil {
					log.Printf("bot worker paused; retrying in 30 seconds")
				}
				timer := time.NewTimer(30 * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
		botServer := server.New(addr, handler)
		botServer.ReadHeaderTimeout = 5 * time.Second
		botServer.ReadTimeout = 15 * time.Second
		botServer.WriteTimeout = 30 * time.Second
		botServer.IdleTimeout = 60 * time.Second
		botServer.MaxHeaderBytes = 16 * 1024
		go func() {
			if err := server.Serve(ctx, botServer, botListener, cfg.ShutdownTimeout); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("bot API stopped unexpectedly; restart required to restore bot access")
			}
		}()
	}

	go func() {
		if err := traktSync.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("Trakt sync manager stopped: %v", err)
		}
	}()
	startDiscordNotifier(ctx, discordNotifier)
	go func() {
		if err := jellyfinRemotes.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("Jellyfin remote connection stopped: %v", err)
		}
	}()

	log.Printf("watchweaver listening on %s", listener.Addr().String())

	if err := server.Serve(ctx, httpServer, listener, cfg.ShutdownTimeout); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("server failed: %v", err)
	}
}

func startDiscordNotifier(ctx context.Context, notifier *discord.Notifier) {
	go func() { _ = notifier.Run(ctx) }()
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
