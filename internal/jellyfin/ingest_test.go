package jellyfin

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/thef4tdaddy/watchweaver/internal/persistence"
)

func testService(t *testing.T) *Service {
	t.Helper()
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "jellyfin.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewService(db)
}

func movieEvent() Event {
	year := 2026
	return Event{SchemaVersion: 1, EventID: "event-1", EventType: "played", OccurredAt: "2026-09-03T15:00:00Z", Server: Server{ID: "server-a", Version: "10.11.0"}, Plugin: Plugin{Version: "0.1.0", TargetABI: "10.11.0.0"}, User: User{ID: "user-1"}, Item: Item{ID: "jf-movie-1", Type: "movie", Title: "Movie", Year: &year, ProviderIDs: map[string]string{"tmdb": "123", "imdb": "tt123"}}, Playback: Playback{Played: true}}
}

func TestAcceptMovieIsIdempotent(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	first, err := svc.Accept(ctx, movieEvent())
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Accept(ctx, movieEvent())
	if err != nil {
		t.Fatal(err)
	}
	if first.WatchEventID == 0 || second.WatchEventID != first.WatchEventID || !second.Duplicate {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	var watches, ingests int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM watch_events`).Scan(&watches); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM jellyfin_ingest_events`).Scan(&ingests); err != nil {
		t.Fatal(err)
	}
	if watches != 1 || ingests != 1 {
		t.Fatalf("watches=%d ingests=%d", watches, ingests)
	}
}

func TestSameIdentityDifferentPayloadConflicts(t *testing.T) {
	svc := testService(t)
	event := movieEvent()
	if _, err := svc.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	event.Item.ID = "different-item"
	if _, err := svc.Accept(context.Background(), event); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("err=%v", err)
	}
}

func TestEpisodeCreatesHierarchyAndDistinctRewatch(t *testing.T) {
	svc := testService(t)
	season, episode := 2, 4
	event := Event{SchemaVersion: 1, EventID: "ep-1", EventType: "played", OccurredAt: "2026-09-03T15:00:00Z", Server: Server{ID: "server-a", Version: "10.11.0"}, Plugin: Plugin{Version: "0.1.0"}, User: User{ID: "user-1"}, Item: Item{ID: "jf-episode-1", Type: "episode", Title: "Episode", SeriesTitle: "Show", SeriesID: "jf-series-1", SeasonID: "jf-season-2", SeasonNumber: &season, EpisodeNumber: &episode, ProviderIDs: map[string]string{"tvdb": "456"}}, Playback: Playback{Played: true}}
	if _, err := svc.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	event.EventID = "ep-2"
	event.OccurredAt = "2026-09-04T15:00:00Z"
	if _, err := svc.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	var shows, seasons, episodes, watches int
	for query, target := range map[string]*int{`SELECT COUNT(*) FROM media_items WHERE media_type='show'`: &shows, `SELECT COUNT(*) FROM media_items WHERE media_type='season'`: &seasons, `SELECT COUNT(*) FROM media_items WHERE media_type='episode'`: &episodes, `SELECT COUNT(*) FROM watch_events`: &watches} {
		if err := svc.db.QueryRow(query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if shows != 1 || seasons != 1 || episodes != 1 || watches != 2 {
		t.Fatalf("show=%d season=%d episode=%d watches=%d", shows, seasons, episodes, watches)
	}
}

func TestProviderlessEpisodesUseExplicitSeriesAndSeasonIdentity(t *testing.T) {
	svc := testService(t)
	season := 1
	firstEpisode := 1
	event := Event{SchemaVersion: 1, EventID: "ep-1", EventType: "played", OccurredAt: "2026-09-03T15:00:00Z", Server: Server{ID: "server-a", Version: "10.11.0"}, Plugin: Plugin{Version: "0.1.0"}, User: User{ID: "user-1"}, Item: Item{ID: "episode-1", Type: "episode", Title: "One", SeriesTitle: "Providerless", SeriesID: "series-a", SeasonID: "season-a-1", SeasonNumber: &season, EpisodeNumber: &firstEpisode}, Playback: Playback{Played: true}}
	if _, err := svc.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	secondEpisode := 2
	event.EventID, event.Item.ID, event.Item.Title, event.Item.EpisodeNumber = "ep-2", "episode-2", "Two", &secondEpisode
	if _, err := svc.Accept(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	var shows, seasons, episodes int
	for query, target := range map[string]*int{
		`SELECT COUNT(*) FROM media_items WHERE media_type='show'`:    &shows,
		`SELECT COUNT(*) FROM media_items WHERE media_type='season'`:  &seasons,
		`SELECT COUNT(*) FROM media_items WHERE media_type='episode'`: &episodes,
	} {
		if err := svc.db.QueryRow(query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if shows != 1 || seasons != 1 || episodes != 2 {
		t.Fatalf("shows=%d seasons=%d episodes=%d", shows, seasons, episodes)
	}
}

func TestConcurrentDuplicateDeliveryReturnsSuccess(t *testing.T) {
	svc := testService(t)
	start := make(chan struct{})
	results := make(chan Result, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := svc.Accept(context.Background(), movieEvent())
			results <- result
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	duplicates := 0
	for result := range results {
		if result.Duplicate {
			duplicates++
		}
	}
	if duplicates != 1 {
		t.Fatalf("duplicates=%d want 1", duplicates)
	}
	var watches int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM watch_events WHERE source='jellyfin'`).Scan(&watches); err != nil || watches != 1 {
		t.Fatalf("watches=%d err=%v", watches, err)
	}
}

func TestAcceptedEventRemainsIdempotentAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewService(db).Accept(context.Background(), movieEvent())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = persistence.OpenAndMigrate(persistence.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	second, err := NewService(db).Accept(context.Background(), movieEvent())
	if err != nil {
		t.Fatal(err)
	}
	if !second.Duplicate || second.WatchEventID != first.WatchEventID {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	var watches int
	if err := db.QueryRow(`SELECT COUNT(*) FROM watch_events WHERE source='jellyfin'`).Scan(&watches); err != nil || watches != 1 {
		t.Fatalf("watches=%d err=%v", watches, err)
	}
}

func TestEpisodePromptHandoffUsesSettledTVRules(t *testing.T) {
	tests := []struct {
		name                                    string
		season, episode, total, watched, future int
		latest                                  int
		episodeType                             string
		wantType                                string
	}{
		{name: "backlog is silent", season: 1, episode: 2, total: 10, watched: 2, latest: 10},
		{name: "caught up prompts for episode rating", season: 1, episode: 4, total: 4, watched: 4, future: 1, latest: 4, wantType: "episode"},
		{name: "completed season prompts for season rating", season: 1, episode: 10, total: 10, watched: 10, latest: 10, wantType: "season"},
		{name: "out of order is silent", season: 1, episode: 3, total: 4, watched: 4, future: 1, latest: 4},
		{name: "special is silent", season: 0, episode: 1, total: 1, watched: 1, latest: 1},
		{name: "explicit finale prompts for season rating", season: 2, episode: 8, episodeType: "season_finale", wantType: "season"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := testService(t)
			event := Event{SchemaVersion: 1, EventID: "event-1", EventType: "played", OccurredAt: "2026-09-03T15:00:00Z", Server: Server{ID: "server-a", Version: "10.11.0"}, Plugin: Plugin{Version: "0.1.0"}, User: User{ID: "user-1"}, Item: Item{ID: "episode-a", Type: "episode", Title: "Episode", SeriesTitle: "Show", SeriesID: "series-a", SeasonID: "season-a", SeasonNumber: &test.season, EpisodeNumber: &test.episode, EpisodeType: test.episodeType, SeasonEpisodeCount: &test.total, SeasonWatchedEpisodeCount: &test.watched, SeasonFutureEpisodeCount: &test.future, LatestReleasedEpisodeNumber: &test.latest}, Playback: Playback{Played: true}}
			if _, err := svc.Accept(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			var count int
			var mediaType string
			err := svc.db.QueryRow(`SELECT COUNT(*),COALESCE(MAX(m.media_type),'') FROM prompt_tasks p JOIN media_items m ON m.id=p.media_id`).Scan(&count, &mediaType)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantType == "" && count != 0 || test.wantType != "" && (count != 1 || mediaType != test.wantType) {
				t.Fatalf("prompts=%d media_type=%q want=%q", count, mediaType, test.wantType)
			}
		})
	}
}

func TestInvalidEventRejectedAndStatusUpdated(t *testing.T) {
	svc := testService(t)
	if _, err := svc.Accept(context.Background(), Event{SchemaVersion: 1}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("err=%v", err)
	}
	status, err := svc.Status(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if status.LastRejectionCode == nil || *status.LastRejectionCode != "missing_field" {
		t.Fatalf("status=%+v", status)
	}
}
