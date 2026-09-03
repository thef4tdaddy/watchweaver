package jellyfin

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/thef4tdaddy/watchweaver/internal/persistence"
)

func testService(t *testing.T) *Service {
	t.Helper()
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "jellyfin.db")})
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = db.Close() })
	return NewService(db)
}

func movieEvent() Event {
	year := 2026
	return Event{EventID: "event-1", EventType: "played", ServerID: "server-a", ServerVersion: "10.10.7", PluginVersion: "0.1.0", OccurredAt: "2026-09-03T15:00:00Z", UserID: "user-1", Item: Item{ID: "jf-movie-1", Type: "movie", Title: "Movie", Year: &year}, ExternalIDs: map[string]string{"tmdb": "123", "imdb": "tt123"}}
}

func TestAcceptMovieIsIdempotent(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	first, err := svc.Accept(ctx, movieEvent())
	if err != nil { t.Fatal(err) }
	second, err := svc.Accept(ctx, movieEvent())
	if err != nil { t.Fatal(err) }
	if first.WatchEventID == 0 || second.WatchEventID != first.WatchEventID || !second.Duplicate { t.Fatalf("first=%+v second=%+v", first, second) }
	var watches, ingests int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM watch_events`).Scan(&watches); err != nil { t.Fatal(err) }
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM jellyfin_ingest_events`).Scan(&ingests); err != nil { t.Fatal(err) }
	if watches != 1 || ingests != 1 { t.Fatalf("watches=%d ingests=%d", watches, ingests) }
}

func TestSameIdentityDifferentPayloadConflicts(t *testing.T) {
	svc := testService(t)
	event := movieEvent()
	if _, err := svc.Accept(context.Background(), event); err != nil { t.Fatal(err) }
	event.Item.Title = "Changed"
	if _, err := svc.Accept(context.Background(), event); !errors.Is(err, ErrEventConflict) { t.Fatalf("err=%v", err) }
}

func TestEpisodeCreatesHierarchyAndDistinctRewatch(t *testing.T) {
	svc := testService(t)
	season, episode := 2, 4
	event := Event{EventID: "ep-1", EventType: "played", ServerID: "server-a", ServerVersion: "10.10.7", PluginVersion: "0.1.0", OccurredAt: "2026-09-03T15:00:00Z", Item: Item{ID: "jf-episode-1", Type: "episode", Title: "Episode", ShowTitle: "Show", SeasonNumber: &season, EpisodeNumber: &episode}, ExternalIDs: map[string]string{"tvdb": "456"}}
	if _, err := svc.Accept(context.Background(), event); err != nil { t.Fatal(err) }
	event.EventID = "ep-2"
	event.OccurredAt = "2026-09-04T15:00:00Z"
	if _, err := svc.Accept(context.Background(), event); err != nil { t.Fatal(err) }
	var shows, seasons, episodes, watches int
	for query, target := range map[string]*int{`SELECT COUNT(*) FROM media_items WHERE media_type='show'`: &shows, `SELECT COUNT(*) FROM media_items WHERE media_type='season'`: &seasons, `SELECT COUNT(*) FROM media_items WHERE media_type='episode'`: &episodes, `SELECT COUNT(*) FROM watch_events`: &watches} {
		if err := svc.db.QueryRow(query).Scan(target); err != nil { t.Fatal(err) }
	}
	if shows != 1 || seasons != 1 || episodes != 1 || watches != 2 { t.Fatalf("show=%d season=%d episode=%d watches=%d", shows, seasons, episodes, watches) }
}

func TestInvalidEventRejectedAndStatusUpdated(t *testing.T) {
	svc := testService(t)
	if _, err := svc.Accept(context.Background(), Event{}); !errors.Is(err, ErrInvalidEvent) { t.Fatalf("err=%v", err) }
	status, err := svc.Status(context.Background(), true)
	if err != nil { t.Fatal(err) }
	if status.LastRejectionCode == nil || *status.LastRejectionCode != "invalid_event" { t.Fatalf("status=%+v", status) }
}
