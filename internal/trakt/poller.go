package trakt

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

const (
	DefaultPollInterval = 5 * time.Minute
	DefaultPollOverlap  = 10 * time.Minute
)

type IncrementalImporter interface {
	ImportIncrementalSince(context.Context, time.Time) (HistoryImportResult, error)
}

type PollerOptions struct {
	Interval   time.Duration
	Overlap    time.Duration
	Now        func() time.Time
	Sleep      func(context.Context, time.Duration) error
	MaxRetries int
}

type Poller struct {
	db         *sql.DB
	importer   IncrementalImporter
	interval   time.Duration
	overlap    time.Duration
	now        func() time.Time
	sleep      func(context.Context, time.Duration) error
	maxRetries int
}

type PollStatus struct {
	LastSuccess         *time.Time
	LastError           string
	ConsecutiveFailures int
}

func NewPoller(db *sql.DB, importer IncrementalImporter, o PollerOptions) *Poller {
	if o.Interval <= 0 {
		o.Interval = DefaultPollInterval
	}
	if o.Overlap <= 0 {
		o.Overlap = DefaultPollOverlap
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = sleepContext
	}
	if o.MaxRetries <= 0 {
		o.MaxRetries = 3
	}
	return &Poller{
		db:         db,
		importer:   importer,
		interval:   o.Interval,
		overlap:    o.Overlap,
		now:        o.Now,
		sleep:      o.Sleep,
		maxRetries: o.MaxRetries,
	}
}

func (p *Poller) Run(ctx context.Context) error {
	_ = p.Poll(ctx)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			_ = p.Poll(ctx)
		}
	}
}

func (p *Poller) Poll(ctx context.Context) error {
	checkpoint, err := p.checkpoint(ctx)
	if err != nil {
		return err
	}
	since := checkpoint.Add(-p.overlap)

	var lastErr error
	for attempt := 0; attempt < p.maxRetries; attempt++ {
		_, lastErr = p.importer.ImportIncrementalSince(ctx, since)
		if lastErr == nil {
			now := p.now().UTC()
			if err := p.set(ctx, "history_poll_checkpoint", now.Format(time.RFC3339Nano)); err != nil {
				return err
			}
			if err := p.set(ctx, "history_poll_last_success", now.Format(time.RFC3339Nano)); err != nil {
				return err
			}
			if err := p.set(ctx, "history_poll_last_error", ""); err != nil {
				return err
			}
			if err := p.set(ctx, "history_poll_failures", "0"); err != nil {
				return err
			}
			return nil
		}

		if err := p.set(ctx, "history_poll_last_error", lastErr.Error()); err != nil {
			return err
		}
		if err := p.set(ctx, "history_poll_failures", strconv.Itoa(attempt+1)); err != nil {
			return err
		}
		if attempt+1 < p.maxRetries {
			if err := p.sleep(ctx, time.Second<<attempt); err != nil {
				return err
			}
		}
	}

	return fmt.Errorf("trakt history poll failed after %d attempts: %w", p.maxRetries, lastErr)
}

func (p *Poller) checkpoint(ctx context.Context) (time.Time, error) {
	var raw string
	err := p.db.QueryRowContext(ctx, `SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='history_poll_checkpoint'`).Scan(&raw)
	if err == sql.ErrNoRows {
		var max sql.NullString
		if err := p.db.QueryRowContext(ctx, `SELECT MAX(watched_at_utc) FROM watch_events WHERE source='trakt'`).Scan(&max); err != nil {
			return time.Time{}, err
		}
		if !max.Valid {
			return p.now().UTC(), nil
		}
		return time.Parse(time.RFC3339, max.String)
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, raw)
}

func (p *Poller) set(ctx context.Context, key, value string) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt',?,?) ON CONFLICT(integration,state_key) DO UPDATE SET state_value=excluded.state_value,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`, key, value)
	return err
}

func (p *Poller) Status(ctx context.Context) (PollStatus, error) {
	var status PollStatus
	var raw string

	err := p.db.QueryRowContext(ctx, `SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='history_poll_last_success'`).Scan(&raw)
	if err == nil {
		parsed, parseErr := time.Parse(time.RFC3339Nano, raw)
		if parseErr != nil {
			return status, parseErr
		}
		status.LastSuccess = &parsed
	} else if err != sql.ErrNoRows {
		return status, err
	}

	err = p.db.QueryRowContext(ctx, `SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='history_poll_last_error'`).Scan(&status.LastError)
	if err != nil && err != sql.ErrNoRows {
		return status, err
	}

	err = p.db.QueryRowContext(ctx, `SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='history_poll_failures'`).Scan(&raw)
	if err == nil {
		failures, parseErr := strconv.Atoi(raw)
		if parseErr != nil {
			return status, parseErr
		}
		status.ConsecutiveFailures = failures
	} else if err != sql.ErrNoRows {
		return status, err
	}

	return status, nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
