package server

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

func TestHistoryFiltersCompletedSeasonsAndPairedLocation(t *testing.T) {
	f := newAPIFixture(t, nil)
	show := mustExecID(t, f.db, `INSERT INTO media_items(media_type,title) VALUES('show','Show')`)
	season := mustExecID(t, f.db, `INSERT INTO media_items(media_type,title,parent_id,season_number) VALUES('season','Show Season 1',?,1)`, show)
	episode := mustExecID(t, f.db, `INSERT INTO media_items(media_type,title,parent_id,episode_number) VALUES('episode','Finale',?,8)`, season)
	watch := mustExecID(t, f.db, `INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at) VALUES(?,'trakt','finale','2026-10-01T00:00:00Z','2026-10-01T00:00:00Z')`, episode)
	mustExecID(t, f.db, `INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at,source_instance_name,duplicate_of) VALUES(?,'jellyfin','jf-finale','2026-10-01T00:00:30Z','2026-10-01T00:00:30Z','Living room',?)`, episode, watch)
	// A caught-up episode has no completion entry until explicitly confirmed.
	rr := f.request(http.MethodGet, "/api/history?type=season", "")
	if rr.Code != http.StatusOK || decodeMap(t, rr)["total"] != float64(0) {
		t.Fatalf("unconfirmed completion: %s", rr.Body.String())
	}
	if _, err := f.db.Exec(`INSERT INTO episode_metadata(media_id,finale_type,provider) VALUES(?,'season','trakt')`, episode); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		filter string
		total  int
	}{
		{"", 4}, {"type=movie", 2}, {"type=tv", 2}, {"type=season", 1},
		{"location=" + url.QueryEscape("Living room"), 2},
		{"type=movie&location=" + url.QueryEscape("Living room"), 0},
		{"type=season&location=" + url.QueryEscape("Living room"), 1},
	} {
		rr := f.request(http.MethodGet, "/api/history?"+tc.filter, "")
		if rr.Code != http.StatusOK || decodeMap(t, rr)["total"] != float64(tc.total) {
			t.Fatalf("%s: %d %s", tc.filter, rr.Code, rr.Body.String())
		}
	}
	rr = f.request(http.MethodGet, "/api/history?type=season&per_page=1", "")
	item := decodeMap(t, rr)["items"].([]any)[0].(map[string]any)
	media := item["media"].(map[string]any)
	if item["kind"] != "season_completed" || item["location"] != "Living room" || media["id"] != float64(season) || media["episode_number"] != nil {
		t.Fatalf("completion: %#v", item)
	}
	rr = f.request(http.MethodGet, "/api/history?type=tv&per_page=1&page=2", "")
	if decodeMap(t, rr)["total_pages"] != float64(2) {
		t.Fatalf("pagination: %s", rr.Body.String())
	}
	rr = f.request(http.MethodGet, "/api/history?type=unsupported", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatal(fmt.Sprintf("invalid filter status=%d", rr.Code))
	}
}
