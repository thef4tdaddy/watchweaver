package jellyfin

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidEvent  = errors.New("invalid Jellyfin event")
	ErrEventConflict = errors.New("Jellyfin event identity conflict")
)

type Event struct {
	EventID       string            `json:"event_id"`
	EventType     string            `json:"event_type"`
	ServerID      string            `json:"server_id"`
	ServerVersion string            `json:"server_version"`
	PluginVersion string            `json:"plugin_version"`
	OccurredAt    string            `json:"occurred_at"`
	UserID        string            `json:"user_id,omitempty"`
	Item          Item              `json:"item"`
	ExternalIDs   map[string]string `json:"external_ids,omitempty"`
}

type Item struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Title         string `json:"title"`
	Year          *int   `json:"year,omitempty"`
	ShowTitle     string `json:"show_title,omitempty"`
	SeasonNumber  *int   `json:"season_number,omitempty"`
	EpisodeNumber *int   `json:"episode_number,omitempty"`
}

type Result struct {
	WatchEventID int64 `json:"watch_event_id"`
	Duplicate    bool  `json:"duplicate"`
}

type Status struct {
	Configured        bool    `json:"configured"`
	AcceptedCount     int64   `json:"accepted_count"`
	AuthFailureCount  int64   `json:"auth_failure_count"`
	LastAcceptedAt    *string `json:"last_accepted_at,omitempty"`
	LastServerVersion *string `json:"last_server_version,omitempty"`
	LastPluginVersion *string `json:"last_plugin_version,omitempty"`
	LastRejectionAt   *string `json:"last_rejection_at,omitempty"`
	LastRejectionCode *string `json:"last_rejection_code,omitempty"`
	LastAuthFailureAt *string `json:"last_auth_failure_at,omitempty"`
}

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service { return &Service{db: db} }

func (s *Service) Accept(ctx context.Context, event Event) (Result, error) {
	if err := validate(event); err != nil {
		s.RecordRejection(ctx, "invalid_event")
		return Result{}, err
	}
	fingerprint, err := eventFingerprint(event)
	if err != nil {
		return Result{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()

	var existingID int64
	var existingFingerprint string
	err = tx.QueryRowContext(ctx, `SELECT watch_event_id,fingerprint FROM jellyfin_ingest_events WHERE server_id=? AND event_id=?`, strings.TrimSpace(event.ServerID), strings.TrimSpace(event.EventID)).Scan(&existingID, &existingFingerprint)
	if err == nil {
		if existingFingerprint != fingerprint {
			return Result{}, ErrEventConflict
		}
		return Result{WatchEventID: existingID, Duplicate: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Result{}, err
	}

	mediaID, err := ensureMedia(ctx, tx, event)
	if err != nil {
		return Result{}, err
	}
	occurred, _ := time.Parse(time.RFC3339Nano, event.OccurredAt)
	canonicalTime := occurred.UTC().Format(time.RFC3339Nano)
	sourceID := strings.TrimSpace(event.ServerID) + ":" + strings.TrimSpace(event.EventID)
	res, err := tx.ExecContext(ctx, `INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at,is_baseline) VALUES(?,'jellyfin',?,?,?,0)`, mediaID, sourceID, canonicalTime, strings.TrimSpace(event.OccurredAt))
	if err != nil {
		return Result{}, err
	}
	watchEventID, err := res.LastInsertId()
	if err != nil {
		return Result{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO jellyfin_ingest_events(server_id,event_id,event_type,fingerprint,watch_event_id,plugin_version,server_version) VALUES(?,?,?,?,?,?,?)`, strings.TrimSpace(event.ServerID), strings.TrimSpace(event.EventID), event.EventType, fingerprint, watchEventID, strings.TrimSpace(event.PluginVersion), strings.TrimSpace(event.ServerVersion)); err != nil {
		return Result{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jellyfin_ingest_status SET accepted_count=accepted_count+1,last_accepted_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'),last_server_version=?,last_plugin_version=?,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE singleton=1`, strings.TrimSpace(event.ServerVersion), strings.TrimSpace(event.PluginVersion)); err != nil {
		return Result{}, err
	}
	if err := tx.Commit(); err != nil {
		return Result{}, err
	}
	return Result{WatchEventID: watchEventID}, nil
}

func (s *Service) RecordAuthFailure(ctx context.Context) {
	_, _ = s.db.ExecContext(ctx, `UPDATE jellyfin_ingest_status SET auth_failure_count=auth_failure_count+1,last_auth_failure_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'),updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE singleton=1`)
}

func (s *Service) RecordRejection(ctx context.Context, code string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE jellyfin_ingest_status SET last_rejection_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'),last_rejection_code=?,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE singleton=1`, code)
}

func (s *Service) Status(ctx context.Context, configured bool) (Status, error) {
	var out Status
	out.Configured = configured
	var accepted, serverVersion, pluginVersion, rejectionAt, rejectionCode, authAt sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT accepted_count,auth_failure_count,last_accepted_at,last_server_version,last_plugin_version,last_rejection_at,last_rejection_code,last_auth_failure_at FROM jellyfin_ingest_status WHERE singleton=1`).Scan(&out.AcceptedCount, &out.AuthFailureCount, &accepted, &serverVersion, &pluginVersion, &rejectionAt, &rejectionCode, &authAt)
	if err != nil {
		return Status{}, err
	}
	out.LastAcceptedAt = nullable(accepted)
	out.LastServerVersion = nullable(serverVersion)
	out.LastPluginVersion = nullable(pluginVersion)
	out.LastRejectionAt = nullable(rejectionAt)
	out.LastRejectionCode = nullable(rejectionCode)
	out.LastAuthFailureAt = nullable(authAt)
	return out, nil
}

func nullable(v sql.NullString) *string {
	if !v.Valid { return nil }
	return &v.String
}

func validate(event Event) error {
	if strings.TrimSpace(event.EventID) == "" || strings.TrimSpace(event.ServerID) == "" || strings.TrimSpace(event.Item.ID) == "" || strings.TrimSpace(event.Item.Title) == "" {
		return ErrInvalidEvent
	}
	if event.EventType != "played" && event.EventType != "marked_played" {
		return ErrInvalidEvent
	}
	if event.Item.Type != "movie" && event.Item.Type != "episode" {
		return ErrInvalidEvent
	}
	if _, err := time.Parse(time.RFC3339Nano, event.OccurredAt); err != nil {
		return ErrInvalidEvent
	}
	if event.Item.Type == "episode" && (strings.TrimSpace(event.Item.ShowTitle) == "" || event.Item.SeasonNumber == nil || event.Item.EpisodeNumber == nil) {
		return ErrInvalidEvent
	}
	return nil
}

func eventFingerprint(event Event) (string, error) {
	raw, err := json.Marshal(event)
	if err != nil { return "", err }
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func ensureMedia(ctx context.Context, tx *sql.Tx, event Event) (int64, error) {
	providerID := "jellyfin:" + strings.TrimSpace(event.ServerID)
	if id, ok, err := findExternal(ctx, tx, providerID, event.Item.ID); err != nil || ok {
		return id, err
	}
	if event.Item.Type == "movie" {
		id, err := insertMedia(ctx, tx, "movie", event.Item.Title, event.Item.Year, nil, nil, nil)
		if err != nil { return 0, err }
		if err := attachIDs(ctx, tx, id, providerID, event.Item.ID, event.ExternalIDs); err != nil { return 0, err }
		return id, nil
	}

	showID, err := ensureShow(ctx, tx, event)
	if err != nil { return 0, err }
	seasonID, err := ensureSeason(ctx, tx, showID, *event.Item.SeasonNumber)
	if err != nil { return 0, err }
	id, err := insertMedia(ctx, tx, "episode", event.Item.Title, event.Item.Year, &seasonID, nil, event.Item.EpisodeNumber)
	if err != nil {
		var existing int64
		if qerr := tx.QueryRowContext(ctx, `SELECT id FROM media_items WHERE media_type='episode' AND parent_id=? AND episode_number=?`, seasonID, *event.Item.EpisodeNumber).Scan(&existing); qerr == nil {
			id = existing
		} else { return 0, err }
	}
	if err := attachIDs(ctx, tx, id, providerID, event.Item.ID, event.ExternalIDs); err != nil { return 0, err }
	return id, nil
}

func ensureShow(ctx context.Context, tx *sql.Tx, event Event) (int64, error) {
	for provider, external := range event.ExternalIDs {
		if !strings.HasPrefix(strings.ToLower(provider), "show_") { continue }
		if id, ok, err := findExternal(ctx, tx, strings.TrimPrefix(strings.ToLower(provider), "show_"), external); err != nil || ok { return id, err }
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM media_items WHERE media_type='show' AND title=? ORDER BY id LIMIT 1`, strings.TrimSpace(event.Item.ShowTitle)).Scan(&id); err == nil { return id, nil }
	return insertMedia(ctx, tx, "show", event.Item.ShowTitle, nil, nil, nil, nil)
}

func ensureSeason(ctx context.Context, tx *sql.Tx, showID int64, number int) (int64, error) {
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM media_items WHERE media_type='season' AND parent_id=? AND season_number=?`, showID, number).Scan(&id); err == nil { return id, nil }
	title := "Season " + strconv.Itoa(number)
	return insertMedia(ctx, tx, "season", title, nil, &showID, &number, nil)
}

func insertMedia(ctx context.Context, tx *sql.Tx, kind, title string, year *int, parentID *int64, season, episode *int) (int64, error) {
	res, err := tx.ExecContext(ctx, `INSERT INTO media_items(media_type,title,year,parent_id,season_number,episode_number) VALUES(?,?,?,?,?,?)`, kind, strings.TrimSpace(title), year, parentID, season, episode)
	if err != nil { return 0, err }
	return res.LastInsertId()
}

func findExternal(ctx context.Context, tx *sql.Tx, provider, external string) (int64, bool, error) {
	provider, external = strings.TrimSpace(strings.ToLower(provider)), strings.TrimSpace(external)
	if provider == "" || external == "" { return 0, false, nil }
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT media_id FROM external_ids WHERE provider=? AND external_id=?`, provider, external).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) { return 0, false, nil }
	return id, err == nil, err
}

func attachIDs(ctx context.Context, tx *sql.Tx, mediaID int64, jellyfinProvider, jellyfinID string, ids map[string]string) error {
	pairs := map[string]string{jellyfinProvider: jellyfinID}
	for provider, external := range ids {
		provider = strings.ToLower(strings.TrimSpace(provider))
		if strings.HasPrefix(provider, "show_") || external == "" { continue }
		if provider == "tmdb" || provider == "imdb" || provider == "tvdb" || provider == "trakt" { pairs[provider] = strings.TrimSpace(external) }
	}
	for provider, external := range pairs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO external_ids(media_id,provider,external_id) VALUES(?,?,?) ON CONFLICT(media_id,provider) DO NOTHING`, mediaID, provider, external); err != nil {
			return fmt.Errorf("attach %s ID: %w", provider, err)
		}
	}
	return nil
}
