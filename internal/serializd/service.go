package serializd

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const ImportURL = "https://www.serializd.com/settings/import"

type Options struct {
	Enabled         bool
	ReminderChanges int
	ReminderDays    int
}

type Status struct {
	Enabled                     bool       `json:"enabled"`
	LastConfirmedAt             *time.Time `json:"last_confirmed_at,omitempty"`
	PendingChanges              int        `json:"pending_changes"`
	PendingEpisodeWatches       int        `json:"pending_episode_watches"`
	PendingRatingChanges        int        `json:"pending_rating_changes"`
	OldestPendingAt             *time.Time `json:"oldest_pending_at,omitempty"`
	CountThresholdReached       bool       `json:"count_threshold_reached"`
	ElapsedThresholdReached     bool       `json:"elapsed_threshold_reached"`
	Due                         bool       `json:"due"`
	ReminderAnnouncementPending bool       `json:"reminder_announcement_pending"`
	UnsupportedSeasonRatings    int        `json:"unsupported_season_ratings"`
	UnsupportedTVReviews        int        `json:"unsupported_tv_reviews"`
	ReminderChanges             int        `json:"reminder_changes"`
	ReminderDays                int        `json:"reminder_days"`
	ImportURL                   string     `json:"import_url"`
}

type Service struct {
	db  *sql.DB
	now func() time.Time
}

func NewService(db *sql.DB) *Service { return &Service{db: db, now: time.Now} }
func (s *Service) SetNow(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

func (s *Service) Status(ctx context.Context, options Options) (Status, error) {
	status := Status{Enabled: options.Enabled, ReminderChanges: options.ReminderChanges, ReminderDays: options.ReminderDays, ImportURL: ImportURL}
	var checkpoint int64
	var confirmed sql.NullString
	var wasDue, announced int
	if err := s.db.QueryRowContext(ctx, `SELECT last_change_id,confirmed_at,due,reminder_announced FROM serializd_checkpoint WHERE singleton=1`).Scan(&checkpoint, &confirmed, &wasDue, &announced); err != nil {
		return Status{}, err
	}
	if confirmed.Valid {
		parsed, err := time.Parse(time.RFC3339Nano, confirmed.String)
		if err != nil {
			return Status{}, err
		}
		status.LastConfirmedAt = &parsed
	}
	var oldest sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN change_type='episode_watch' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN change_type='episode_rating' THEN 1 ELSE 0 END),0),MIN(occurred_at) FROM serializd_changes WHERE id>?`, checkpoint).Scan(&status.PendingChanges, &status.PendingEpisodeWatches, &status.PendingRatingChanges, &oldest); err != nil {
		return Status{}, err
	}
	if oldest.Valid {
		parsed, err := time.Parse(time.RFC3339Nano, oldest.String)
		if err != nil {
			return Status{}, err
		}
		status.OldestPendingAt = &parsed
	}
	status.CountThresholdReached = status.PendingChanges > 0 && options.ReminderChanges > 0 && status.PendingChanges >= options.ReminderChanges
	if status.PendingChanges > 0 && options.ReminderDays > 0 {
		reference := status.OldestPendingAt
		if status.LastConfirmedAt != nil {
			reference = status.LastConfirmedAt
		}
		if reference != nil {
			status.ElapsedThresholdReached = !s.now().Before(reference.Add(time.Duration(options.ReminderDays) * 24 * time.Hour))
		}
	}
	status.Due = options.Enabled && (status.CountThresholdReached || status.ElapsedThresholdReached)
	if status.Due && wasDue == 0 {
		announced = 0
	}
	if !status.Due {
		announced = 0
	}
	status.ReminderAnnouncementPending = status.Due && announced == 0
	if boolInt(status.Due) != wasDue || (!status.Due && announced != 0) {
		if _, err := s.db.ExecContext(ctx, `UPDATE serializd_checkpoint SET due=?,reminder_announced=?,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE singleton=1`, boolInt(status.Due), announced); err != nil {
			return Status{}, err
		}
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ratings r JOIN media_items m ON m.id=r.media_id WHERE m.media_type='season'`).Scan(&status.UnsupportedSeasonRatings); err != nil {
		return Status{}, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM reviews r JOIN media_items m ON m.id=r.media_id WHERE m.media_type IN ('season','episode')`).Scan(&status.UnsupportedTVReviews); err != nil {
		return Status{}, err
	}
	return status, nil
}

func (s *Service) MarkSynced(ctx context.Context, options Options) (Status, error) {
	var highWater int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM serializd_changes`).Scan(&highWater); err != nil {
		return Status{}, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, `UPDATE serializd_checkpoint SET last_change_id=?,confirmed_at=?,due=0,reminder_announced=0,updated_at=? WHERE singleton=1`, highWater, now, now); err != nil {
		return Status{}, err
	}
	return s.Status(ctx, options)
}

func (s *Service) MarkReminderAnnounced(ctx context.Context) error {
	result, err := s.db.ExecContext(ctx, `UPDATE serializd_checkpoint SET reminder_announced=1,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE singleton=1 AND due=1`)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return errors.New("Serializd reminder is not due")
	}
	return nil
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
