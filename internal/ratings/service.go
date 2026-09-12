package ratings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/thef4tdaddy/watchweaver/internal/workflow"
	"strings"
	"time"
)

var ErrInvalidRating = errors.New("rating must be an integer from 1 to 10")
var ErrUnsupportedTarget = errors.New("unsupported rating target")

type Rating struct {
	MediaID         int64
	Value           int
	Source          string
	RemoteUpdatedAt *time.Time
	LocalUpdatedAt  time.Time
}

type Review struct {
	MediaID   int64
	Body      string
	UpdatedAt time.Time
}

type Service struct {
	db  *sql.DB
	now func() time.Time
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db, now: time.Now}
}

func (s *Service) SetNow(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

func Stars(value int) (float64, error) {
	if value < 1 || value > 10 {
		return 0, ErrInvalidRating
	}
	return float64(value) / 2, nil
}

func FromStars(stars float64) (int, error) {
	value := int(stars * 2)
	if stars < 0.5 || stars > 5 || float64(value) != stars*2 {
		return 0, ErrInvalidRating
	}
	return value, nil
}

func (s *Service) Get(ctx context.Context, mediaID int64) (*Rating, error) {
	var r Rating
	var remote sql.NullString
	var local string
	err := s.db.QueryRowContext(ctx, `SELECT media_id,rating,source,remote_updated_at,local_updated_at FROM ratings WHERE media_id=?`, mediaID).Scan(&r.MediaID, &r.Value, &r.Source, &remote, &local)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, local)
	if err != nil {
		return nil, err
	}
	r.LocalUpdatedAt = parsed
	if remote.Valid {
		t, err := time.Parse(time.RFC3339Nano, remote.String)
		if err != nil {
			return nil, err
		}
		r.RemoteUpdatedAt = &t
	}
	return &r, nil
}

func (s *Service) SetLocal(ctx context.Context, mediaID int64, value int) error {
	if value < 1 || value > 10 {
		return ErrInvalidRating
	}
	if err := s.validateTarget(ctx, mediaID); err != nil {
		return err
	}
	_, err := workflow.NewWithClock(s.db, s.now).Apply(ctx, workflow.Command{Target: "media", ID: mediaID, Action: "rating", Rating: &value}, "", "")
	return err
}

func (s *Service) DeleteLocal(ctx context.Context, mediaID int64) error {
	if err := s.validateTarget(ctx, mediaID); err != nil {
		return err
	}
	_, err := workflow.NewWithClock(s.db, s.now).Apply(ctx, workflow.Command{Target: "media", ID: mediaID, Action: "delete-rating"}, "", "")
	return err
}

func (s *Service) SetReview(ctx context.Context, mediaID int64, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return errors.New("review body must not be empty")
	}
	if err := s.validateTarget(ctx, mediaID); err != nil {
		return err
	}
	_, err := workflow.NewWithClock(s.db, s.now).Apply(ctx, workflow.Command{Target: "media", ID: mediaID, Action: "review", Review: &body}, "", "")
	return err
}

func (s *Service) GetReview(ctx context.Context, mediaID int64) (*Review, error) {
	var r Review
	var updated string
	err := s.db.QueryRowContext(ctx, `SELECT media_id,body,updated_at FROM reviews WHERE media_id=?`, mediaID).Scan(&r.MediaID, &r.Body, &updated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	return &r, err
}

func (s *Service) DeleteReview(ctx context.Context, mediaID int64) error {
	_, err := workflow.NewWithClock(s.db, s.now).Apply(ctx, workflow.Command{Target: "media", ID: mediaID, Action: "delete-review"}, "", "")
	return err
}

func (s *Service) validateTarget(ctx context.Context, mediaID int64) error {
	var mediaType string
	if err := s.db.QueryRowContext(ctx, `SELECT media_type FROM media_items WHERE id=?`, mediaID).Scan(&mediaType); err != nil {
		if err == sql.ErrNoRows {
			return ErrUnsupportedTarget
		}
		return err
	}
	if mediaType != "movie" && mediaType != "season" && mediaType != "episode" {
		return fmt.Errorf("%w: %s", ErrUnsupportedTarget, mediaType)
	}
	return nil
}
