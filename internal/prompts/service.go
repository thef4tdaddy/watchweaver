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
	if len(decisions) == 0 {
		return decisions, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	for _, decision := range decisions {
		if err := insertDecision(ctx, tx, decision); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return decisions, nil
}

func insertDecision(ctx context.Context, tx *sql.Tx, decision Decision) error {
	var exists int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM prompt_tasks WHERE media_id=? AND task_type='rating' AND state IN ('pending','snoozed')`, decision.MediaID).Scan(&exists)
	if err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO prompt_tasks(media_id,task_type,state) VALUES(?,'rating','pending')`, decision.MediaID); err != nil {
		return fmt.Errorf("create %s prompt for media %d: %w", decision.Kind, decision.MediaID, err)
	}
	return nil
}
