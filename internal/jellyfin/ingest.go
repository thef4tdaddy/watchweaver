package jellyfin

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/metadata"
	"github.com/thef4tdaddy/watchweaver/internal/prompts"
)

var (
	ErrInvalidEvent  = errors.New("invalid Jellyfin event")
	ErrEventConflict = errors.New("Jellyfin event identity conflict")
)

type Event struct {
	SchemaVersion int      `json:"schema_version"`
	EventID       string   `json:"event_id"`
	EventType     string   `json:"event_type"`
	OccurredAt    string   `json:"occurred_at"`
	Server        Server   `json:"server"`
	Plugin        Plugin   `json:"plugin"`
	User          User     `json:"user"`
	Item          Item     `json:"item"`
	Playback      Playback `json:"playback"`
}
type Server struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}
type Plugin struct {
	Version   string `json:"version"`
	TargetABI string `json:"target_abi,omitempty"`
}
type User struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}
type Item struct {
	ID                          string            `json:"id"`
	Type                        string            `json:"type"`
	Title                       string            `json:"title"`
	Year                        *int              `json:"year,omitempty"`
	SeriesTitle                 string            `json:"series_title,omitempty"`
	SeriesID                    string            `json:"series_id,omitempty"`
	SeasonID                    string            `json:"season_id,omitempty"`
	SeasonNumber                *int              `json:"season_number,omitempty"`
	EpisodeNumber               *int              `json:"episode_number,omitempty"`
	ProviderIDs                 map[string]string `json:"provider_ids,omitempty"`
	SeriesProviderIDs           map[string]string `json:"series_provider_ids,omitempty"`
	SeasonProviderIDs           map[string]string `json:"season_provider_ids,omitempty"`
	EpisodeType                 string            `json:"episode_type,omitempty"`
	SeasonEpisodeCount          *int              `json:"season_episode_count,omitempty"`
	SeasonWatchedEpisodeCount   *int              `json:"season_watched_episode_count,omitempty"`
	SeasonFutureEpisodeCount    *int              `json:"season_future_episode_count,omitempty"`
	LatestReleasedEpisodeNumber *int              `json:"latest_released_episode_number,omitempty"`
}
type Playback struct {
	Played          bool     `json:"played"`
	PositionTicks   *int64   `json:"position_ticks,omitempty"`
	RuntimeTicks    *int64   `json:"runtime_ticks,omitempty"`
	ProgressPercent *float64 `json:"progress_percent,omitempty"`
	PlayCount       *int     `json:"play_count,omitempty"`
	Client          string   `json:"client,omitempty"`
	Device          string   `json:"device,omitempty"`
}
type Result struct {
	WatchEventID    int64 `json:"watch_event_id"`
	Duplicate       bool  `json:"duplicate"`
	ProtocolVersion int   `json:"protocol_version"`
}
type Status struct {
	Configured             bool    `json:"configured"`
	ProtocolVersion        int     `json:"protocol_version"`
	AcceptedCount          int64   `json:"accepted_count"`
	AuthFailureCount       int64   `json:"auth_failure_count"`
	LastAcceptedAt         *string `json:"last_accepted_at,omitempty"`
	LastServerVersion      *string `json:"last_server_version,omitempty"`
	LastPluginVersion      *string `json:"last_plugin_version,omitempty"`
	LastRejectionAt        *string `json:"last_rejection_at,omitempty"`
	LastRejectionCode      *string `json:"last_rejection_code,omitempty"`
	LastAuthFailureAt      *string `json:"last_auth_failure_at,omitempty"`
	LastProbeAt            *string `json:"last_probe_at,omitempty"`
	LastProbeServerVersion *string `json:"last_probe_server_version,omitempty"`
	LastProbePluginVersion *string `json:"last_probe_plugin_version,omitempty"`
}
type Service struct{ db *sql.DB }

func NewService(db *sql.DB) *Service { return &Service{db: db} }

func (s *Service) Accept(ctx context.Context, e Event) (Result, error) {
	if err := validate(e); err != nil {
		s.RecordRejection(ctx, rejectionCode(err))
		return Result{}, err
	}
	fp, err := eventFingerprint(e)
	if err != nil {
		return Result{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	var existing int64
	var oldFP string
	err = tx.QueryRowContext(ctx, `SELECT watch_event_id,fingerprint FROM jellyfin_ingest_events WHERE server_id=? AND event_id=?`, trim(e.Server.ID), trim(e.EventID)).Scan(&existing, &oldFP)
	if err == nil {
		if oldFP != fp {
			return Result{}, ErrEventConflict
		}
		return Result{WatchEventID: existing, Duplicate: true, ProtocolVersion: 1}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Result{}, err
	}
	mediaID, err := ensureMedia(ctx, tx, e)
	if err != nil {
		return Result{}, err
	}
	previouslyWatched := false
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM watch_events WHERE media_id=? AND deleted_at IS NULL)`, mediaID).Scan(&previouslyWatched); err != nil {
		return Result{}, err
	}
	occurred, _ := time.Parse(time.RFC3339Nano, e.OccurredAt)
	canonical := occurred.UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx, `INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at,is_baseline) VALUES(?,'jellyfin',?,?,?,0)`, mediaID, trim(e.Server.ID)+":"+trim(e.EventID), canonical, trim(e.OccurredAt))
	if err != nil {
		return Result{}, err
	}
	watchID, err := res.LastInsertId()
	if err != nil {
		return Result{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO jellyfin_ingest_events(server_id,event_id,event_type,fingerprint,watch_event_id,plugin_version,server_version) VALUES(?,?,?,?,?,?,?)`, trim(e.Server.ID), trim(e.EventID), e.EventType, fp, watchID, trim(e.Plugin.Version), trim(e.Server.Version))
	if err != nil {
		return Result{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE jellyfin_ingest_status SET accepted_count=accepted_count+1,last_accepted_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'),last_server_version=?,last_plugin_version=?,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE singleton=1`, trim(e.Server.Version), trim(e.Plugin.Version))
	if err != nil {
		return Result{}, err
	}
	if err = tx.Commit(); err != nil {
		return Result{}, err
	}
	// The watch is acknowledged once durable. Prompt evaluation is deliberately
	// downstream so a task failure cannot make the plugin ambiguously redeliver
	// an already accepted watch.
	if err := s.evaluatePrompt(ctx, e, mediaID, previouslyWatched); err != nil {
		log.Printf("Jellyfin prompt evaluation deferred after durable event acceptance: %v", err)
	}
	return Result{WatchEventID: watchID, ProtocolVersion: 1}, nil
}

func (s *Service) evaluatePrompt(ctx context.Context, e Event, mediaID int64, previouslyWatched bool) error {
	batch := prompts.Batch{}
	if e.Item.Type == "movie" {
		batch.NewMovieWatches = []int64{mediaID}
		_, err := prompts.NewService(s.db).Apply(ctx, batch)
		return err
	}
	if previouslyWatched || e.Item.SeasonNumber == nil || *e.Item.SeasonNumber == 0 {
		return nil
	}
	var seasonID, showID int64
	if err := s.db.QueryRowContext(ctx, `SELECT season.id,season.parent_id FROM media_items episode JOIN media_items season ON season.id=episode.parent_id WHERE episode.id=?`, mediaID).Scan(&seasonID, &showID); err != nil {
		return err
	}
	if metadata.FinaleFromTrakt(e.Item.EpisodeType).CompletesSeason() {
		batch.CompletedSeasonIDs = []int64{seasonID}
	} else if inventoryComplete(e.Item) {
		batch.CompletedSeasonIDs = []int64{seasonID}
	} else if caughtUp(e.Item) {
		batch.NewEpisodeIDs = []int64{mediaID}
		batch.Seasons = []prompts.SeasonState{{SeasonID: seasonID, ShowID: showID, InventoryKnown: true, Episodes: []prompts.Episode{
			{ID: mediaID, Number: *e.Item.EpisodeNumber, Released: true, Watched: true, Normal: true},
			{ID: -1, Number: *e.Item.EpisodeNumber + 1, Released: false, Normal: true},
		}}}
	}
	_, err := prompts.NewService(s.db).Apply(ctx, batch)
	return err
}

func inventoryComplete(item Item) bool {
	return item.SeasonEpisodeCount != nil && item.SeasonWatchedEpisodeCount != nil && item.SeasonFutureEpisodeCount != nil && *item.SeasonEpisodeCount > 0 && *item.SeasonWatchedEpisodeCount == *item.SeasonEpisodeCount && *item.SeasonFutureEpisodeCount == 0
}

func caughtUp(item Item) bool {
	return item.SeasonEpisodeCount != nil && item.SeasonWatchedEpisodeCount != nil && item.SeasonFutureEpisodeCount != nil && item.LatestReleasedEpisodeNumber != nil && item.EpisodeNumber != nil && *item.SeasonEpisodeCount > 0 && *item.SeasonWatchedEpisodeCount == *item.SeasonEpisodeCount && *item.SeasonFutureEpisodeCount > 0 && *item.EpisodeNumber == *item.LatestReleasedEpisodeNumber
}
func (s *Service) RecordAuthFailure(ctx context.Context) {
	_, _ = s.db.ExecContext(ctx, `UPDATE jellyfin_ingest_status SET auth_failure_count=auth_failure_count+1,last_auth_failure_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'),updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE singleton=1`)
}
func (s *Service) RecordRejection(ctx context.Context, code string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE jellyfin_ingest_status SET last_rejection_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'),last_rejection_code=?,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE singleton=1`, code)
}
func (s *Service) RecordProbe(ctx context.Context, serverVersion, pluginVersion string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE jellyfin_ingest_status SET last_probe_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'),last_probe_server_version=?,last_probe_plugin_version=?,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE singleton=1`, trim(serverVersion), trim(pluginVersion))
}
func (s *Service) Status(ctx context.Context, configured bool) (Status, error) {
	out := Status{Configured: configured, ProtocolVersion: 1}
	var a, b, c, d, e, f, g, h, i sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT accepted_count,auth_failure_count,last_accepted_at,last_server_version,last_plugin_version,last_rejection_at,last_rejection_code,last_auth_failure_at,last_probe_at,last_probe_server_version,last_probe_plugin_version FROM jellyfin_ingest_status WHERE singleton=1`).Scan(&out.AcceptedCount, &out.AuthFailureCount, &a, &b, &c, &d, &e, &f, &g, &h, &i)
	if err != nil {
		return Status{}, err
	}
	out.LastAcceptedAt = nullable(a)
	out.LastServerVersion = nullable(b)
	out.LastPluginVersion = nullable(c)
	out.LastRejectionAt = nullable(d)
	out.LastRejectionCode = nullable(e)
	out.LastAuthFailureAt = nullable(f)
	out.LastProbeAt, out.LastProbeServerVersion, out.LastProbePluginVersion = nullable(g), nullable(h), nullable(i)
	return out, nil
}
func nullable(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}
func trim(s string) string { return strings.TrimSpace(s) }
func validate(e Event) error {
	if e.SchemaVersion != 1 {
		return fmt.Errorf("%w: unsupported_schema", ErrInvalidEvent)
	}
	if trim(e.EventID) == "" || len(e.EventID) > 128 || trim(e.Server.ID) == "" || len(e.Server.ID) > 256 || trim(e.Server.Version) == "" || trim(e.Plugin.Version) == "" || trim(e.User.ID) == "" || trim(e.Item.ID) == "" || trim(e.Item.Title) == "" {
		return fmt.Errorf("%w: missing_field", ErrInvalidEvent)
	}
	if e.EventType != "played" && e.EventType != "marked_played" {
		return fmt.Errorf("%w: unsupported_event_type", ErrInvalidEvent)
	}
	if e.Item.Type != "movie" && e.Item.Type != "episode" {
		return fmt.Errorf("%w: unsupported_item_type", ErrInvalidEvent)
	}
	if !e.Playback.Played {
		return fmt.Errorf("%w: not_played", ErrInvalidEvent)
	}
	if _, err := time.Parse(time.RFC3339Nano, e.OccurredAt); err != nil {
		return fmt.Errorf("%w: invalid_timestamp", ErrInvalidEvent)
	}
	if e.Item.Year != nil && (*e.Item.Year < 1888 || *e.Item.Year > 3000) {
		return fmt.Errorf("%w: invalid_number", ErrInvalidEvent)
	}
	if e.Item.Type == "episode" && (trim(e.Item.SeriesTitle) == "" || e.Item.SeasonNumber == nil || e.Item.EpisodeNumber == nil || *e.Item.SeasonNumber < 0 || *e.Item.EpisodeNumber < 0 || !hasStableIdentity(e.Item.SeriesID, e.Item.SeriesProviderIDs) || !hasStableIdentity(e.Item.SeasonID, e.Item.SeasonProviderIDs)) {
		return fmt.Errorf("%w: invalid_episode", ErrInvalidEvent)
	}
	if invalidCount(e.Item.SeasonEpisodeCount) || invalidCount(e.Item.SeasonWatchedEpisodeCount) || invalidCount(e.Item.SeasonFutureEpisodeCount) || (e.Item.SeasonEpisodeCount != nil && e.Item.SeasonWatchedEpisodeCount != nil && *e.Item.SeasonWatchedEpisodeCount > *e.Item.SeasonEpisodeCount) {
		return fmt.Errorf("%w: invalid_inventory", ErrInvalidEvent)
	}
	if e.Playback.PositionTicks != nil && *e.Playback.PositionTicks < 0 || e.Playback.RuntimeTicks != nil && *e.Playback.RuntimeTicks < 0 || e.Playback.ProgressPercent != nil && (*e.Playback.ProgressPercent < 0 || *e.Playback.ProgressPercent > 100) || e.Playback.PlayCount != nil && *e.Playback.PlayCount < 0 {
		return fmt.Errorf("%w: invalid_number", ErrInvalidEvent)
	}
	return nil
}
func invalidCount(value *int) bool { return value != nil && *value < 0 }
func hasStableIdentity(jellyfinID string, providerIDs map[string]string) bool {
	if trim(jellyfinID) != "" {
		return true
	}
	for _, provider := range []string{"tmdb", "tvdb", "imdb"} {
		if trim(providerIDs[provider]) != "" {
			return true
		}
	}
	return false
}
func rejectionCode(err error) string {
	parts := strings.Split(err.Error(), ": ")
	if len(parts) > 1 {
		return parts[len(parts)-1]
	}
	return "invalid_event"
}
func eventFingerprint(e Event) (string, error) {
	identity := struct{ EventType, OccurredAt, ServerID, UserID, ItemID, ItemType string }{e.EventType, trim(e.OccurredAt), trim(e.Server.ID), trim(e.User.ID), trim(e.Item.ID), e.Item.Type}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func ensureMedia(ctx context.Context, tx *sql.Tx, e Event) (int64, error) {
	jp := "jellyfin:" + trim(e.Server.ID)
	if id, ok, err := findExternal(ctx, tx, jp, e.Item.ID); err != nil || ok {
		return id, err
	}
	if e.Item.Type == "movie" {
		if id, ok, err := findPreferred(ctx, tx, e.Item.ProviderIDs); err != nil || ok {
			if err == nil {
				err = attachIDs(ctx, tx, id, jp, e.Item.ID, e.Item.ProviderIDs)
			}
			return id, err
		}
		id, err := insertMedia(ctx, tx, "movie", e.Item.Title, e.Item.Year, nil, nil, nil)
		if err != nil {
			return 0, err
		}
		return id, attachIDs(ctx, tx, id, jp, e.Item.ID, e.Item.ProviderIDs)
	}
	showID, err := ensureShow(ctx, tx, e)
	if err != nil {
		return 0, err
	}
	seasonID, err := ensureSeason(ctx, tx, e, showID, *e.Item.SeasonNumber)
	if err != nil {
		return 0, err
	}
	if id, ok, err := findPreferred(ctx, tx, e.Item.ProviderIDs); err != nil || ok {
		if err == nil {
			err = attachIDs(ctx, tx, id, jp, e.Item.ID, e.Item.ProviderIDs)
		}
		return id, err
	}
	id, err := insertMedia(ctx, tx, "episode", e.Item.Title, e.Item.Year, &seasonID, nil, e.Item.EpisodeNumber)
	if err != nil {
		if q := tx.QueryRowContext(ctx, `SELECT id FROM media_items WHERE media_type='episode' AND parent_id=? AND episode_number=?`, seasonID, *e.Item.EpisodeNumber).Scan(&id); q != nil {
			return 0, err
		}
	}
	return id, attachIDs(ctx, tx, id, jp, e.Item.ID, e.Item.ProviderIDs)
}
func ensureShow(ctx context.Context, tx *sql.Tx, e Event) (int64, error) {
	ids := e.Item.SeriesProviderIDs
	if id, ok, err := findPreferred(ctx, tx, ids); err != nil || ok {
		if err == nil && trim(e.Item.SeriesID) != "" {
			err = attachIDs(ctx, tx, id, "jellyfin:"+trim(e.Server.ID), e.Item.SeriesID, ids)
		}
		return id, err
	}
	jp := "jellyfin:" + trim(e.Server.ID)
	if id, ok, err := findExternal(ctx, tx, jp, e.Item.SeriesID); err != nil || ok {
		return id, err
	}
	id, err := insertMedia(ctx, tx, "show", e.Item.SeriesTitle, nil, nil, nil, nil)
	if err != nil {
		return 0, err
	}
	return id, attachIDs(ctx, tx, id, jp, e.Item.SeriesID, ids)
}
func ensureSeason(ctx context.Context, tx *sql.Tx, e Event, showID int64, n int) (int64, error) {
	jp := "jellyfin:" + trim(e.Server.ID)
	if id, ok, err := findExternal(ctx, tx, jp, e.Item.SeasonID); err != nil || ok {
		return id, err
	}
	if id, ok, err := findPreferred(ctx, tx, e.Item.SeasonProviderIDs); err != nil || ok {
		if err == nil && trim(e.Item.SeasonID) != "" {
			err = attachIDs(ctx, tx, id, jp, e.Item.SeasonID, e.Item.SeasonProviderIDs)
		}
		return id, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM media_items WHERE media_type='season' AND parent_id=? AND season_number=?`, showID, n).Scan(&id); err == nil {
		return id, attachIDs(ctx, tx, id, jp, e.Item.SeasonID, e.Item.SeasonProviderIDs)
	}
	id, err := insertMedia(ctx, tx, "season", "Season "+strconv.Itoa(n), nil, &showID, &n, nil)
	if err != nil {
		return 0, err
	}
	return id, attachIDs(ctx, tx, id, jp, e.Item.SeasonID, e.Item.SeasonProviderIDs)
}
func insertMedia(ctx context.Context, tx *sql.Tx, kind, title string, year *int, parent *int64, season, episode *int) (int64, error) {
	r, err := tx.ExecContext(ctx, `INSERT INTO media_items(media_type,title,year,parent_id,season_number,episode_number) VALUES(?,?,?,?,?,?)`, kind, trim(title), year, parent, season, episode)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}
func findPreferred(ctx context.Context, tx *sql.Tx, ids map[string]string) (int64, bool, error) {
	for _, p := range []string{"tmdb", "imdb", "tvdb"} {
		if id, ok, err := findExternal(ctx, tx, p, ids[p]); err != nil || ok {
			return id, ok, err
		}
	}
	return 0, false, nil
}
func findExternal(ctx context.Context, tx *sql.Tx, p, x string) (int64, bool, error) {
	p, x = trim(strings.ToLower(p)), trim(x)
	if p == "" || x == "" {
		return 0, false, nil
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT media_id FROM external_ids WHERE provider=? AND external_id=?`, p, x).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}
func attachIDs(ctx context.Context, tx *sql.Tx, id int64, jp, jid string, ids map[string]string) error {
	pairs := map[string]string{}
	if trim(jp) != "" && trim(jid) != "" {
		pairs[jp] = trim(jid)
	}
	for p, x := range ids {
		p = strings.ToLower(trim(p))
		if (p == "tmdb" || p == "imdb" || p == "tvdb") && trim(x) != "" {
			pairs[p] = trim(x)
		}
	}
	for p, x := range pairs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO external_ids(media_id,provider,external_id) VALUES(?,?,?) ON CONFLICT(provider,external_id) DO NOTHING`, id, p, x); err != nil {
			return err
		}
	}
	return nil
}
