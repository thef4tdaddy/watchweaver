package trakt

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

const DefaultAuthorizationCheckInterval = 5 * time.Second

type HistorySyncImporter interface {
	ImportInitial(context.Context) (HistoryImportResult, error)
	ImportIncrementalSince(context.Context, time.Time) (HistoryImportResult, error)
}

type HistorySyncOptions struct {
	Poller                     PollerOptions
	AuthorizationCheckInterval time.Duration
	Sleep                      func(context.Context, time.Duration) error
	ImporterFactory            func(accessToken string) HistorySyncImporter
}

type HistorySync struct {
	db                         *sql.DB
	pollerOptions              PollerOptions
	authorizationCheckInterval time.Duration
	sleep                      func(context.Context, time.Duration) error
	importerFactory            func(string) HistorySyncImporter
}

func NewHistorySync(db *sql.DB, options HistorySyncOptions) *HistorySync {
	if options.Poller.Interval <= 0 {
		options.Poller.Interval = DefaultPollInterval
	}
	if options.AuthorizationCheckInterval <= 0 {
		options.AuthorizationCheckInterval = DefaultAuthorizationCheckInterval
	}
	if options.Sleep == nil {
		options.Sleep = sleepContext
	}
	return &HistorySync{
		db:                         db,
		pollerOptions:              options.Poller,
		authorizationCheckInterval: options.AuthorizationCheckInterval,
		sleep:                      options.Sleep,
		importerFactory:            options.ImporterFactory,
	}
}

func (s *HistorySync) Run(ctx context.Context) error {
	for {
		accessToken, err := s.accessToken(ctx)
		if err != nil {
			return err
		}
		if accessToken == "" {
			if err := s.set(ctx, "history_sync_phase", "waiting_for_authorization"); err != nil {
				return err
			}
			if err := s.sleep(ctx, s.authorizationCheckInterval); err != nil {
				return err
			}
			continue
		}

		importer := s.importerFactory(accessToken)
		if err := s.set(ctx, "history_sync_phase", "initial_sync"); err != nil {
			return err
		}
		if _, err := importer.ImportInitial(ctx); err != nil {
			if persistErr := s.recordFailure(ctx, err); persistErr != nil {
				return persistErr
			}
			delay := s.authorizationCheckInterval
			var retryable *RetryableError
			if errors.As(err, &retryable) && retryable.RetryAfter > delay {
				delay = retryable.RetryAfter
			}
			if err := s.sleep(ctx, delay); err != nil {
				return err
			}
			continue
		}

		if err := s.set(ctx, "history_sync_phase", "polling"); err != nil {
			return err
		}
		poller := NewPoller(s.db, importer, s.pollerOptions)
		_ = poller.Poll(ctx)
		if err := s.sleep(ctx, s.pollerOptions.Interval); err != nil {
			return err
		}
	}
}

func (s *HistorySync) accessToken(ctx context.Context) (string, error) {
	var token string
	err := s.db.QueryRowContext(ctx, `SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='access_token'`).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return token, err
}

func (s *HistorySync) recordFailure(ctx context.Context, syncErr error) error {
	var raw string
	failures := 0
	if err := s.db.QueryRowContext(ctx, `SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='history_poll_failures'`).Scan(&raw); err == nil {
		failures, _ = strconv.Atoi(raw)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := s.set(ctx, "history_poll_last_error", syncErr.Error()); err != nil {
		return err
	}
	return s.set(ctx, "history_poll_failures", strconv.Itoa(failures+1))
}

func (s *HistorySync) set(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt',?,?) ON CONFLICT(integration,state_key) DO UPDATE SET state_value=excluded.state_value,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`, key, value)
	return err
}
