package prompts

import (
	"context"
	"database/sql"
	"fmt"
)

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

func (s *Service) Apply(ctx context.Context, batch Batch) ([]Decision, error) {
	decisions := Evaluate(batch)
	moviesEnabled, tvEnabled, err := s.preferences(ctx)
	if err != nil {
		return nil, err
	}
	filtered := decisions[:0]
	for _, decision := range decisions {
		if decision.Kind == MovieRating && !moviesEnabled {
			continue
		}
		if decision.Kind != MovieRating && !tvEnabled {
			continue
		}
		filtered = append(filtered, decision)
	}
	decisions = filtered
	if len(decisions) == 0 {
		return decisions, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	created := make([]Decision, 0, len(decisions))
	for _, decision := range decisions {
		inserted, err := insertDecision(ctx, tx, decision)
		if err != nil {
			return nil, err
		}
		if inserted {
			created = append(created, decision)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return created, nil
}

func (s *Service) preferences(ctx context.Context) (bool, bool, error) {
	movies, tv := true, true
	rows, err := s.db.QueryContext(ctx, `SELECT setting_key,setting_value FROM app_settings WHERE setting_key IN ('prompt_movies_enabled','prompt_tv_enabled')`)
	if err != nil {
		return false, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return false, false, err
		}
		enabled := value == "true"
		if key == "prompt_movies_enabled" {
			movies = enabled
		} else {
			tv = enabled
		}
	}
	return movies, tv, rows.Err()
}

func insertDecision(ctx context.Context, tx *sql.Tx, decision Decision) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM prompt_tasks WHERE media_id=? AND task_type='rating' AND state IN ('pending','snoozed')`, decision.MediaID).Scan(&exists)
	if err != nil {
		return false, err
	}
	if exists > 0 {
		return false, nil
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO prompt_tasks(media_id,task_type,state) VALUES(?,'rating','pending')`, decision.MediaID); err != nil {
		return false, fmt.Errorf("create %s prompt for media %d: %w", decision.Kind, decision.MediaID, err)
	}
	return true, nil
}
