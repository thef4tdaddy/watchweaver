package trakt

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/thef4tdaddy/watchweaver/internal/jellyfin"
)

func TestCrossSourceViewingIdentityAndReplay(t *testing.T) {
	for _, mediaType := range []string{"movie", "episode"} {
		for _, jellyfinFirst := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/jellyfinFirst=%v", mediaType, jellyfinFirst), func(t *testing.T) {
				db := testHistoryDB(t)
				ctx := context.Background()
				importer := NewHistoryImporter(db, "", nil, "")
				service := jellyfin.NewService(db)
				season, episode := 1, 2
				event := jellyfin.Event{SchemaVersion: 1, EventID: "jf-1", EventType: "played", OccurredAt: "2026-09-03T15:00:00.123Z", Server: jellyfin.Server{ID: "server", Version: "10.11.0"}, Plugin: jellyfin.Plugin{Version: "0.1.0"}, User: jellyfin.User{ID: "user"}, Item: jellyfin.Item{ID: "item", Type: mediaType, Title: "Title", ProviderIDs: map[string]string{"tmdb": "123"}}, Playback: jellyfin.Playback{Played: true}}
				raw := `{"id":101,"watched_at":"2026-09-03T15:00:30Z","movie":{"title":"Title","ids":{"trakt":42,"tmdb":123}}}`
				if mediaType == "episode" {
					event.Item.SeasonNumber = &season
					event.Item.EpisodeNumber = &episode
					event.Item.SeriesTitle = "Show"
					event.Item.SeriesID = "show"
					event.Item.SeasonID = "season"
					event.Item.SeriesProviderIDs = map[string]string{"tvdb": "456"}
					raw = `{"id":101,"watched_at":"2026-09-03T15:00:30Z","episode":{"season":1,"number":2,"title":"Title","ids":{"trakt":42,"tmdb":123}},"show":{"title":"Show","ids":{"trakt":43,"tvdb":456}}}`
				}
				var item historyItem
				if err := json.Unmarshal([]byte(raw), &item); err != nil {
					t.Fatal(err)
				}
				jf := func() {
					if _, err := service.Accept(ctx, event); err != nil {
						t.Fatal(err)
					}
				}
				tr := func() {
					if _, _, err := importer.persistItem(ctx, item, false); err != nil {
						t.Fatal(err)
					}
				}
				if jellyfinFirst {
					jf()
					tr()
				} else {
					tr()
					jf()
				}
				jf()
				tr() // replay must keep both source identities idempotent
				var reports, viewings, media int
				if err := db.QueryRow(`SELECT COUNT(*),COUNT(CASE WHEN duplicate_of IS NULL THEN 1 END),COUNT(DISTINCT media_id) FROM watch_events`).Scan(&reports, &viewings, &media); err != nil {
					t.Fatal(err)
				}
				if reports != 2 || viewings != 1 || media != 1 {
					t.Fatalf("reports=%d viewings=%d media=%d", reports, viewings, media)
				}
				var traktID string
				if err := db.QueryRow(`SELECT external_id FROM external_ids WHERE provider='trakt' AND media_id=(SELECT media_id FROM watch_events LIMIT 1)`).Scan(&traktID); err != nil || traktID != "42" {
					t.Fatalf("Trakt ID=%q err=%v", traktID, err)
				}
				// A real later rewatch gets its own pair, even if reports arrive reversed.
				event.EventID = "jf-2"
				event.OccurredAt = "2026-09-04T15:00:00Z"
				item.ID = 102
				item.WatchedAt = "2026-09-04T15:00:30Z"
				tr()
				jf()
				if err := db.QueryRow(`SELECT COUNT(*) FROM watch_events WHERE duplicate_of IS NULL`).Scan(&viewings); err != nil || viewings != 2 {
					t.Fatalf("rewatch count=%d err=%v", viewings, err)
				}
				var tasks int
				if err := db.QueryRow(`SELECT COUNT(*) FROM prompt_tasks`).Scan(&tasks); err != nil {
					t.Fatal(err)
				}
				if mediaType == "movie" && jellyfinFirst && tasks != 1 {
					t.Fatalf("duplicate movie prompts=%d", tasks)
				}
			})
		}
	}
}
