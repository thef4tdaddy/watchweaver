package trakt

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/metadata"
)

type HistoryImporter struct {
	db          *sql.DB
	baseURL     string
	httpClient  *http.Client
	accessToken string
	clientID    string
}

func (h *HistoryImporter) SetClientID(clientID string) { h.clientID = strings.TrimSpace(clientID) }

type HistoryImportResult struct {
	Imported         int
	Skipped          int
	Scanned          int
	Pages            int
	NewWatchEventIDs []int64
	FinaleSeasonIDs  []int64
}

// ImportFinalesSince scans a bounded history window and only writes explicit
// season/series finale records. It is intended for metadata backfills: replaying
// every non-finale watch through the normal importer makes large libraries take
// minutes and performs thousands of unnecessary SQLite transactions.
func (h *HistoryImporter) ImportFinalesSince(ctx context.Context, since time.Time) (HistoryImportResult, error) {
	var r HistoryImportResult
	for page := 1; ; page++ {
		items, pages, err := h.fetchPage(ctx, page, since)
		if err != nil {
			return r, err
		}
		r.Pages++
		r.Scanned += len(items)
		for _, item := range items {
			if item.Episode == nil || !metadata.FinaleFromTrakt(item.Episode.EpisodeType).CompletesSeason() {
				continue
			}
			inserted, eventID, err := h.persistItem(ctx, item, false)
			if err != nil {
				return r, err
			}
			if inserted {
				r.Imported++
			} else {
				r.Skipped++
			}
			seasonID, baselineEvent, err := h.seasonForEvent(ctx, eventID)
			if err != nil {
				return r, err
			}
			if seasonID != 0 && !baselineEvent {
				r.FinaleSeasonIDs = appendUnique(r.FinaleSeasonIDs, seasonID)
			}
		}
		if page >= pages {
			return r, nil
		}
	}
}

type RetryableError struct {
	StatusCode int
	RetryAfter time.Duration
}

func (e *RetryableError) Error() string {
	return fmt.Sprintf("fetch trakt history: HTTP %d", e.StatusCode)
}

type historyItem struct {
	ID        int64  `json:"id"`
	WatchedAt string `json:"watched_at"`
	Action    string `json:"action"`
	Type      string `json:"type"`
	Movie     *struct {
		Title string         `json:"title"`
		Year  int            `json:"year"`
		IDs   map[string]any `json:"ids"`
	} `json:"movie"`
	Episode *struct {
		Season      int            `json:"season"`
		Number      int            `json:"number"`
		Title       string         `json:"title"`
		IDs         map[string]any `json:"ids"`
		EpisodeType string         `json:"episode_type"`
	} `json:"episode"`
	Show *struct {
		Title string         `json:"title"`
		Year  int            `json:"year"`
		IDs   map[string]any `json:"ids"`
	} `json:"show"`
}

func NewHistoryImporter(db *sql.DB, baseURL string, client *http.Client, accessToken string) *HistoryImporter {
	if baseURL == "" {
		baseURL = "https://api.trakt.tv"
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &HistoryImporter{db: db, baseURL: baseURL, httpClient: client, accessToken: accessToken}
}
func (h *HistoryImporter) ImportInitial(ctx context.Context) (HistoryImportResult, error) {
	if done, err := h.initialSyncComplete(ctx); err != nil {
		return HistoryImportResult{}, err
	} else if done {
		return HistoryImportResult{}, nil
	}
	r, err := h.importAll(ctx, time.Time{}, true)
	if err != nil {
		return r, err
	}
	_, err = h.db.ExecContext(ctx, `INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt','initial_history_complete','1') ON CONFLICT(integration,state_key) DO UPDATE SET state_value='1',updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`)
	return r, err
}
func (h *HistoryImporter) ImportIncremental(ctx context.Context) (HistoryImportResult, error) {
	return h.importAll(ctx, time.Time{}, false)
}
func (h *HistoryImporter) ImportIncrementalSince(ctx context.Context, since time.Time) (HistoryImportResult, error) {
	return h.importAll(ctx, since, false)
}
func (h *HistoryImporter) initialSyncComplete(ctx context.Context) (bool, error) {
	var v string
	err := h.db.QueryRowContext(ctx, `SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='initial_history_complete'`).Scan(&v)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return v == "1", err
}
func (h *HistoryImporter) importAll(ctx context.Context, since time.Time, baseline bool) (HistoryImportResult, error) {
	var r HistoryImportResult
	for page := 1; ; page++ {
		items, pages, err := h.fetchPage(ctx, page, since)
		if err != nil {
			return r, err
		}
		r.Pages++
		for _, item := range items {
			inserted, eventID, err := h.persistItem(ctx, item, baseline)
			if err != nil {
				return r, err
			}
			if inserted {
				r.Imported++
				if !baseline {
					r.NewWatchEventIDs = append(r.NewWatchEventIDs, eventID)
				}
			} else {
				r.Skipped++
			}
			if !baseline && item.Episode != nil && metadata.FinaleFromTrakt(item.Episode.EpisodeType).CompletesSeason() {
				seasonID, baselineEvent, err := h.seasonForEvent(ctx, eventID)
				if err != nil {
					return r, err
				}
				if seasonID != 0 && !baselineEvent {
					r.FinaleSeasonIDs = appendUnique(r.FinaleSeasonIDs, seasonID)
				}
			}
		}
		if page >= pages {
			break
		}
	}
	return r, nil
}
func (h *HistoryImporter) fetchPage(ctx context.Context, page int, since time.Time) ([]historyItem, int, error) {
	u, err := url.Parse(h.baseURL + "/sync/history")
	if err != nil {
		return nil, 0, err
	}
	q := u.Query()
	q.Set("page", strconv.Itoa(page))
	q.Set("limit", "100")
	// episode_type (season_finale/series_finale) is extended episode metadata;
	// compact history responses omit it.
	q.Set("extended", "full")
	if !since.IsZero() {
		q.Set("start_at", since.UTC().Format(time.RFC3339))
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	if h.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.accessToken)
	}
	if h.clientID != "" {
		req.Header.Set("trakt-api-key", h.clientID)
	}
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch trakt history: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, 0, &RetryableError{StatusCode: resp.StatusCode, RetryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now())}
		}
		return nil, 0, fmt.Errorf("fetch trakt history: HTTP %d", resp.StatusCode)
	}
	var items []historyItem
	if err = json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, 0, fmt.Errorf("decode trakt history: %w", err)
	}
	pages := 1
	if raw := resp.Header.Get("X-Pagination-Page-Count"); raw != "" {
		if n, e := strconv.Atoi(raw); e == nil && n > 0 {
			pages = n
		}
	}
	return items, pages, nil
}
func retryAfter(raw string, now time.Time) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil && when.After(now) {
		return when.Sub(now)
	}
	return 0
}
func (h *HistoryImporter) persistItem(ctx context.Context, item historyItem, baseline bool) (bool, int64, error) {
	if item.ID == 0 || item.WatchedAt == "" {
		return false, 0, fmt.Errorf("invalid trakt history item")
	}
	watched, err := time.Parse(time.RFC3339, item.WatchedAt)
	if err != nil {
		return false, 0, fmt.Errorf("parse watched_at: %w", err)
	}
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback()
	var existingEventID, existingMediaID int64
	if err = tx.QueryRowContext(ctx, `SELECT id,media_id FROM watch_events WHERE source='trakt' AND source_event_id=?`, strconv.FormatInt(item.ID, 10)).Scan(&existingEventID, &existingMediaID); err == nil {
		if err := h.storeEpisodeMetadata(ctx, tx, existingMediaID, item); err != nil {
			return false, 0, err
		}
		if err := tx.Commit(); err != nil {
			return false, 0, err
		}
		return false, existingEventID, nil
	} else if err != sql.ErrNoRows {
		return false, 0, err
	}
	mediaID, err := h.ensureMedia(ctx, tx, item)
	if err != nil {
		return false, 0, err
	}
	if err := h.storeEpisodeMetadata(ctx, tx, mediaID, item); err != nil {
		return false, 0, err
	}
	baselineValue := 0
	if baseline {
		baselineValue = 1
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at,is_baseline) VALUES(?,'trakt',?,?,?,?)`, mediaID, strconv.FormatInt(item.ID, 10), watched.UTC().Format(time.RFC3339), item.WatchedAt, baselineValue)
	if err != nil {
		return false, 0, err
	}
	eventID, err := result.LastInsertId()
	if err != nil {
		return false, 0, err
	}
	if err = tx.Commit(); err != nil {
		return false, 0, err
	}
	return true, eventID, nil
}

func (h *HistoryImporter) storeEpisodeMetadata(ctx context.Context, tx *sql.Tx, mediaID int64, item historyItem) error {
	if item.Episode == nil {
		return nil
	}
	finale := metadata.FinaleFromTrakt(item.Episode.EpisodeType)
	_, err := tx.ExecContext(ctx, `INSERT INTO episode_metadata(media_id,finale_type,provider) VALUES(?,?,'trakt') ON CONFLICT(media_id) DO UPDATE SET finale_type=excluded.finale_type,provider=excluded.provider,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`, mediaID, finale)
	return err
}

func (h *HistoryImporter) seasonForEvent(ctx context.Context, eventID int64) (int64, bool, error) {
	if eventID == 0 {
		return 0, false, nil
	}
	var seasonID int64
	var baseline bool
	err := h.db.QueryRowContext(ctx, `SELECT episode.parent_id,w.is_baseline FROM watch_events w JOIN media_items episode ON episode.id=w.media_id WHERE w.id=? AND episode.media_type='episode'`, eventID).Scan(&seasonID, &baseline)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return seasonID, baseline, err
}

func appendUnique(values []int64, value int64) []int64 {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
func (h *HistoryImporter) ensureMedia(ctx context.Context, tx *sql.Tx, item historyItem) (int64, error) {
	switch {
	case item.Movie != nil:
		return ensureMediaItem(ctx, tx, "movie", item.Movie.Title, item.Movie.Year, nil, nil, item.Movie.IDs)
	case item.Episode != nil && item.Show != nil:
		showID, err := ensureMediaItem(ctx, tx, "show", item.Show.Title, item.Show.Year, nil, nil, item.Show.IDs)
		if err != nil {
			return 0, err
		}
		season := item.Episode.Season
		seasonID, err := ensureMediaItem(ctx, tx, "season", item.Show.Title+" Season "+strconv.Itoa(season), 0, &showID, &season, nil)
		if err != nil {
			return 0, err
		}
		episode := item.Episode.Number
		title := item.Episode.Title
		if title == "" {
			title = "Episode " + strconv.Itoa(episode)
		}
		return ensureMediaItem(ctx, tx, "episode", title, 0, &seasonID, &episode, item.Episode.IDs)
	default:
		return 0, fmt.Errorf("unsupported trakt history item")
	}
}
func ensureMediaItem(ctx context.Context, tx *sql.Tx, mediaType, title string, year int, parentID *int64, number *int, ids map[string]any) (int64, error) {
	if ids != nil {
		if traktID, ok := numericID(ids["trakt"]); ok {
			var id int64
			err := tx.QueryRowContext(ctx, `SELECT media_id FROM external_ids WHERE provider='trakt' AND external_id=?`, strconv.FormatInt(traktID, 10)).Scan(&id)
			if err == nil {
				return id, nil
			}
			if err != sql.ErrNoRows {
				return 0, err
			}
		}
	}
	var res sql.Result
	var err error
	switch mediaType {
	case "movie", "show":
		var y any
		if year > 0 {
			y = year
		}
		res, err = tx.ExecContext(ctx, `INSERT INTO media_items(media_type,title,year) VALUES(?,?,?)`, mediaType, title, y)
	case "season":
		res, err = tx.ExecContext(ctx, `INSERT INTO media_items(media_type,title,parent_id,season_number) VALUES('season',?,?,?)`, title, *parentID, *number)
	case "episode":
		res, err = tx.ExecContext(ctx, `INSERT INTO media_items(media_type,title,parent_id,episode_number) VALUES('episode',?,?,?)`, title, *parentID, *number)
	default:
		return 0, fmt.Errorf("unsupported media type %q", mediaType)
	}
	if err != nil {
		if mediaType == "season" {
			var id int64
			if e := tx.QueryRowContext(ctx, `SELECT id FROM media_items WHERE media_type='season' AND parent_id=? AND season_number=?`, *parentID, *number).Scan(&id); e == nil {
				return id, nil
			}
		}
		if mediaType == "episode" {
			var id int64
			if e := tx.QueryRowContext(ctx, `SELECT id FROM media_items WHERE media_type='episode' AND parent_id=? AND episode_number=?`, *parentID, *number).Scan(&id); e == nil {
				return id, nil
			}
		}
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for provider, raw := range ids {
		if provider != "trakt" && provider != "tmdb" && provider != "imdb" {
			continue
		}
		if value, ok := stringID(raw); ok && value != "" {
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO external_ids(media_id,provider,external_id) VALUES(?,?,?)`, id, provider, value); err != nil {
				return 0, err
			}
		}
	}
	return id, nil
}
func numericID(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	default:
		return 0, false
	}
}
func stringID(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, x != ""
	case float64:
		return strconv.FormatInt(int64(x), 10), true
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	default:
		return "", false
	}
}
