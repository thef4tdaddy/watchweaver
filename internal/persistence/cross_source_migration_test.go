package persistence

import (
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestCrossSourceMigrationPreservesReportsAndPairsExistingWatches(t *testing.T) {
	old := fstest.MapFS{}
	if err := fs.WalkDir(embeddedMigrations, "migrations", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path == "migrations/000017_cross_source_watches.up.sql" {
			return nil
		}
		raw, err := fs.ReadFile(embeddedMigrations, path)
		if err == nil {
			old[path] = &fstest.MapFile{Data: raw}
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := OpenAndMigrate(Options{Path: path, MigrationsFS: old})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO media_items(id,media_type,title) VALUES(1,'movie','Movie'),(2,'movie','Different movie');
 INSERT INTO watch_events(id,media_id,source,source_event_id,watched_at_utc,source_watched_at) VALUES
 (1,1,'jellyfin','jf-1','2026-10-01T00:00:00Z','raw jf timestamp'),
 (2,1,'trakt','tr-1','2026-10-01T00:00:30Z','raw trakt timestamp'),
 (3,1,'trakt','tr-2','2026-10-02T00:00:00Z','rewatch'),
 (4,1,'jellyfin','jf-2','2026-10-02T00:00:15Z','rewatch'),
 (5,1,'jellyfin','jf-3','2026-10-02T00:00:20Z','separate report'),
 (6,2,'trakt','tr-other','2026-10-01T00:00:30Z','different movie');`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = OpenAndMigrate(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var raw, canonical, pairs int
	if err := db.QueryRow(`SELECT COUNT(*),COUNT(CASE WHEN duplicate_of IS NULL THEN 1 END),COUNT(duplicate_of) FROM watch_events`).Scan(&raw, &canonical, &pairs); err != nil {
		t.Fatal(err)
	}
	if raw != 6 || canonical != 4 || pairs != 2 {
		t.Fatalf("raw=%d canonical=%d pairs=%d", raw, canonical, pairs)
	}
	var original string
	if err := db.QueryRow(`SELECT source_watched_at FROM watch_events WHERE id=2`).Scan(&original); err != nil || original != "raw trakt timestamp" {
		t.Fatalf("original=%q err=%v", original, err)
	}
	var parent int
	if err := db.QueryRow(`SELECT duplicate_of FROM watch_events WHERE id=2`).Scan(&parent); err != nil || parent != 1 {
		t.Fatalf("parent=%d err=%v", parent, err)
	}
}
