package serializd

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type ReviewTransfer struct {
	ReviewID        int64      `json:"review_id"`
	MediaID         int64      `json:"media_id"`
	MediaType       string     `json:"media_type"`
	Title           string     `json:"title"`
	ShowTitle       string     `json:"show_title,omitempty"`
	SeasonNumber    *int       `json:"season_number,omitempty"`
	EpisodeNumber   *int       `json:"episode_number,omitempty"`
	Rating          *int       `json:"rating,omitempty"`
	Body            string     `json:"body"`
	ReviewUpdatedAt time.Time  `json:"review_updated_at"`
	TransferredAt   *time.Time `json:"transferred_at,omitempty"`
}

const ImportURL = "https://www.serializd.com/trakt"

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
	TrackedEpisodeWatches       int        `json:"tracked_episode_watches"`
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
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM watch_events w JOIN media_items m ON m.id=w.media_id WHERE m.media_type='episode' AND w.deleted_at IS NULL`).Scan(&status.TrackedEpisodeWatches); err != nil {
		return Status{}, err
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

func (s *Service) Reviews(ctx context.Context, includeTransferred bool) ([]ReviewTransfer, error) {
	query := `SELECT r.id,r.media_id,m.media_type,m.title,
		COALESCE(show.title,''),season.season_number,m.episode_number,ratings.rating,r.body,r.updated_at,
		CASE WHEN t.review_updated_at=r.updated_at THEN t.transferred_at ELSE NULL END
	FROM reviews r JOIN media_items m ON m.id=r.media_id
	LEFT JOIN media_items season ON (m.media_type='episode' AND season.id=m.parent_id) OR (m.media_type='season' AND season.id=m.id)
	LEFT JOIN media_items show ON show.id=season.parent_id
	LEFT JOIN ratings ON ratings.media_id=m.id
	LEFT JOIN serializd_review_transfers t ON t.review_id=r.id
	WHERE m.media_type IN ('season','episode')`
	if !includeTransferred {
		query += ` AND (t.review_id IS NULL OR t.review_updated_at<>r.updated_at)`
	}
	query += ` ORDER BY r.updated_at DESC,r.id DESC`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ReviewTransfer{}
	for rows.Next() {
		var item ReviewTransfer
		var season, episode, rating sql.NullInt64
		var updated string
		var transferred sql.NullString
		if err := rows.Scan(&item.ReviewID, &item.MediaID, &item.MediaType, &item.Title, &item.ShowTitle, &season, &episode, &rating, &item.Body, &updated, &transferred); err != nil {
			return nil, err
		}
		item.ReviewUpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, err
		}
		if season.Valid {
			v := int(season.Int64)
			item.SeasonNumber = &v
		}
		if episode.Valid {
			v := int(episode.Int64)
			item.EpisodeNumber = &v
		}
		if rating.Valid {
			v := int(rating.Int64)
			item.Rating = &v
		}
		if transferred.Valid {
			v, parseErr := time.Parse(time.RFC3339Nano, transferred.String)
			if parseErr != nil {
				return nil, parseErr
			}
			item.TransferredAt = &v
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) SetReviewTransferred(ctx context.Context, reviewID int64, transferred bool) error {
	if !transferred {
		result, err := s.db.ExecContext(ctx, `DELETE FROM serializd_review_transfers WHERE review_id=?`, reviewID)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			return sql.ErrNoRows
		}
		return nil
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO serializd_review_transfers(review_id,review_updated_at,transferred_at) SELECT id,updated_at,? FROM reviews WHERE id=? ON CONFLICT(review_id) DO UPDATE SET review_updated_at=excluded.review_updated_at,transferred_at=excluded.transferred_at`, s.now().UTC().Format(time.RFC3339Nano), reviewID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
