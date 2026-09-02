package trakt

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

var ErrSyncInProgress = errors.New("trakt sync is already running")

type SyncManagerOptions struct {
	BaseURL, ClientID string
	HTTPClient        *http.Client
	AccessToken       func(context.Context) (string, error)
	ClientIDProvider  func(context.Context) (string, error)
	Interval          func(context.Context) time.Duration
	Overlap           time.Duration
	Now               func() time.Time
}

type SyncResult struct {
	StartedAt               time.Time `json:"started_at"`
	CompletedAt             time.Time `json:"completed_at"`
	HistoryChanges          int       `json:"history_changes"`
	RatingChanges           int       `json:"rating_changes"`
	PendingRatingsCompleted int       `json:"pending_ratings_completed"`
	PendingRatingsRemaining int       `json:"pending_ratings_remaining"`
}

type SyncStatus struct {
	Running      bool        `json:"running"`
	LastResult   *SyncResult `json:"last_result,omitempty"`
	LastError    string      `json:"last_error,omitempty"`
	NextRun      *time.Time  `json:"next_run,omitempty"`
	CanSync      bool        `json:"can_sync"`
	RetryAllowed bool        `json:"retry_allowed"`
}

type SyncManager struct {
	db      *sql.DB
	options SyncManagerOptions
	running atomic.Bool
	wake    chan struct{}
}

func NewSyncManager(db *sql.DB, options SyncManagerOptions) *SyncManager {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Overlap <= 0 {
		options.Overlap = DefaultPollOverlap
	}
	return &SyncManager{db: db, options: options, wake: make(chan struct{}, 1)}
}

// Trigger requests an early background cycle without blocking the caller.
func (m *SyncManager) Trigger() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *SyncManager) Run(ctx context.Context) error {
	delay := m.nextDelay(ctx, m.interval(ctx))
	for {
		next := m.options.Now().UTC().Add(delay)
		_ = m.set(ctx, "sync_next_run", next.Format(time.RFC3339Nano))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-m.wake:
			timer.Stop()
		case <-timer.C:
		}
		_ = m.SyncNow(ctx)
		delay = m.interval(ctx)
	}
}

func (m *SyncManager) interval(ctx context.Context) time.Duration {
	interval := DefaultPollInterval
	if m.options.Interval != nil {
		interval = m.options.Interval(ctx)
	}
	if interval <= 0 {
		return DefaultPollInterval
	}
	return interval
}

func (m *SyncManager) nextDelay(ctx context.Context, interval time.Duration) time.Duration {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	var raw string
	err := m.db.QueryRowContext(ctx, `SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='sync_last_completed'`).Scan(&raw)
	if err != nil {
		return 0
	}
	completed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return 0
	}
	remaining := completed.Add(interval).Sub(m.options.Now().UTC())
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (m *SyncManager) SyncNow(ctx context.Context) error {
	if !m.running.CompareAndSwap(false, true) {
		return ErrSyncInProgress
	}
	defer m.running.Store(false)
	started := m.options.Now().UTC()
	_ = m.set(ctx, "sync_last_started", started.Format(time.RFC3339Nano))
	_ = m.set(ctx, "history_sync_phase", "syncing")
	result, err := m.cycle(ctx, started)
	if err != nil {
		_ = m.set(ctx, "sync_last_error", err.Error())
		_ = m.set(ctx, "history_sync_phase", "error")
		return err
	}
	completed := m.options.Now().UTC()
	result.CompletedAt = completed
	values := map[string]string{
		"sync_last_completed": completed.Format(time.RFC3339Nano), "sync_last_error": "",
		"sync_history_changes": strconv.Itoa(result.HistoryChanges), "sync_rating_changes": strconv.Itoa(result.RatingChanges),
		"sync_pending_completed": strconv.Itoa(result.PendingRatingsCompleted), "sync_pending_remaining": strconv.Itoa(result.PendingRatingsRemaining),
	}
	for key, value := range values {
		if err := m.set(ctx, key, value); err != nil {
			return err
		}
	}
	return m.set(ctx, "history_sync_phase", "polling")
}

func (m *SyncManager) cycle(ctx context.Context, started time.Time) (SyncResult, error) {
	result := SyncResult{StartedAt: started}
	token, err := m.options.AccessToken(ctx)
	if err != nil {
		return result, err
	}
	if token == "" {
		_ = m.set(ctx, "history_sync_phase", "waiting_for_authorization")
		return result, errors.New("Trakt authorization is required")
	}
	clientID := m.options.ClientID
	if m.options.ClientIDProvider != nil {
		clientID, err = m.options.ClientIDProvider(ctx)
		if err != nil {
			return result, err
		}
	}
	beforeHistory, beforeRatings, beforePending, err := m.snapshot(ctx)
	if err != nil {
		return result, err
	}
	importer := NewHistoryImporter(m.db, m.options.BaseURL, m.options.HTTPClient, token)
	importer.SetClientID(clientID)
	if _, err := importer.ImportInitial(ctx); err != nil {
		return result, err
	}
	poller := NewPoller(m.db, importer, PollerOptions{Overlap: m.options.Overlap, MaxRetries: 3, Now: m.options.Now})
	if err := poller.Poll(ctx); err != nil {
		return result, err
	}
	ratings := NewRatingSync(m.db, m.options.BaseURL, m.options.HTTPClient, clientID, token)
	ratings.SetNow(m.options.Now)
	if err := ratings.ImportInitial(ctx); err != nil {
		return result, err
	}
	if err := ratings.FlushPending(ctx); err != nil {
		return result, err
	}
	if err := ratings.Reconcile(ctx); err != nil {
		return result, err
	}
	afterHistory, afterRatings, afterPending, err := m.snapshot(ctx)
	if err != nil {
		return result, err
	}
	result.HistoryChanges = max(0, afterHistory-beforeHistory)
	for mediaID, rating := range afterRatings {
		if previous, ok := beforeRatings[mediaID]; !ok || previous != rating {
			result.RatingChanges++
		}
	}
	result.PendingRatingsCompleted = max(0, beforePending-afterPending)
	result.PendingRatingsRemaining = afterPending
	return result, nil
}

func (m *SyncManager) snapshot(ctx context.Context) (history int, ratings map[int64]int, pending int, err error) {
	if err = m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM watch_events WHERE source='trakt'`).Scan(&history); err != nil {
		return
	}
	ratings = map[int64]int{}
	rows, queryErr := m.db.QueryContext(ctx, `SELECT media_id,rating FROM ratings`)
	if queryErr != nil {
		err = queryErr
		return
	}
	defer rows.Close()
	for rows.Next() {
		var mediaID int64
		var rating int
		if scanErr := rows.Scan(&mediaID, &rating); scanErr != nil {
			err = scanErr
			return
		}
		ratings[mediaID] = rating
	}
	if err = rows.Err(); err != nil {
		return
	}
	err = m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM rating_sync_state WHERE pending_rating IS NOT NULL OR pending_delete=1`).Scan(&pending)
	return
}

func (m *SyncManager) Status(ctx context.Context) (SyncStatus, error) {
	status := SyncStatus{Running: m.running.Load(), CanSync: true}
	read := func(key string) (string, error) {
		var v string
		err := m.db.QueryRowContext(ctx, `SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key=?`, key).Scan(&v)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return v, err
	}
	lastError, err := read("sync_last_error")
	if err != nil {
		return status, err
	}
	status.LastError = lastError
	status.RetryAllowed = lastError != "" && !status.Running
	next, err := read("sync_next_run")
	if err != nil {
		return status, err
	}
	if next != "" {
		parsed, e := time.Parse(time.RFC3339Nano, next)
		if e != nil {
			return status, e
		}
		status.NextRun = &parsed
	}
	started, err := read("sync_last_started")
	if err != nil {
		return status, err
	}
	completed, err := read("sync_last_completed")
	if err != nil {
		return status, err
	}
	if completed != "" {
		s, _ := time.Parse(time.RFC3339Nano, started)
		c, e := time.Parse(time.RFC3339Nano, completed)
		if e != nil {
			return status, e
		}
		result := SyncResult{StartedAt: s, CompletedAt: c}
		fields := []struct {
			key string
			out *int
		}{{"sync_history_changes", &result.HistoryChanges}, {"sync_rating_changes", &result.RatingChanges}, {"sync_pending_completed", &result.PendingRatingsCompleted}, {"sync_pending_remaining", &result.PendingRatingsRemaining}}
		for _, field := range fields {
			raw, e := read(field.key)
			if e != nil {
				return status, e
			}
			fieldValue, _ := strconv.Atoi(raw)
			*field.out = fieldValue
		}
		status.LastResult = &result
	}
	return status, nil
}

func (m *SyncManager) set(ctx context.Context, key, value string) error {
	_, err := m.db.ExecContext(ctx, `INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt',?,?) ON CONFLICT(integration,state_key) DO UPDATE SET state_value=excluded.state_value,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`, key, value)
	return err
}
