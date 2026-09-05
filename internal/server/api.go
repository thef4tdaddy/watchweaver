package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/credentials"
	"github.com/thef4tdaddy/watchweaver/internal/discord"
	"github.com/thef4tdaddy/watchweaver/internal/letterboxd"
	"github.com/thef4tdaddy/watchweaver/internal/ratings"
	"github.com/thef4tdaddy/watchweaver/internal/serializd"
	"github.com/thef4tdaddy/watchweaver/internal/trakt"
)

const maxRequestBody = 1 << 20

type API struct {
	db                *sql.DB
	ratings           *ratings.Service
	letterboxd        *letterboxd.Service
	serializd         *serializd.Service
	trakt             *trakt.Service
	credentials       *credentials.Store
	discord           *discord.Notifier
	traktSync         *trakt.SyncManager
	discordConfigured bool
	version           string
	revision          string
	updateURL         string
	updateTagsURL     string
	compareBaseURL    string
	updateClient      *http.Client
	updateCache       updateCache
}

func (a *API) SetDiscordConfigured(configured bool)           { a.discordConfigured = configured }
func (a *API) SetCredentialStore(store *credentials.Store)    { a.credentials = store }
func (a *API) SetDiscordNotifier(notifier *discord.Notifier)  { a.discord = notifier }
func (a *API) SetTraktSyncManager(manager *trakt.SyncManager) { a.traktSync = manager }

func NewAPI(db *sql.DB, traktService *trakt.Service) *API {
	return &API{db: db, ratings: ratings.NewService(db), letterboxd: letterboxd.NewService(db), serializd: serializd.NewService(db), trakt: traktService, version: "dev", updateURL: "https://api.github.com/repos/thef4tdaddy/watchweaver/releases", updateTagsURL: "https://api.github.com/repos/thef4tdaddy/watchweaver/tags", compareBaseURL: "https://github.com/thef4tdaddy/watchweaver/compare/", updateClient: &http.Client{Timeout: 5 * time.Second}}
}

type mediaJSON struct {
	ID            int64             `json:"id"`
	Type          string            `json:"type"`
	Title         string            `json:"title"`
	Year          *int              `json:"year,omitempty"`
	ShowTitle     string            `json:"show_title,omitempty"`
	SeasonNumber  *int              `json:"season_number,omitempty"`
	EpisodeNumber *int              `json:"episode_number,omitempty"`
	SeasonID      *int64            `json:"season_id,omitempty"`
	ExternalIDs   map[string]string `json:"external_ids"`
}

type taskJSON struct {
	ID           int64     `json:"id"`
	Type         string    `json:"type"`
	State        string    `json:"state"`
	SnoozedUntil *string   `json:"snoozed_until,omitempty"`
	CreatedAt    string    `json:"created_at"`
	Media        mediaJSON `json:"media"`
}

type historyJSON struct {
	ID              int64     `json:"id"`
	Source          string    `json:"source"`
	SourceEventID   *string   `json:"source_event_id,omitempty"`
	WatchedAt       string    `json:"watched_at"`
	SourceWatchedAt string    `json:"source_watched_at"`
	Media           mediaJSON `json:"media"`
}

type pageJSON struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
	Items      any `json:"items"`
}

func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) { notFound(w) })
	mux.HandleFunc("/api/inbox", a.inbox)
	mux.HandleFunc("/api/history", a.history)
	mux.HandleFunc("/api/tasks/", a.taskAction)
	mux.HandleFunc("/api/media/", a.mediaResource)
	mux.HandleFunc("/api/settings", a.settings)
	mux.HandleFunc("/api/integrations", a.integrationStatus)
	mux.HandleFunc("/api/status", a.operationalStatus)
	mux.HandleFunc("/api/update", a.update)
	mux.HandleFunc("/api/diagnostics", a.diagnostics)
	mux.HandleFunc("/api/setup", a.setupStatus)
	mux.HandleFunc("/api/integrations/trakt/config", a.traktConfig)
	mux.HandleFunc("/api/integrations/trakt/authorize", a.traktAuthorize)
	mux.HandleFunc("/api/integrations/trakt/authorize/poll", a.traktPoll)
	mux.HandleFunc("/api/integrations/trakt/sync", a.traktSyncNow)
	mux.HandleFunc("/api/integrations/discord/config", a.discordConfig)
	mux.HandleFunc("/api/integrations/discord/test", a.discordTest)
	mux.HandleFunc("/api/letterboxd", a.letterboxdStatus)
	mux.HandleFunc("/api/letterboxd/batches", a.letterboxdGenerate)
	mux.HandleFunc("/api/letterboxd/batches/", a.letterboxdBatch)
	mux.HandleFunc("/api/serializd", a.serializdStatus)
	mux.HandleFunc("/api/serializd/mark-synced", a.serializdMarkSynced)
	mux.HandleFunc("/api/serializd/reviews", a.serializdReviews)
	mux.HandleFunc("/api/serializd/reviews/", a.serializdReviewTransfer)
}

func (a *API) serializdOptions(ctx context.Context) (serializd.Options, error) {
	settings, err := a.loadSettings(ctx)
	if err != nil {
		return serializd.Options{}, err
	}
	return serializd.Options{Enabled: settings.SerializdEnabled, ReminderChanges: settings.SerializdReminderChanges, ReminderDays: settings.SerializdReminderDays}, nil
}

func (a *API) serializdStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	options, err := a.serializdOptions(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	status, err := a.serializd.Status(r.Context(), options)
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *API) serializdMarkSynced(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	options, err := a.serializdOptions(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	status, err := a.serializd.MarkSynced(r.Context(), options)
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *API) serializdReviews(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	items, err := a.serializd.Reviews(r.Context(), r.URL.Query().Get("include_transferred") == "true")
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *API) serializdReviewTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/serializd/reviews/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[1] != "transferred" {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id < 1 {
		badRequest(w, "invalid review id")
		return
	}
	var body struct {
		Transferred bool `json:"transferred"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := a.serializd.SetReviewTransferred(r.Context(), id, body.Transferred); errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		internalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) letterboxdStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	settings, err := a.loadSettings(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	status, err := a.letterboxd.Status(r.Context(), settings.Timezone)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *API) letterboxdGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		batches, err := a.letterboxd.ListBatches(r.Context())
		if err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": batches})
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	settings, err := a.loadSettings(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	batch, err := a.letterboxd.Generate(r.Context(), settings.Timezone)
	if errors.Is(err, letterboxd.ErrNothingPending) {
		conflict(w, err.Error())
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, batch)
}

func (a *API) letterboxdBatch(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, "/api/letterboxd/batches/")
	if len(parts) < 1 {
		notFound(w)
		return
	}
	id, ok := positiveID(w, parts[0])
	if !ok {
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		batch, err := a.letterboxd.GetBatch(r.Context(), id)
		if errors.Is(err, letterboxd.ErrBatchNotFound) {
			notFound(w)
			return
		}
		if err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, batch)
		return
	}
	if len(parts) == 2 && parts[1] == "confirm" && r.Method == http.MethodPost {
		batch, err := a.letterboxd.Confirm(r.Context(), id)
		if errors.Is(err, letterboxd.ErrBatchNotFound) {
			notFound(w)
			return
		}
		if errors.Is(err, letterboxd.ErrBatchConfirmed) {
			conflict(w, err.Error())
			return
		}
		if err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, batch)
		return
	}
	if len(parts) == 3 && parts[1] == "files" && r.Method == http.MethodGet {
		part, err := strconv.Atoi(parts[2])
		if err != nil || part < 1 {
			badRequest(w, "part must be a positive integer")
			return
		}
		file, err := a.letterboxd.GetFile(r.Context(), id, part)
		if errors.Is(err, letterboxd.ErrBatchNotFound) {
			notFound(w)
			return
		}
		if err != nil {
			internalError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file.Filename))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(file.Content)
		return
	}
	if (len(parts) == 1 || len(parts) == 2 || len(parts) == 3) && r.Method != http.MethodGet && r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	notFound(w)
}

func (a *API) inbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	page, perPage, ok := pagination(w, r)
	if !ok {
		return
	}
	var total int
	if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM prompt_tasks WHERE state IN ('pending','snoozed')`).Scan(&total); err != nil {
		internalError(w)
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT t.id,t.task_type,t.state,t.snoozed_until,t.created_at,m.id,m.media_type,m.title,m.year,CASE WHEN m.media_type='episode' THEN p.season_number ELSE m.season_number END,m.episode_number,
		CASE WHEN m.media_type='season' THEN p.title WHEN m.media_type='episode' THEN gp.title ELSE '' END
		FROM prompt_tasks t JOIN media_items m ON m.id=t.media_id
		LEFT JOIN media_items p ON p.id=m.parent_id LEFT JOIN media_items gp ON gp.id=p.parent_id
		WHERE t.state IN ('pending','snoozed') ORDER BY t.created_at ASC,t.id ASC LIMIT ? OFFSET ?`, perPage, (page-1)*perPage)
	if err != nil {
		internalError(w)
		return
	}
	defer rows.Close()
	items := make([]taskJSON, 0)
	for rows.Next() {
		var item taskJSON
		var snooze, year sql.NullString
		var season, episode sql.NullInt64
		if err := rows.Scan(&item.ID, &item.Type, &item.State, &snooze, &item.CreatedAt, &item.Media.ID, &item.Media.Type, &item.Media.Title, &year, &season, &episode, &item.Media.ShowTitle); err != nil {
			internalError(w)
			return
		}
		setOptionalMediaFields(&item.Media, year, season, episode)
		if snooze.Valid {
			item.SnoozedUntil = &snooze.String
		}
		item.Media.ExternalIDs = a.externalIDs(r.Context(), item.Media.ID)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, newPage(page, perPage, total, items))
}

func (a *API) history(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	page, perPage, ok := pagination(w, r)
	if !ok {
		return
	}
	var total int
	if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM watch_events WHERE deleted_at IS NULL`).Scan(&total); err != nil {
		internalError(w)
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT w.id,w.source,w.source_event_id,w.watched_at_utc,w.source_watched_at,m.id,m.media_type,m.title,m.year,CASE WHEN m.media_type='episode' THEN p.season_number ELSE m.season_number END,m.episode_number,CASE WHEN m.media_type='episode' THEN p.id END,
		CASE WHEN m.media_type='season' THEN p.title WHEN m.media_type='episode' THEN gp.title ELSE '' END
		FROM watch_events w JOIN media_items m ON m.id=w.media_id LEFT JOIN media_items p ON p.id=m.parent_id LEFT JOIN media_items gp ON gp.id=p.parent_id
		WHERE w.deleted_at IS NULL ORDER BY w.watched_at_utc DESC,w.id DESC LIMIT ? OFFSET ?`, perPage, (page-1)*perPage)
	if err != nil {
		internalError(w)
		return
	}
	defer rows.Close()
	items := make([]historyJSON, 0)
	for rows.Next() {
		var item historyJSON
		var sourceID, year sql.NullString
		var season, episode sql.NullInt64
		var seasonID sql.NullInt64
		if err := rows.Scan(&item.ID, &item.Source, &sourceID, &item.WatchedAt, &item.SourceWatchedAt, &item.Media.ID, &item.Media.Type, &item.Media.Title, &year, &season, &episode, &seasonID, &item.Media.ShowTitle); err != nil {
			internalError(w)
			return
		}
		if sourceID.Valid {
			item.SourceEventID = &sourceID.String
		}
		setOptionalMediaFields(&item.Media, year, season, episode)
		if seasonID.Valid {
			item.Media.SeasonID = &seasonID.Int64
		}
		item.Media.ExternalIDs = a.externalIDs(r.Context(), item.Media.ID)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, newPage(page, perPage, total, items))
}

func (a *API) taskAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	parts := pathParts(r.URL.Path, "/api/tasks/")
	if len(parts) != 2 {
		notFound(w)
		return
	}
	id, ok := positiveID(w, parts[0])
	if !ok {
		return
	}
	switch parts[1] {
	case "complete":
		a.completeTask(w, r, id)
	case "skip":
		a.transitionTask(w, r, id, "skipped", nil)
	case "snooze":
		var body struct {
			Until string `json:"until"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		until, err := time.Parse(time.RFC3339, body.Until)
		if err != nil || !until.After(time.Now()) {
			badRequest(w, "until must be a future RFC3339 timestamp")
			return
		}
		normalized := until.UTC().Format(time.RFC3339)
		a.transitionTask(w, r, id, "snoozed", &normalized)
	default:
		notFound(w)
	}
}

func (a *API) completeTask(w http.ResponseWriter, r *http.Request, id int64) {
	var body struct {
		Rating *int    `json:"rating"`
		Review *string `json:"review"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Rating == nil && body.Review == nil {
		badRequest(w, "rating or review is required")
		return
	}
	if body.Rating != nil && (*body.Rating < 1 || *body.Rating > 10) {
		badRequest(w, ratings.ErrInvalidRating.Error())
		return
	}
	if body.Review != nil && strings.TrimSpace(*body.Review) == "" {
		badRequest(w, "review must not be empty")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		internalError(w)
		return
	}
	defer tx.Rollback()
	var mediaID int64
	var state string
	if err = tx.QueryRowContext(r.Context(), `SELECT media_id,state FROM prompt_tasks WHERE id=?`, id).Scan(&mediaID, &state); err == sql.ErrNoRows {
		notFound(w)
		return
	} else if err != nil {
		internalError(w)
		return
	}
	if state != "pending" && state != "snoozed" {
		conflict(w, "task is already resolved")
		return
	}
	var mediaType string
	if err = tx.QueryRowContext(r.Context(), `SELECT media_type FROM media_items WHERE id=?`, mediaID).Scan(&mediaType); err != nil {
		internalError(w)
		return
	}
	if mediaType != "movie" && mediaType != "season" && mediaType != "episode" {
		badRequest(w, "unsupported media target")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if body.Rating != nil {
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO ratings(media_id,rating,source,local_updated_at) VALUES(?,?,'local',?) ON CONFLICT(media_id) DO UPDATE SET rating=excluded.rating,source='local',local_updated_at=excluded.local_updated_at`, mediaID, *body.Rating, now); err != nil {
			internalError(w)
			return
		}
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO rating_sync_state(media_id,last_local_change_at,pending_rating,pending_delete,attempt_count,next_attempt_at,last_error) VALUES(?,?,?,0,0,?,NULL) ON CONFLICT(media_id) DO UPDATE SET last_local_change_at=excluded.last_local_change_at,pending_rating=excluded.pending_rating,pending_delete=0,attempt_count=0,next_attempt_at=excluded.next_attempt_at,last_error=NULL,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`, mediaID, now, *body.Rating, now); err != nil {
			internalError(w)
			return
		}
	}
	if body.Review != nil {
		review := strings.TrimSpace(*body.Review)
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO reviews(media_id,body,source,created_at,updated_at) VALUES(?,?,'local',?,?) ON CONFLICT(media_id) DO UPDATE SET body=excluded.body,source='local',updated_at=excluded.updated_at`, mediaID, review, now, now); err != nil {
			internalError(w)
			return
		}
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE prompt_tasks SET state='completed',snoozed_until=NULL,updated_at=? WHERE id=?`, now, id); err != nil {
		internalError(w)
		return
	}
	if err = tx.Commit(); err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "state": "completed", "media_id": mediaID})
}

func (a *API) transitionTask(w http.ResponseWriter, r *http.Request, id int64, state string, snooze *string) {
	res, err := a.db.ExecContext(r.Context(), `UPDATE prompt_tasks SET state=?,snoozed_until=?,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND state IN ('pending','snoozed')`, state, snooze, id)
	if err != nil {
		internalError(w)
		return
	}
	changed, _ := res.RowsAffected()
	if changed == 0 {
		var exists int
		if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM prompt_tasks WHERE id=?`, id).Scan(&exists); err != nil {
			internalError(w)
			return
		}
		if exists == 0 {
			notFound(w)
		} else {
			conflict(w, "task is already resolved")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "state": state, "snoozed_until": snooze})
}

func (a *API) mediaResource(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, "/api/media/")
	if len(parts) != 2 {
		notFound(w)
		return
	}
	id, ok := positiveID(w, parts[0])
	if !ok {
		return
	}
	mediaType, err := a.mediaType(r.Context(), id)
	if err == sql.ErrNoRows {
		notFound(w)
		return
	}
	if err != nil {
		internalError(w)
		return
	}
	if mediaType != "movie" && mediaType != "season" && mediaType != "episode" {
		badRequest(w, "unsupported media target")
		return
	}
	switch parts[1] {
	case "rating":
		a.rating(w, r, id)
	case "review":
		a.review(w, r, id)
	default:
		notFound(w)
	}
}

func (a *API) rating(w http.ResponseWriter, r *http.Request, id int64) {
	switch r.Method {
	case http.MethodGet:
		value, err := a.ratings.Get(r.Context(), id)
		if err != nil {
			internalError(w)
			return
		}
		if value == nil {
			notFound(w)
			return
		}
		stars, _ := ratings.Stars(value.Value)
		writeJSON(w, http.StatusOK, map[string]any{"media_id": id, "rating": value.Value, "stars": stars, "source": value.Source, "local_updated_at": value.LocalUpdatedAt, "remote_updated_at": value.RemoteUpdatedAt})
	case http.MethodPut:
		var body struct {
			Rating *int     `json:"rating"`
			Stars  *float64 `json:"stars"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if (body.Rating == nil) == (body.Stars == nil) {
			badRequest(w, "provide exactly one of rating or stars")
			return
		}
		value := 0
		var err error
		if body.Rating != nil {
			value = *body.Rating
		} else {
			value, err = ratings.FromStars(*body.Stars)
		}
		if err == nil {
			err = a.ratings.SetLocal(r.Context(), id, value)
		}
		if errors.Is(err, ratings.ErrInvalidRating) {
			badRequest(w, ratings.ErrInvalidRating.Error())
			return
		}
		if errors.Is(err, ratings.ErrUnsupportedTarget) {
			badRequest(w, "unsupported media target")
			return
		}
		if err != nil {
			internalError(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"media_id": id, "rating": value, "stars": float64(value) / 2})
	case http.MethodDelete:
		if err := a.ratings.DeleteLocal(r.Context(), id); errors.Is(err, ratings.ErrUnsupportedTarget) {
			badRequest(w, "unsupported media target")
			return
		} else if err != nil {
			internalError(w)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}

func (a *API) review(w http.ResponseWriter, r *http.Request, id int64) {
	switch r.Method {
	case http.MethodGet:
		value, err := a.ratings.GetReview(r.Context(), id)
		if err != nil {
			internalError(w)
			return
		}
		if value == nil {
			notFound(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"media_id": id, "body": value.Body, "updated_at": value.UpdatedAt})
	case http.MethodPut:
		var body struct {
			Body string `json:"body"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if err := a.ratings.SetReview(r.Context(), id, body.Body); errors.Is(err, ratings.ErrUnsupportedTarget) || err != nil && strings.Contains(err.Error(), "must not be empty") {
			badRequest(w, err.Error())
			return
		} else if err != nil {
			internalError(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"media_id": id, "body": strings.TrimSpace(body.Body)})
	case http.MethodDelete:
		if err := a.ratings.DeleteReview(r.Context(), id); err != nil {
			internalError(w)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}

type settingsJSON struct {
	Timezone                 string `json:"timezone"`
	TraktPollMinutes         int    `json:"trakt_poll_minutes"`
	PromptMoviesEnabled      bool   `json:"prompt_movies_enabled"`
	PromptTVEnabled          bool   `json:"prompt_tv_enabled"`
	SerializdEnabled         bool   `json:"serializd_enabled"`
	SerializdReminderChanges int    `json:"serializd_reminder_changes"`
	SerializdReminderDays    int    `json:"serializd_reminder_days"`
	UpdateChecksEnabled      bool   `json:"update_checks_enabled"`
}

type settingsUpdateJSON struct {
	Timezone                 *string `json:"timezone"`
	TraktPollMinutes         *int    `json:"trakt_poll_minutes"`
	PromptMoviesEnabled      *bool   `json:"prompt_movies_enabled"`
	PromptTVEnabled          *bool   `json:"prompt_tv_enabled"`
	SerializdEnabled         *bool   `json:"serializd_enabled"`
	SerializdReminderChanges *int    `json:"serializd_reminder_changes"`
	SerializdReminderDays    *int    `json:"serializd_reminder_days"`
	UpdateChecksEnabled      *bool   `json:"update_checks_enabled"`
}

func (a *API) settings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := a.loadSettings(r.Context())
		if err != nil {
			internalError(w)
			return
		}
		writeJSON(w, http.StatusOK, settings)
	case http.MethodPut:
		var update settingsUpdateJSON
		if !decodeJSON(w, r, &update) {
			return
		}
		body, err := a.loadSettings(r.Context())
		if err != nil {
			internalError(w)
			return
		}
		if update.Timezone != nil {
			body.Timezone = *update.Timezone
		}
		if update.TraktPollMinutes != nil {
			body.TraktPollMinutes = *update.TraktPollMinutes
		}
		if update.PromptMoviesEnabled != nil {
			body.PromptMoviesEnabled = *update.PromptMoviesEnabled
		}
		if update.PromptTVEnabled != nil {
			body.PromptTVEnabled = *update.PromptTVEnabled
		}
		if update.SerializdEnabled != nil {
			body.SerializdEnabled = *update.SerializdEnabled
		}
		if update.SerializdReminderChanges != nil {
			body.SerializdReminderChanges = *update.SerializdReminderChanges
		}
		if update.SerializdReminderDays != nil {
			body.SerializdReminderDays = *update.SerializdReminderDays
		}
		if update.UpdateChecksEnabled != nil {
			body.UpdateChecksEnabled = *update.UpdateChecksEnabled
		}
		if _, err := time.LoadLocation(body.Timezone); err != nil {
			badRequest(w, "timezone must be a valid IANA timezone")
			return
		}
		if body.SerializdReminderChanges < 1 || body.SerializdReminderDays < 1 {
			badRequest(w, "reminder thresholds must be positive")
			return
		}
		if body.TraktPollMinutes < 1 || body.TraktPollMinutes > 1440 {
			badRequest(w, "Trakt polling must be between 1 and 1440 minutes")
			return
		}
		tx, err := a.db.BeginTx(r.Context(), nil)
		if err != nil {
			internalError(w)
			return
		}
		defer tx.Rollback()
		values := map[string]string{"timezone": body.Timezone, "trakt_poll_minutes": strconv.Itoa(body.TraktPollMinutes), "prompt_movies_enabled": strconv.FormatBool(body.PromptMoviesEnabled), "prompt_tv_enabled": strconv.FormatBool(body.PromptTVEnabled), "serializd_enabled": strconv.FormatBool(body.SerializdEnabled), "serializd_reminder_changes": strconv.Itoa(body.SerializdReminderChanges), "serializd_reminder_days": strconv.Itoa(body.SerializdReminderDays), "update_checks_enabled": strconv.FormatBool(body.UpdateChecksEnabled)}
		for key, value := range values {
			if _, err = tx.ExecContext(r.Context(), `INSERT INTO app_settings(setting_key,setting_value) VALUES(?,?) ON CONFLICT(setting_key) DO UPDATE SET setting_value=excluded.setting_value,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`, key, value); err != nil {
				internalError(w)
				return
			}
		}
		if err = tx.Commit(); err != nil {
			internalError(w)
			return
		}
		writeJSON(w, http.StatusOK, body)
	default:
		methodNotAllowed(w)
	}
}
func (a *API) loadSettings(ctx context.Context) (settingsJSON, error) {
	out := settingsJSON{Timezone: "UTC", TraktPollMinutes: 5, PromptMoviesEnabled: true, PromptTVEnabled: true, SerializdReminderChanges: 20, SerializdReminderDays: 14, UpdateChecksEnabled: true}
	rows, err := a.db.QueryContext(ctx, `SELECT setting_key,setting_value FROM app_settings WHERE setting_key IN ('timezone','trakt_poll_minutes','prompt_movies_enabled','prompt_tv_enabled','serializd_enabled','serializd_reminder_changes','serializd_reminder_days','update_checks_enabled')`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return out, err
		}
		switch k {
		case "timezone":
			out.Timezone = v
		case "trakt_poll_minutes":
			out.TraktPollMinutes, _ = strconv.Atoi(v)
		case "prompt_movies_enabled":
			out.PromptMoviesEnabled, _ = strconv.ParseBool(v)
		case "prompt_tv_enabled":
			out.PromptTVEnabled, _ = strconv.ParseBool(v)
		case "serializd_enabled":
			out.SerializdEnabled, _ = strconv.ParseBool(v)
		case "serializd_reminder_changes":
			out.SerializdReminderChanges, _ = strconv.Atoi(v)
		case "serializd_reminder_days":
			out.SerializdReminderDays, _ = strconv.Atoi(v)
		case "update_checks_enabled":
			out.UpdateChecksEnabled, _ = strconv.ParseBool(v)
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if _, err := time.LoadLocation(out.Timezone); err != nil {
		out.Timezone = "UTC"
	}
	return out, nil
}

func (a *API) integrationStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	settings, err := a.loadSettings(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	traktStatus := trakt.PublicStatus{Status: trakt.StatusNotConfigured}
	if a.trakt != nil {
		traktStatus = a.trakt.Status(r.Context())
	}
	pollStatus, err := trakt.NewPoller(a.db, nil, trakt.PollerOptions{}).Status(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	discordConfigured := a.discordConfigured
	if a.discord != nil {
		discordConfigured = a.discord.Configured()
	}
	var syncStatus trakt.SyncStatus
	if a.traktSync != nil {
		syncStatus, err = a.traktSync.Status(r.Context())
		if err != nil {
			internalError(w)
			return
		}
		syncStatus.CanSync = traktStatus.Status == trakt.StatusConnected
	}
	jellyfinConfigured := false
	if a.credentials != nil {
		jellyfinConfigured, _ = a.credentials.Configured(r.Context(), "jellyfin", jellyfinTokenKey)
	}
	jellyfinStatus, err := a.jellyfinService().Status(r.Context(), jellyfinConfigured)
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trakt": map[string]any{"authorization": traktStatus, "poll": pollStatus, "sync": syncStatus}, "jellyfin": jellyfinStatus, "serializd": map[string]any{"enabled": settings.SerializdEnabled, "status": map[bool]string{true: "enabled", false: "disabled"}[settings.SerializdEnabled]}, "letterboxd": map[string]any{"enabled": true, "status": "available"}, "discord": map[string]any{"enabled": discordConfigured, "status": map[bool]string{true: "configured", false: "disabled"}[discordConfigured]}})
}

func (a *API) traktSyncNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if a.traktSync == nil {
		writeError(w, http.StatusServiceUnavailable, "Trakt sync is unavailable")
		return
	}
	if a.trakt == nil || a.trakt.Status(r.Context()).Status != trakt.StatusConnected {
		conflict(w, "Trakt authorization is required")
		return
	}
	if err := a.traktSync.SyncNow(r.Context()); err != nil {
		if errors.Is(err, trakt.ErrSyncInProgress) {
			conflict(w, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	status, err := a.traktSync.Status(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
func (a *API) traktAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if a.trakt == nil {
		writeError(w, http.StatusServiceUnavailable, "trakt is unavailable")
		return
	}
	status, err := a.trakt.Start(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, status)
}
func (a *API) traktPoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if a.trakt == nil {
		writeError(w, http.StatusServiceUnavailable, "trakt is unavailable")
		return
	}
	status, err := a.trakt.Poll(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if status.Status == trakt.StatusConnected && a.traktSync != nil {
		a.traktSync.Trigger()
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *API) mediaType(ctx context.Context, id int64) (string, error) {
	var mediaType string
	err := a.db.QueryRowContext(ctx, `SELECT media_type FROM media_items WHERE id=?`, id).Scan(&mediaType)
	return mediaType, err
}
func (a *API) externalIDs(ctx context.Context, id int64) map[string]string {
	out := map[string]string{}
	rows, err := a.db.QueryContext(ctx, `SELECT provider,external_id FROM external_ids WHERE media_id=? ORDER BY provider`, id)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			out[k] = v
		}
	}
	return out
}
func setOptionalMediaFields(m *mediaJSON, year sql.NullString, season, episode sql.NullInt64) {
	if year.Valid {
		v, _ := strconv.Atoi(year.String)
		m.Year = &v
	}
	if season.Valid {
		v := int(season.Int64)
		m.SeasonNumber = &v
	}
	if episode.Valid {
		v := int(episode.Int64)
		m.EpisodeNumber = &v
	}
}
func newPage(page, perPage, total int, items any) pageJSON {
	pages := 0
	if total > 0 {
		pages = (total + perPage - 1) / perPage
	}
	return pageJSON{Page: page, PerPage: perPage, Total: total, TotalPages: pages, Items: items}
}
func pagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	page, perPage := 1, 50
	var err error
	if raw := r.URL.Query().Get("page"); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 {
			badRequest(w, "page must be a positive integer")
			return 0, 0, false
		}
	}
	if raw := r.URL.Query().Get("per_page"); raw != "" {
		perPage, err = strconv.Atoi(raw)
		if err != nil || perPage < 1 || perPage > 100 {
			badRequest(w, "per_page must be between 1 and 100")
			return 0, 0, false
		}
	}
	return page, perPage, true
}
func pathParts(value, prefix string) []string {
	rest := strings.Trim(strings.TrimPrefix(value, prefix), "/")
	if rest == "" {
		return nil
	}
	return strings.Split(rest, "/")
}
func positiveID(w http.ResponseWriter, raw string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		badRequest(w, "id must be a positive integer")
		return 0, false
	}
	return id, true
}
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
			return false
		}
		badRequest(w, "invalid JSON body")
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		badRequest(w, "request body must contain one JSON object")
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func badRequest(w http.ResponseWriter, message string) { writeError(w, http.StatusBadRequest, message) }
func conflict(w http.ResponseWriter, message string)   { writeError(w, http.StatusConflict, message) }
func notFound(w http.ResponseWriter)                   { writeError(w, http.StatusNotFound, "not found") }
func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
func internalError(w http.ResponseWriter, causes ...error) {
	for _, err := range causes {
		if err != nil {
			log.Printf("internal API error: %v", err)
		}
	}
	writeError(w, http.StatusInternalServerError, "internal server error")
}
