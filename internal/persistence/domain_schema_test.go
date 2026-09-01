package persistence

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func openDomainTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := OpenAndMigrate(Options{Path: filepath.Join(t.TempDir(), "domain.db")})
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertMedia(t *testing.T, db *sql.DB, mediaType, title string, parent any, season, episode any) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO media_items(media_type,title,parent_id,season_number,episode_number) VALUES(?,?,?,?,?)`, mediaType, title, parent, season, episode)
	if err != nil {
		t.Fatalf("insert %s: %v", mediaType, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDomainSchemaMediaHierarchy(t *testing.T) {
	db := openDomainTestDB(t)
	movie := insertMedia(t, db, "movie", "Movie", nil, nil, nil)
	show := insertMedia(t, db, "show", "Show", nil, nil, nil)
	season := insertMedia(t, db, "season", "Season 1", show, 1, nil)
	episode := insertMedia(t, db, "episode", "Episode 1", season, nil, 1)
	if movie == show || season == episode {
		t.Fatal("expected distinct internal IDs")
	}

	bad := []struct {
		typ                     string
		parent, season, episode any
	}{
		{"season", movie, 1, nil},
		{"episode", show, nil, 1},
		{"movie", show, nil, nil},
		{"show", nil, 1, nil},
	}
	for _, tc := range bad {
		if _, err := db.Exec(`INSERT INTO media_items(media_type,title,parent_id,season_number,episode_number) VALUES(?,?,?,?,?)`, tc.typ, "bad", tc.parent, tc.season, tc.episode); err == nil {
			t.Fatalf("expected invalid %s hierarchy to fail", tc.typ)
		}
	}
	if _, err := db.Exec(`INSERT INTO media_items(media_type,title,parent_id,season_number) VALUES('season','duplicate',?,1)`, show); err == nil {
		t.Fatal("expected duplicate season number to fail")
	}
	if _, err := db.Exec(`INSERT INTO media_items(media_type,title,parent_id,episode_number) VALUES('episode','duplicate',?,1)`, season); err == nil {
		t.Fatal("expected duplicate episode number to fail")
	}
}

func TestExternalIDsAreNormalizedAndUnique(t *testing.T) {
	db := openDomainTestDB(t)
	one := insertMedia(t, db, "movie", "One", nil, nil, nil)
	two := insertMedia(t, db, "movie", "Two", nil, nil, nil)
	if _, err := db.Exec(`INSERT INTO external_ids(media_id,provider,external_id) VALUES(?,?,?),(?,?,?)`, one, "trakt", "10", one, "imdb", "tt10"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO external_ids(media_id,provider,external_id) VALUES(?,?,?)`, two, "trakt", "10"); err == nil {
		t.Fatal("expected provider/external ID uniqueness")
	}
	if _, err := db.Exec(`INSERT INTO external_ids(media_id,provider,external_id) VALUES(?,?,?)`, one, "trakt", "11"); err == nil {
		t.Fatal("expected one external ID per provider/media")
	}
}

func TestWatchEventsPreserveRewatchesAndSourceIdentity(t *testing.T) {
	db := openDomainTestDB(t)
	movie := insertMedia(t, db, "movie", "Movie", nil, nil, nil)
	stamp := "2026-08-31T12:00:00Z"
	for _, id := range []string{"100", "101"} {
		if _, err := db.Exec(`INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at) VALUES(?,?,?,?,?)`, movie, "trakt", id, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at) VALUES(?,?,?,?,?)`, movie, "trakt", "100", stamp, stamp); err == nil {
		t.Fatal("expected duplicate source event identity to fail")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM watch_events WHERE media_id=? AND watched_at_utc=?`, movie, stamp).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected two same-time rewatches, got %d", count)
	}
	if _, err := db.Exec(`DELETE FROM media_items WHERE id=?`, movie); err == nil {
		t.Fatal("expected watch history to protect media deletion")
	}
}

func TestRatingsAreSingleCurrentIndependentOfWatchEvents(t *testing.T) {
	db := openDomainTestDB(t)
	movie := insertMedia(t, db, "movie", "Movie", nil, nil, nil)
	show := insertMedia(t, db, "show", "Show", nil, nil, nil)
	season := insertMedia(t, db, "season", "Season", show, 1, nil)
	episode := insertMedia(t, db, "episode", "Episode", season, nil, 1)
	for _, id := range []int64{movie, season, episode} {
		if _, err := db.Exec(`INSERT INTO ratings(media_id,rating,source) VALUES(?,?,?)`, id, 8, "local"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO ratings(media_id,rating,source) VALUES(?,?,?)`, id, 9, "local"); err == nil {
			t.Fatal("expected exactly one current rating")
		}
	}
	if _, err := db.Exec(`INSERT INTO ratings(media_id,rating,source) VALUES(?,?,?)`, show, 8, "local"); err == nil {
		t.Fatal("show must not be a rating target")
	}
	if _, err := db.Exec(`UPDATE ratings SET rating=11 WHERE media_id=?`, movie); err == nil {
		t.Fatal("expected rating range enforcement")
	}

	stamp := "2026-08-31T12:00:00Z"
	if _, err := db.Exec(`INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at) VALUES(?,?,?,?,?)`, movie, "trakt", "1", stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at) VALUES(?,?,?,?,?)`, movie, "trakt", "2", stamp, stamp); err != nil {
		t.Fatal(err)
	}
	var ratings int
	if err := db.QueryRow(`SELECT count(*) FROM ratings WHERE media_id=?`, movie).Scan(&ratings); err != nil {
		t.Fatal(err)
	}
	if ratings != 1 {
		t.Fatalf("rewatches changed current rating cardinality: %d", ratings)
	}
}

func TestWorkflowAndGenericStateConstraints(t *testing.T) {
	db := openDomainTestDB(t)
	movie := insertMedia(t, db, "movie", "Movie", nil, nil, nil)
	show := insertMedia(t, db, "show", "Show", nil, nil, nil)
	stamp := "2026-08-31T12:00:00Z"
	res, err := db.Exec(`INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at) VALUES(?,?,?,?,?)`, movie, "trakt", "1", stamp, stamp)
	if err != nil {
		t.Fatal(err)
	}
	event, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO reviews(media_id,body) VALUES(?,?)`, movie, "Good movie"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO reviews(media_id,body) VALUES(?,?)`, show, "Nope"); err == nil {
		t.Fatal("show must not be review target")
	}
	if _, err := db.Exec(`INSERT INTO prompt_tasks(media_id,watch_event_id,task_type) VALUES(?,?,?)`, movie, event, "rating_review"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO prompt_tasks(media_id,task_type,state) VALUES(?,?,?)`, movie, "rating", "snoozed"); err == nil {
		t.Fatal("snoozed task requires timestamp")
	}
	if _, err := db.Exec(`INSERT INTO prompt_tasks(media_id,task_type,state,snoozed_until) VALUES(?,?,?,?)`, movie, "rating", "snoozed", "2026-09-01T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO prompt_tasks(media_id,task_type,state) VALUES(?,?,?)`, movie, "rating", "bogus"); err == nil {
		t.Fatal("invalid task state accepted")
	}
	if _, err := db.Exec(`INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt','checkpoint','123')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO app_settings(setting_key,setting_value) VALUES('timezone','America/Chicago')`); err != nil {
		t.Fatal(err)
	}
}
