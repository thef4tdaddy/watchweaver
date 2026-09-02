package discord

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/serializd"
)

const DefaultInterval = time.Minute

type Options struct {
	WebhookURL string
	HTTPClient *http.Client
	Interval   time.Duration
	Now        func() time.Time
}

type Notifier struct {
	db         *sql.DB
	mu         sync.RWMutex
	webhookURL string
	httpClient *http.Client
	interval   time.Duration
	now        func() time.Time
	serializd  *serializd.Service
}

func (n *Notifier) Configure(webhookURL string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.webhookURL = strings.TrimSpace(webhookURL)
}

func (n *Notifier) Configured() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.webhookURL != ""
}

func (n *Notifier) Test(ctx context.Context) error {
	return n.send(ctx, "WatchWeaver connection test succeeded. Discord announcements are ready.")
}

func NewNotifier(db *sql.DB, options Options) *Notifier {
	if options.HTTPClient == nil {
		options.HTTPClient = http.DefaultClient
	}
	if options.Interval <= 0 {
		options.Interval = DefaultInterval
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	service := serializd.NewService(db)
	service.SetNow(options.Now)
	return &Notifier{db: db, webhookURL: strings.TrimSpace(options.WebhookURL), httpClient: options.HTTPClient, interval: options.Interval, now: options.Now, serializd: service}
}

func (n *Notifier) Run(ctx context.Context) error {
	_ = n.Poll(ctx)
	ticker := time.NewTicker(n.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			_ = n.Poll(ctx)
		}
	}
}

func (n *Notifier) Poll(ctx context.Context) error {
	if !n.Configured() {
		return nil
	}
	if err := n.pollTasks(ctx); err != nil {
		return err
	}
	return n.pollSerializd(ctx)
}

func (n *Notifier) pollTasks(ctx context.Context) error {
	now := n.now().UTC()
	rows, err := n.db.QueryContext(ctx, `SELECT t.id FROM prompt_tasks t LEFT JOIN discord_task_notifications d ON d.prompt_task_id=t.id WHERE t.state IN ('pending','snoozed') AND (d.prompt_task_id IS NULL OR (d.state='pending' AND (d.next_attempt_at IS NULL OR d.next_attempt_at<=?))) ORDER BY t.created_at,t.id LIMIT 50`, now.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	message := fmt.Sprintf("WatchWeaver has %d new rating/review task", len(ids))
	if len(ids) != 1 {
		message += "s"
	}
	message += " ready. Open WatchWeaver to rate, review, snooze, or skip."
	if err := n.send(ctx, message); err != nil {
		return n.recordTaskFailure(ctx, ids, err, now)
	}
	tx, err := n.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO discord_task_notifications(prompt_task_id,state,sent_at,updated_at) VALUES(?,'sent',?,?) ON CONFLICT(prompt_task_id) DO UPDATE SET state='sent',sent_at=excluded.sent_at,next_attempt_at=NULL,last_error=NULL,updated_at=excluded.updated_at`, id, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (n *Notifier) recordTaskFailure(ctx context.Context, ids []int64, sendErr error, now time.Time) error {
	for _, id := range ids {
		var attempts int
		_ = n.db.QueryRowContext(ctx, `SELECT attempt_count FROM discord_task_notifications WHERE prompt_task_id=?`, id).Scan(&attempts)
		attempts++
		delay := time.Duration(1<<min(attempts-1, 6)) * time.Minute
		if retry := retryAfter(sendErr); retry > delay {
			delay = retry
		}
		if _, err := n.db.ExecContext(ctx, `INSERT INTO discord_task_notifications(prompt_task_id,state,attempt_count,next_attempt_at,last_error) VALUES(?,'pending',?,?,?) ON CONFLICT(prompt_task_id) DO UPDATE SET state='pending',attempt_count=excluded.attempt_count,next_attempt_at=excluded.next_attempt_at,last_error=excluded.last_error,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`, id, attempts, now.Add(delay).Format(time.RFC3339Nano), publicError(sendErr)); err != nil {
			return err
		}
	}
	return sendErr
}

func (n *Notifier) pollSerializd(ctx context.Context) error {
	if next, err := n.serializdNextAttempt(ctx); err != nil {
		return err
	} else if next != nil && n.now().UTC().Before(*next) {
		return nil
	}
	options, err := n.serializdOptions(ctx)
	if err != nil {
		return err
	}
	status, err := n.serializd.Status(ctx, options)
	if err != nil {
		return err
	}
	if !status.ReminderAnnouncementPending {
		return nil
	}
	message := fmt.Sprintf("WatchWeaver: it is time to run the Serializd Trakt importer (%d transferable TV changes pending). Open WatchWeaver when finished and choose Mark synced.", status.PendingChanges)
	if err := n.send(ctx, message); err != nil {
		return n.recordSerializdFailure(ctx, err)
	}
	if err := n.serializd.MarkReminderAnnounced(ctx); err != nil {
		return err
	}
	_, err = n.db.ExecContext(ctx, `DELETE FROM integration_state WHERE integration='discord' AND state_key IN ('serializd_attempts','serializd_next_attempt_at','serializd_last_error')`)
	return err
}

func (n *Notifier) serializdNextAttempt(ctx context.Context) (*time.Time, error) {
	var raw string
	err := n.db.QueryRowContext(ctx, `SELECT state_value FROM integration_state WHERE integration='discord' AND state_key='serializd_next_attempt_at'`).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	value, err := time.Parse(time.RFC3339Nano, raw)
	return &value, err
}
func (n *Notifier) recordSerializdFailure(ctx context.Context, sendErr error) error {
	var raw string
	attempts := 0
	if err := n.db.QueryRowContext(ctx, `SELECT state_value FROM integration_state WHERE integration='discord' AND state_key='serializd_attempts'`).Scan(&raw); err == nil {
		attempts, _ = strconv.Atoi(raw)
	} else if err != sql.ErrNoRows {
		return err
	}
	attempts++
	delay := time.Duration(1<<min(attempts-1, 6)) * time.Minute
	if retry := retryAfter(sendErr); retry > delay {
		delay = retry
	}
	values := map[string]string{"serializd_attempts": strconv.Itoa(attempts), "serializd_next_attempt_at": n.now().UTC().Add(delay).Format(time.RFC3339Nano), "serializd_last_error": publicError(sendErr)}
	tx, err := n.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range values {
		if _, err := tx.ExecContext(ctx, `INSERT INTO integration_state(integration,state_key,state_value) VALUES('discord',?,?) ON CONFLICT(integration,state_key) DO UPDATE SET state_value=excluded.state_value,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`, key, value); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return sendErr
}

func (n *Notifier) serializdOptions(ctx context.Context) (serializd.Options, error) {
	options := serializd.Options{ReminderChanges: 20, ReminderDays: 14}
	rows, err := n.db.QueryContext(ctx, `SELECT setting_key,setting_value FROM app_settings WHERE setting_key IN ('serializd_enabled','serializd_reminder_changes','serializd_reminder_days')`)
	if err != nil {
		return options, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return options, err
		}
		switch key {
		case "serializd_enabled":
			options.Enabled, _ = strconv.ParseBool(value)
		case "serializd_reminder_changes":
			options.ReminderChanges, _ = strconv.Atoi(value)
		case "serializd_reminder_days":
			options.ReminderDays, _ = strconv.Atoi(value)
		}
	}
	return options, rows.Err()
}

type webhookError struct {
	status     int
	retryAfter time.Duration
}

func (e *webhookError) Error() string {
	return fmt.Sprintf("Discord webhook returned HTTP %d", e.status)
}
func (n *Notifier) send(ctx context.Context, content string) error {
	n.mu.RLock()
	webhookURL := n.webhookURL
	n.mu.RUnlock()
	if webhookURL == "" {
		return fmt.Errorf("Discord webhook is not configured")
	}
	body, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL+"?wait=true", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("Discord webhook request failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &webhookError{status: resp.StatusCode, retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	return nil
}
func parseRetryAfter(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return time.Duration(value * float64(time.Second))
}
func retryAfter(err error) time.Duration {
	if value, ok := err.(*webhookError); ok {
		return value.retryAfter
	}
	return 0
}
func publicError(err error) string {
	if value, ok := err.(*webhookError); ok {
		return value.Error()
	}
	return "Discord webhook request failed"
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
