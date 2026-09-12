package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

func randomBotID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// RunBotWorker owns durable sync requests and notification production. HTTP
// handlers never spawn untracked work or hold a Discord interaction open.
func (a *API) RunBotWorker(ctx context.Context, notifications bool) error {
	if _, err := a.db.ExecContext(ctx, `UPDATE bot_sync_jobs SET state='pending' WHERE state='running'`); err != nil {
		return err
	}
	if notifications {
		if _, err := a.db.ExecContext(ctx, `INSERT INTO app_settings(setting_key,setting_value) SELECT 'bot_task_baseline',CAST(COALESCE(MAX(id),0) AS TEXT) FROM prompt_tasks WHERE true ON CONFLICT DO NOTHING`); err != nil {
			return err
		}
	}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		if notifications {
			if err := a.produceBotNotifications(ctx); err != nil {
				return err
			}
		}
		var id string
		err := a.db.QueryRowContext(ctx, `SELECT id FROM bot_sync_jobs WHERE state='pending' ORDER BY created_at,id LIMIT 1`).Scan(&id)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil && a.traktSync != nil {
			_, err = a.db.ExecContext(ctx, `UPDATE bot_sync_jobs SET state='running' WHERE id=?`, id)
			if err != nil {
				return err
			}
			syncErr := a.traktSync.SyncNow(ctx)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			state := "completed"
			if syncErr != nil {
				state = "failed"
			}
			// Public results contain no remote error strings or credentials.
			result, _ := json.Marshal(map[string]string{"state": state, "integration": "trakt"})
			if _, err = a.db.ExecContext(ctx, `UPDATE bot_sync_jobs SET state=?,result=? WHERE id=?`, state, string(result), id); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
func (a *API) produceBotNotifications(ctx context.Context) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Only new prompts after activation, plus revisions of already tracked cards.
	_, err = tx.ExecContext(ctx, `INSERT INTO bot_notifications(task_id,revision)
 SELECT t.id,t.revision FROM prompt_tasks t WHERE
 ((t.id>CAST((SELECT setting_value FROM app_settings WHERE setting_key='bot_task_baseline') AS INTEGER)
 AND t.state='pending' AND julianday(t.created_at)>julianday('now','-1 day')
 AND NOT EXISTS(SELECT 1 FROM discord_task_notifications d WHERE d.prompt_task_id=t.id AND d.state='sent'))
 OR EXISTS(SELECT 1 FROM bot_notifications n WHERE n.task_id=t.id AND n.state='sent'))
 ON CONFLICT DO NOTHING`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE bot_notifications SET state='expired' WHERE state IN ('pending','leased') AND (revision<(SELECT revision FROM prompt_tasks WHERE id=task_id) OR julianday(created_at)<julianday('now','-1 day'))`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (a *API) registerBotDelivery(mux *http.ServeMux, notifications bool) {
	mux.HandleFunc("POST /api/bot/v1/sync/trakt", func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		actor := r.Header.Get("X-Discord-User-ID")
		if key == "" || len(key) > 128 {
			badRequest(w, "idempotency key required")
			return
		}
		if a.traktSync == nil {
			conflict(w, "Trakt sync is unavailable")
			return
		}
		// Namespaced by actor, with a stable deterministic identifier for retries.
		id := actor + ":" + key
		_, err := a.db.ExecContext(r.Context(), `INSERT INTO bot_sync_jobs(id,actor,state) VALUES(?,?,'pending') ON CONFLICT DO NOTHING`, id, actor)
		if err != nil {
			internalError(w)
			return
		}
		writeJSON(w, 202, map[string]string{"id": id})
	})
	mux.HandleFunc("GET /api/bot/v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		var state string
		var result sql.NullString
		err := a.db.QueryRowContext(r.Context(), `SELECT state,result FROM bot_sync_jobs WHERE id=? AND actor=?`, r.PathValue("id"), r.Header.Get("X-Discord-User-ID")).Scan(&state, &result)
		if err == sql.ErrNoRows {
			notFound(w)
			return
		}
		if err != nil {
			internalError(w)
			return
		}
		writeJSON(w, 200, map[string]any{"id": r.PathValue("id"), "state": state})
	})
	if !notifications {
		return
	}
	mux.HandleFunc("POST /api/bot/v1/notifications/claim", a.claimBotNotifications)
	mux.HandleFunc("POST /api/bot/v1/notifications/{id}/ack", a.ackBotNotification)
}
func (a *API) claimBotNotifications(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		internalError(w)
		return
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	// Bound catch-up delivery independently of how often the consumer polls.
	var next string
	err = tx.QueryRowContext(ctx, `SELECT setting_value FROM app_settings WHERE setting_key='bot_next_claim'`).Scan(&next)
	if err != nil && err != sql.ErrNoRows {
		internalError(w)
		return
	}
	if next != "" && next > now.Format(time.RFC3339Nano) {
		writeJSON(w, 200, map[string]any{"items": []any{}, "retry_after_seconds": 60})
		return
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO app_settings(setting_key,setting_value) VALUES('bot_next_claim',?) ON CONFLICT(setting_key) DO UPDATE SET setting_value=excluded.setting_value`, now.Add(time.Minute).Format(time.RFC3339Nano))
	if err != nil {
		internalError(w)
		return
	}
	lease := randomBotID()
	_, err = tx.ExecContext(ctx, `UPDATE bot_notifications SET state='pending',lease=NULL WHERE state='leased' AND lease_until<?`, now.Format(time.RFC3339Nano))
	if err != nil {
		internalError(w)
		return
	}
	_, err = tx.ExecContext(ctx, `UPDATE bot_notifications SET state='leased',lease=?,lease_until=? WHERE id IN (SELECT id FROM bot_notifications WHERE state='pending' AND revision=(SELECT revision FROM prompt_tasks WHERE id=task_id) ORDER BY id LIMIT 10)`, lease, now.Add(2*time.Minute).Format(time.RFC3339Nano))
	if err != nil {
		internalError(w)
		return
	}
	rows, err := tx.QueryContext(ctx, `SELECT n.id,n.task_id,n.revision,COALESCE((SELECT message_id FROM bot_notifications old WHERE old.task_id=n.task_id AND old.message_id IS NOT NULL ORDER BY old.id DESC LIMIT 1),'') FROM bot_notifications n WHERE n.lease=? AND n.state='leased'`, lease)
	if err != nil {
		internalError(w)
		return
	}
	items := []map[string]any{}
	for rows.Next() {
		var id, task, revision int64
		var message string
		if err = rows.Scan(&id, &task, &revision, &message); err != nil {
			rows.Close()
			internalError(w)
			return
		}
		items = append(items, map[string]any{"id": id, "task_id": task, "revision": revision, "lease": lease, "message_id": message, "path": "/tasks/" + strconv.FormatInt(task, 10)})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		internalError(w)
		return
	}
	if err = tx.Commit(); err != nil {
		internalError(w)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "lease_seconds": 120, "digest_recommended": len(items) >= 5})
}
func (a *API) ackBotNotification(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Lease     string `json:"lease"`
		MessageID string `json:"message_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Lease == "" || body.MessageID == "" || len(body.MessageID) > 64 {
		badRequest(w, "lease and message_id required")
		return
	}
	result, err := a.db.ExecContext(r.Context(), `UPDATE bot_notifications SET state='sent',message_id=? WHERE id=? AND lease=? AND ((state='leased' AND lease_until>?) OR (state='sent' AND message_id=?))`, body.MessageID, r.PathValue("id"), body.Lease, time.Now().UTC().Format(time.RFC3339Nano), body.MessageID)
	if err != nil {
		internalError(w)
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		conflict(w, "delivery lease expired or changed")
		return
	}
	w.WriteHeader(204)
}
