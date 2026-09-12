// Package workflow owns transactional changes shared by the web UI and bot.
package workflow

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrConflict = errors.New("item changed; reload before trying again")
var ErrInvalid = errors.New("invalid workflow action")
var ErrNotFound = errors.New("item not found")

type Command struct {
	Target        string  `json:"target"`
	ID            int64   `json:"id"`
	Action        string  `json:"action"`
	Rating        *int    `json:"rating,omitempty"`
	Review        *string `json:"review,omitempty"`
	Until         *string `json:"until,omitempty"`
	Revision      *int64  `json:"revision,omitempty"`
	MediaRevision *int64  `json:"media_revision,omitempty"`
}
type Result struct {
	ID      int64  `json:"id"`
	MediaID int64  `json:"media_id"`
	State   string `json:"state,omitempty"`
}
type Service struct {
	db  *sql.DB
	now func() time.Time
}

func New(db *sql.DB) *Service                                { return &Service{db, time.Now} }
func NewWithClock(db *sql.DB, now func() time.Time) *Service { return &Service{db, now} }
func (s *Service) Apply(ctx context.Context, c Command, actor, key string) (Result, error) {
	var out Result
	if c.Target == "media" && ((c.Rating != nil && c.Action != "rating") || (c.Review != nil && c.Action != "review") || c.Until != nil) {
		return out, ErrInvalid
	}
	if c.Target == "task" && c.Action != "snooze" && c.Until != nil {
		return out, ErrInvalid
	}
	raw, _ := json.Marshal(c)
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(raw))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if key != "" {
		// Acquire the writer before reading state; prevents upgrade races with web writes.
		_, err = tx.ExecContext(ctx, `INSERT INTO bot_requests(actor,request_id,fingerprint,result) VALUES(?,?,?,'') ON CONFLICT DO NOTHING`, actor, key, fingerprint)
		if err != nil {
			return out, err
		}
		var old, result string
		if err = tx.QueryRowContext(ctx, `SELECT fingerprint,result FROM bot_requests WHERE actor=? AND request_id=?`, actor, key).Scan(&old, &result); err != nil {
			return out, err
		}
		if old != fingerprint {
			return out, ErrConflict
		}
		if result != "" {
			err = json.Unmarshal([]byte(result), &out)
			return out, err
		}
	}
	mediaID := c.ID
	state := ""
	var revision int64
	if c.Target == "task" {
		err = tx.QueryRowContext(ctx, `SELECT media_id,state,revision FROM prompt_tasks WHERE id=?`, c.ID).Scan(&mediaID, &state, &revision)
	} else if c.Target == "media" {
		err = tx.QueryRowContext(ctx, `SELECT revision FROM media_items WHERE id=?`, c.ID).Scan(&revision)
	} else {
		return out, ErrInvalid
	}
	if err == sql.ErrNoRows {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if c.Revision != nil && *c.Revision != revision {
		return out, ErrConflict
	}
	var kind string
	var mediaRevision int64
	if err = tx.QueryRowContext(ctx, `SELECT media_type,revision FROM media_items WHERE id=?`, mediaID).Scan(&kind, &mediaRevision); err != nil {
		return out, err
	}
	if c.MediaRevision != nil && *c.MediaRevision != mediaRevision {
		return out, ErrConflict
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	if c.Target == "task" {
		if state != "pending" && state != "snoozed" {
			return out, ErrConflict
		}
		switch c.Action {
		case "complete":
			if c.Rating == nil && c.Review == nil {
				return out, ErrInvalid
			}
			state = "completed"
		case "skip":
			state = "skipped"
		case "snooze":
			if c.Until == nil {
				return out, ErrInvalid
			}
			until, e := time.Parse(time.RFC3339, *c.Until)
			if e != nil || !until.After(time.Now()) {
				return out, ErrInvalid
			}
			normalized := until.UTC().Format(time.RFC3339Nano)
			c.Until = &normalized
			state = "snoozed"
		default:
			return out, ErrInvalid
		}
		if c.Action != "complete" && (c.Rating != nil || c.Review != nil) {
			return out, ErrInvalid
		}
	} else {
		switch c.Action {
		case "rating":
			if c.Rating == nil {
				return out, ErrInvalid
			}
		case "review":
			if c.Review == nil {
				return out, ErrInvalid
			}
		case "delete-rating", "delete-review":
		case "ignore", "unignore":
			if kind != "movie" && kind != "show" {
				return out, ErrInvalid
			}
		default:
			return out, ErrInvalid
		}
	}
	if c.Rating != nil || c.Review != nil || c.Action == "delete-rating" || c.Action == "delete-review" {
		if kind != "movie" && kind != "season" && kind != "episode" {
			return out, ErrInvalid
		}
	}
	if c.Rating != nil || c.Action == "delete-rating" {
		if c.Rating != nil {
			if *c.Rating < 1 || *c.Rating > 10 {
				return out, ErrInvalid
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO ratings(media_id,rating,source,local_updated_at) VALUES(?,?,'local',?) ON CONFLICT(media_id) DO UPDATE SET rating=excluded.rating,source='local',local_updated_at=excluded.local_updated_at`, mediaID, *c.Rating, now)
		} else {
			_, err = tx.ExecContext(ctx, `DELETE FROM ratings WHERE media_id=?`, mediaID)
		}
		if err != nil {
			return out, err
		}
		deleted := 0
		if c.Rating == nil {
			deleted = 1
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO rating_sync_state(media_id,last_local_change_at,pending_rating,pending_delete,attempt_count,next_attempt_at,last_error) VALUES(?,?,?,?,0,?,NULL) ON CONFLICT(media_id) DO UPDATE SET last_local_change_at=excluded.last_local_change_at,pending_rating=excluded.pending_rating,pending_delete=excluded.pending_delete,attempt_count=0,next_attempt_at=excluded.next_attempt_at,last_error=NULL`, mediaID, now, c.Rating, deleted, now)
		if err != nil {
			return out, err
		}
	}
	if c.Review != nil {
		body := strings.TrimSpace(*c.Review)
		if body == "" || len(body) > 100000 {
			return out, ErrInvalid
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO reviews(media_id,body,source,created_at,updated_at) VALUES(?,?,'local',?,?) ON CONFLICT(media_id) DO UPDATE SET body=excluded.body,source='local',updated_at=excluded.updated_at`, mediaID, body, now, now)
		if err != nil {
			return out, err
		}
	}
	if c.Action == "delete-review" {
		if _, err = tx.ExecContext(ctx, `DELETE FROM reviews WHERE media_id=?`, mediaID); err != nil {
			return out, err
		}
	}
	if c.Action == "ignore" {
		_, err = tx.ExecContext(ctx, `INSERT INTO prompt_ignored_media(media_id) VALUES(?) ON CONFLICT DO NOTHING`, mediaID)
	}
	if c.Action == "unignore" {
		_, err = tx.ExecContext(ctx, `DELETE FROM prompt_ignored_media WHERE media_id=?`, mediaID)
	}
	if err != nil {
		return out, err
	}
	if c.Action == "ignore" || c.Action == "unignore" {
		_, err = tx.ExecContext(ctx, `UPDATE media_items SET revision=revision+1 WHERE id=?`, mediaID)
		if err != nil {
			return out, err
		}
	}
	if c.Action == "ignore" {
		_, err = tx.ExecContext(ctx, `UPDATE prompt_tasks SET state='ignored',snoozed_until=NULL WHERE state IN ('pending','snoozed') AND media_id IN (SELECT id FROM media_items WHERE id=? OR parent_id=? OR parent_id IN (SELECT id FROM media_items WHERE parent_id=?))`, mediaID, mediaID, mediaID)
		if err != nil {
			return out, err
		}
	}
	if c.Target == "task" {
		var snooze *string
		if state == "snoozed" {
			snooze = c.Until
		}
		_, err = tx.ExecContext(ctx, `UPDATE prompt_tasks SET state=?,snoozed_until=?,updated_at=? WHERE id=?`, state, snooze, now, c.ID)
		if err != nil {
			return out, err
		}
	}
	out = Result{c.ID, mediaID, state}
	if key != "" {
		result, _ := json.Marshal(out)
		_, err = tx.ExecContext(ctx, `UPDATE bot_requests SET result=? WHERE actor=? AND request_id=?`, string(result), actor, key)
		if err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}
