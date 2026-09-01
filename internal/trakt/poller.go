package trakt

import (
	"context"
	"database/sql"
	"time"
)

const (
	DefaultPollInterval = 5 * time.Minute
	DefaultPollOverlap = 10 * time.Minute
)

type IncrementalImporter interface {
	ImportIncrementalSince(context.Context, time.Time) (HistoryImportResult, error)
}

type PollerOptions struct {
	Interval time.Duration
	Overlap time.Duration
	Now func() time.Time
	Sleep func(context.Context, time.Duration) error
	MaxRetries int
}

type Poller struct {
	db *sql.DB
	importer IncrementalImporter
	interval time.Duration
	overlap time.Duration
	now func() time.Time
	sleep func(context.Context, time.Duration) error
	maxRetries int
}

func NewPoller(db *sql.DB, importer IncrementalImporter, opts PollerOptions) *Poller {
	if opts.Interval <= 0 { opts.Interval = DefaultPollInterval }
	if opts.Overlap <= 0 { opts.Overlap = DefaultPollOverlap }
	if opts.Now == nil { opts.Now = time.Now }
	if opts.Sleep == nil { opts.Sleep = sleepContext }
	if opts.MaxRetries <= 0 { opts.MaxRetries = 3 }
	return &Poller{db: db, importer: importer, interval: opts.Interval, overlap: opts.Overlap, now: opts.Now, sleep: opts.Sleep, maxRetries: opts.MaxRetries}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done(): return ctx.Err()
	case <-t.C: return nil
	}
}
