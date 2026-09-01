package trakt

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type HistoryImporter struct {
	db         *sql.DB
	baseURL    string
	httpClient *http.Client
	accessToken string
}

type HistoryImportResult struct {
	Imported int
	Skipped  int
	Pages    int
}

type historyItem struct {
	ID        int64  `json:"id"`
	WatchedAt string `json:"watched_at"`
	Action    string `json:"action"`
	Type      string `json:"type"`
	Movie     *struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
		IDs   map[string]any `json:"ids"`
	} `json:"movie"`
	Episode *struct {
		Season int    `json:"season"`
		Number int    `json:"number"`
		Title  string `json:"title"`
		IDs    map[string]any `json:"ids"`
	} `json:"episode"`
	Show *struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
		IDs   map[string]any `json:"ids"`
	} `json:"show"`
}

func NewHistoryImporter(db *sql.DB, baseURL string, client *http.Client, accessToken string) *HistoryImporter {
	if baseURL == "" { baseURL = "https://api.trakt.tv" }
	if client == nil { client = http.DefaultClient }
	return &HistoryImporter{db: db, baseURL: baseURL, httpClient: client, accessToken: accessToken}
}

func (h *HistoryImporter) ImportInitial(ctx context.Context) (HistoryImportResult, error) {
	if done, err := h.initialSyncComplete(ctx); err != nil { return HistoryImportResult{}, err } else if done { return HistoryImportResult{}, nil }
	result, err := h.importAll(ctx)
	if err != nil { return result, err }
	if _, err := h.db.ExecContext(ctx, `INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt','initial_history_complete','1') ON CONFLICT(integration,state_key) DO UPDATE SET state_value='1',updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`); err != nil { return result, err }
	return result, nil
}

func (h *HistoryImporter) ImportIncremental(ctx context.Context) (HistoryImportResult, error) {
	return h.importAll(ctx)
}

func (h *HistoryImporter) initialSyncComplete(ctx context.Context) (bool, error) {
	var v string
	err := h.db.QueryRowContext(ctx, `SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='initial_history_complete'`).Scan(&v)
	if err == sql.ErrNoRows { return false, nil }
	if err != nil { return false, err }
	return v == "1", nil
}

func (h *HistoryImporter) importAll(ctx context.Context) (HistoryImportResult, error) {
	var result HistoryImportResult
	for page := 1; ; page++ {
		items, pageCount, err := h.fetchPage(ctx, page)
		if err != nil { return result, err }
		result.Pages++
		for _, item := range items {
			inserted, err := h.persistItem(ctx, item)
			if err != nil { return result, err }
			if inserted { result.Imported++ } else { result.Skipped++ }
		}
		if page >= pageCount { break }
	}
	return result, nil
}

func (h *HistoryImporter) fetchPage(ctx context.Context, page int) ([]historyItem, int, error) {
	u, err := url.Parse(h.baseURL + "/sync/history")
	if err != nil { return nil, 0, err }
	q := u.Query(); q.Set("page", strconv.Itoa(page)); q.Set("limit", "100"); u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil { return nil, 0, err }
	if h.accessToken != "" { req.Header.Set("Authorization", "Bearer "+h.accessToken) }
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.httpClient.Do(req)
	if err != nil { return nil, 0, fmt.Errorf("fetch trakt history: %w", err) }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { return nil, 0, fmt.Errorf("fetch trakt history: HTTP %d", resp.StatusCode) }
	var items []historyItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil { return nil, 0, fmt.Errorf("decode trakt history: %w", err) }
	pageCount := 1
	if raw := resp.Header.Get("X-Pagination-Page-Count"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 { pageCount = n }
	}
	return items, pageCount, nil
}

func (h *HistoryImporter) persistItem(ctx context.Context, item historyItem) (bool, error) {
	if item.ID == 0 || item.WatchedAt == "" { return false, fmt.Errorf("invalid trakt history item") }
	watched, err := time.Parse(time.RFC3339, item.WatchedAt)
	if err != nil { return false, fmt.Errorf("parse watched_at: %w", err) }
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil { return false, err }
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM watch_events WHERE source='trakt' AND source_event_id=?`, strconv.FormatInt(item.ID,10)).Scan(&exists); err != nil { return false, err }
	if exists > 0 { return false, nil }
	mediaID, err := h.ensureMedia(ctx, tx, item)
	if err != nil { return false, err }
	if _, err := tx.ExecContext(ctx, `INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at) VALUES(?, 'trakt', ?, ?, ?)`, mediaID, strconv.FormatInt(item.ID,10), watched.UTC().Format(time.RFC3339), item.WatchedAt); err != nil { return false, err }
	if err := tx.Commit(); err != nil { return false, err }
	return true, nil
}

func (h *HistoryImporter) ensureMedia(ctx context.Context, tx *sql.Tx, item historyItem) (int64, error) {
	switch {
	case item.Movie != nil:
		return ensureMediaItem(ctx, tx, "movie", item.Movie.Title, item.Movie.Year, nil, nil, item.Movie.IDs)
	case item.Episode != nil && item.Show != nil:
		showID, err := ensureMediaItem(ctx, tx, "show", item.Show.Title, item.Show.Year, nil, nil, item.Show.IDs)
		if err != nil { return 0, err }
		season := item.Episode.Season
		seasonID, err := ensureMediaItem(ctx, tx, "season", item.Show.Title+" Season "+strconv.Itoa(season), 0, &showID, &season, nil)
		if err != nil { return 0, err }
		episode := item.Episode.Number
		return ensureMediaItem(ctx, tx, "episode", item.Episode.Title, 0, &seasonID, &episode, item.Episode.IDs)
	default:
		return 0, fmt.Errorf("unsupported trakt history item")
	}
}

func ensureMediaItem(ctx context.Context, tx *sql.Tx, mediaType, title string, year int, parentID *int64, number *int, ids map[string]any) (int64, error) {
	if ids != nil {
		if traktID, ok := numericID(ids["trakt"]); ok {
			var id int64
			err := tx.QueryRowContext(ctx, `SELECT media_id FROM external_ids WHERE provider='trakt' AND external_id=?`, strconv.FormatInt(traktID,10)).Scan(&id)
			if err == nil { return id, nil }
			if err != sql.ErrNoRows { return 0, err }
		}
	}
	var res sql.Result
	var err error
	switch mediaType {
	case "movie", "show":
		var y any = nil; if year > 0 { y = year }
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
			var id int64; if qerr := tx.QueryRowContext(ctx, `SELECT id FROM media_items WHERE media_type='season' AND parent_id=? AND season_number=?`, *parentID,*number).Scan(&id); qerr == nil { return id,nil }
		}
		if mediaType == "episode" {
			var id int64; if qerr := tx.QueryRowContext(ctx, `SELECT id FROM media_items WHERE media_type='episode' AND parent_id=? AND episode_number=?`, *parentID,*number).Scan(&id); qerr == nil { return id,nil }
		}
		return 0, err
	}
	id, err := res.LastInsertId(); if err != nil { return 0, err }
	if ids != nil {
		for provider, raw := range ids {
			if value, ok := stringID(raw); ok && value != "" {
				if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO external_ids(media_id,provider,external_id) VALUES(?,?,?)`, id, provider, value); err != nil { return 0, err }
			}
		}
	}
	return id, nil
}

func numericID(v any) (int64, bool) {
	switch n := v.(type) {
	case float64: return int64(n), true
	case int64: return n, true
	case int: return int64(n), true
	default: return 0, false
	}
}

func stringID(v any) (string, bool) {
	switch x := v.(type) {
	case string: return x, x != ""
	case float64: return strconv.FormatInt(int64(x),10), true
	case int: return strconv.Itoa(x), true
	case int64: return strconv.FormatInt(x,10), true
	default: return "", false
	}
}
