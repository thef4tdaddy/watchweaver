package trakt

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
	"time"
)

const maxRatingResponseBytes = 2 << 20

type RatingSync struct {
	db          *sql.DB
	baseURL     string
	httpClient  *http.Client
	clientID    string
	accessToken string
	now         func() time.Time
}

type remoteRating struct {
	RatedAt string         `json:"rated_at"`
	Rating  int            `json:"rating"`
	Type    string         `json:"type"`
	Movie   *ratingObject  `json:"movie"`
	Show    *ratingObject  `json:"show"`
	Season  *seasonObject  `json:"season"`
	Episode *episodeObject `json:"episode"`
}

type ratingObject struct {
	Title string         `json:"title"`
	Year  int            `json:"year"`
	IDs   map[string]any `json:"ids"`
}

type seasonObject struct {
	Number int            `json:"number"`
	IDs    map[string]any `json:"ids"`
}

type episodeObject struct {
	Season int            `json:"season"`
	Number int            `json:"number"`
	Title  string         `json:"title"`
	IDs    map[string]any `json:"ids"`
}

func NewRatingSync(db *sql.DB, baseURL string, client *http.Client, clientID, accessToken string) *RatingSync {
	if baseURL == "" {
		baseURL = "https://api.trakt.tv"
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &RatingSync{db: db, baseURL: strings.TrimRight(baseURL, "/"), httpClient: client, clientID: clientID, accessToken: accessToken, now: time.Now}
}

func (s *RatingSync) SetNow(now func() time.Time) { s.now = now }

func (s *RatingSync) ImportInitial(ctx context.Context) error {
	var complete string
	err := s.db.QueryRowContext(ctx, `SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='initial_ratings_complete'`).Scan(&complete)
	if err == nil && complete == "1" {
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err := s.Reconcile(ctx); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt','initial_ratings_complete','1') ON CONFLICT(integration,state_key) DO UPDATE SET state_value='1',updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`)
	return err
}

func (s *RatingSync) Reconcile(ctx context.Context) error {
	items, err := s.fetchRatings(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Rating < 1 || item.Rating > 10 || item.RatedAt == "" {
			continue
		}
		ratedAt, err := time.Parse(time.RFC3339, item.RatedAt)
		if err != nil {
			continue
		}
		mediaID, err := s.matchRating(ctx, item)
		if err != nil {
			return err
		}
		if mediaID == 0 {
			continue
		}
		if err := s.applyRemote(ctx, mediaID, item.Rating, ratedAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *RatingSync) FlushPending(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT media_id,pending_rating,pending_delete,attempt_count FROM rating_sync_state WHERE (pending_rating IS NOT NULL OR pending_delete=1) AND (next_attempt_at IS NULL OR next_attempt_at<=?) ORDER BY media_id`, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	defer rows.Close()
	type pending struct {
		mediaID int64
		rating  sql.NullInt64
		delete  bool
		attempt int
	}
	var work []pending
	for rows.Next() {
		var p pending
		var del int
		if err := rows.Scan(&p.mediaID, &p.rating, &del, &p.attempt); err != nil {
			return err
		}
		p.delete = del == 1
		work = append(work, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range work {
		if err := s.flushOne(ctx, p.mediaID, p.rating, p.delete, p.attempt); err != nil {
			return err
		}
	}
	return nil
}

func (s *RatingSync) fetchRatings(ctx context.Context) ([]remoteRating, error) {
	var all []remoteRating
	for page := 1; ; page++ {
		req, err := s.request(ctx, http.MethodGet, fmt.Sprintf("/sync/ratings/all?page=%d&limit=100", page), nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch trakt ratings: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxRatingResponseBytes+1))
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if len(body) > maxRatingResponseBytes {
			return nil, fmt.Errorf("fetch trakt ratings: response too large")
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch trakt ratings: HTTP %d", resp.StatusCode)
		}
		var items []remoteRating
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, fmt.Errorf("decode trakt ratings: %w", err)
		}
		all = append(all, items...)
		pages := 1
		if raw := resp.Header.Get("X-Pagination-Page-Count"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				pages = n
			}
		}
		if page >= pages {
			break
		}
	}
	return all, nil
}

func (s *RatingSync) request(ctx context.Context, method, path string, payload any) (*http.Request, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", s.clientID)
	if s.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.accessToken)
	}
	return req, nil
}

func (s *RatingSync) matchRating(ctx context.Context, item remoteRating) (int64, error) {
	var ids map[string]any
	switch item.Type {
	case "movie":
		if item.Movie != nil { ids = item.Movie.IDs }
	case "episode":
		if item.Episode != nil { ids = item.Episode.IDs }
	case "season":
		if item.Season != nil { ids = item.Season.IDs }
	default:
		return 0, nil
	}
	if traktID, ok := stringID(ids["trakt"]); ok {
		var id int64
		err := s.db.QueryRowContext(ctx, `SELECT media_id FROM external_ids WHERE provider='trakt' AND external_id=?`, traktID).Scan(&id)
		if err == nil { return id, nil }
		if err != sql.ErrNoRows { return 0, err }
	}
	if item.Type == "season" && item.Show != nil && item.Season != nil {
		showTrakt, ok := stringID(item.Show.IDs["trakt"])
		if !ok { return 0, nil }
		var id int64
		err := s.db.QueryRowContext(ctx, `SELECT s.id FROM media_items s JOIN external_ids x ON x.media_id=s.parent_id AND x.provider='trakt' WHERE s.media_type='season' AND s.season_number=? AND x.external_id=?`, item.Season.Number, showTrakt).Scan(&id)
		if err == sql.ErrNoRows { return 0, nil }
		if err != nil { return 0, err }
		if seasonTrakt, ok := stringID(item.Season.IDs["trakt"]); ok {
			_, _ = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO external_ids(media_id,provider,external_id) VALUES(?,'trakt',?)`, id, seasonTrakt)
		}
		return id, nil
	}
	return 0, nil
}

func (s *RatingSync) applyRemote(ctx context.Context, mediaID int64, value int, ratedAt time.Time) error {
	remoteAt := ratedAt.UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer tx.Rollback()
	var lastLocal, lastRemote sql.NullString
	var pending sql.NullInt64
	var pendingDelete int
	err = tx.QueryRowContext(ctx, `SELECT last_local_change_at,last_remote_change_at,pending_rating,pending_delete FROM rating_sync_state WHERE media_id=?`, mediaID).Scan(&lastLocal, &lastRemote, &pending, &pendingDelete)
	if err != nil && err != sql.ErrNoRows { return err }
	if lastRemote.Valid && remoteAt <= lastRemote.String { return tx.Commit() }
	if lastLocal.Valid && remoteAt <= lastLocal.String {
		if pending.Valid && int(pending.Int64) == value {
			_, err = tx.ExecContext(ctx, `UPDATE rating_sync_state SET last_remote_change_at=?,last_remote_rating=?,pending_rating=NULL,pending_delete=0,attempt_count=0,next_attempt_at=NULL,last_error=NULL,updated_at=? WHERE media_id=?`, remoteAt, value, remoteAt, mediaID)
			if err != nil { return err }
		}
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ratings(media_id,rating,source,remote_updated_at,local_updated_at) VALUES(? ,?,'trakt',?,?) ON CONFLICT(media_id) DO UPDATE SET rating=excluded.rating,source='trakt',remote_updated_at=excluded.remote_updated_at,local_updated_at=excluded.local_updated_at`, mediaID, value, remoteAt, remoteAt)
	if err != nil { return err }
	_, err = tx.ExecContext(ctx, `INSERT INTO rating_sync_state(media_id,last_remote_change_at,last_remote_rating,updated_at) VALUES(?,?,?,?) ON CONFLICT(media_id) DO UPDATE SET last_remote_change_at=excluded.last_remote_change_at,last_remote_rating=excluded.last_remote_rating,pending_rating=NULL,pending_delete=0,attempt_count=0,next_attempt_at=NULL,last_error=NULL,updated_at=excluded.updated_at`, mediaID, remoteAt, value, remoteAt)
	if err != nil { return err }
	return tx.Commit()
}

func (s *RatingSync) flushOne(ctx context.Context, mediaID int64, rating sql.NullInt64, deleting bool, attempt int) error {
	mediaType, traktID, err := s.outboundIdentity(ctx, mediaID)
	if err != nil { return err }
	item := map[string]any{"ids": map[string]string{"trakt": traktID}}
	if rating.Valid { item["rating"] = rating.Int64 }
	payload := map[string]any{mediaType + "s": []any{item}}
	path := "/sync/ratings"
	if deleting { path += "/remove" }
	req, err := s.request(ctx, http.MethodPost, path, payload)
	if err != nil { return err }
	resp, err := s.httpClient.Do(req)
	if err != nil { return s.recordFailure(ctx, mediaID, attempt, err.Error(), 0) }
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxRatingResponseBytes))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retryAfter := 0
		if raw := resp.Header.Get("Retry-After"); raw != "" { retryAfter, _ = strconv.Atoi(raw) }
		return s.recordFailure(ctx, mediaID, attempt, fmt.Sprintf("Trakt HTTP %d", resp.StatusCode), retryAfter)
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	var remote any
	if rating.Valid { remote = rating.Int64 }
	_, err = s.db.ExecContext(ctx, `UPDATE rating_sync_state SET last_remote_change_at=?,last_remote_rating=?,pending_rating=NULL,pending_delete=0,attempt_count=0,next_attempt_at=NULL,last_error=NULL,updated_at=? WHERE media_id=?`, now, remote, now, mediaID)
	return err
}

func (s *RatingSync) outboundIdentity(ctx context.Context, mediaID int64) (string, string, error) {
	var mediaType string
	if err := s.db.QueryRowContext(ctx, `SELECT media_type FROM media_items WHERE id=?`, mediaID).Scan(&mediaType); err != nil { return "", "", err }
	if mediaType != "movie" && mediaType != "season" && mediaType != "episode" { return "", "", fmt.Errorf("unsupported rating target %q", mediaType) }
	var traktID string
	if err := s.db.QueryRowContext(ctx, `SELECT external_id FROM external_ids WHERE media_id=? AND provider='trakt'`, mediaID).Scan(&traktID); err != nil { return "", "", fmt.Errorf("rating target missing Trakt id: %w", err) }
	return mediaType, traktID, nil
}

func (s *RatingSync) recordFailure(ctx context.Context, mediaID int64, attempt int, message string, retryAfter int) error {
	attempt++
	delay := time.Second << min(attempt-1, 6)
	if retryAfter > 0 && time.Duration(retryAfter)*time.Second > delay { delay = time.Duration(retryAfter) * time.Second }
	if delay > time.Hour { delay = time.Hour }
	next := s.now().UTC().Add(delay).Format(time.RFC3339Nano)
	if len(message) > 256 { message = message[:256] }
	_, err := s.db.ExecContext(ctx, `UPDATE rating_sync_state SET attempt_count=?,next_attempt_at=?,last_error=?,updated_at=? WHERE media_id=?`, attempt, next, message, s.now().UTC().Format(time.RFC3339Nano), mediaID)
	if err != nil { return err }
	return fmt.Errorf("sync rating: %s", message)
}

func min(a, b int) int { if a < b { return a }; return b }
