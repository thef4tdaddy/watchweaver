package watches

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/thef4tdaddy/watchweaver/internal/persistence"
)

func TestPairIsOneToOneAndConservative(t *testing.T) {
	for _, tc := range []struct {
		name, source, stamp string
		deleted, paired     bool
	}{
		{name: "time zone match", source: "trakt", stamp: "2026-10-01T01:00:00+01:00", paired: true},
		{name: "outside window", source: "trakt", stamp: "2026-10-01T00:02:01Z"},
		{name: "same source", source: "jellyfin", stamp: "2026-10-01T00:00:00Z"},
		{name: "deleted report", source: "trakt", stamp: "2026-10-01T00:00:00Z", deleted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "test.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			res, err := db.Exec(`INSERT INTO media_items(media_type,title) VALUES('movie','Movie')`)
			if err != nil {
				t.Fatal(err)
			}
			media, _ := res.LastInsertId()
			var deleted any
			if tc.deleted {
				deleted = "2026-10-02T00:00:00Z"
			}
			if _, err := db.Exec(`INSERT INTO watch_events(media_id,source,watched_at_utc,source_watched_at,deleted_at) VALUES(?,?,'2026-10-01T00:00:00Z','stamp',?)`, media, tc.source, deleted); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				tx, err := db.BeginTx(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				res, err := tx.Exec(`INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at) VALUES(?,'jellyfin',?,?,?)`, media, fmt.Sprint(i), tc.stamp, tc.stamp)
				if err != nil {
					t.Fatal(err)
				}
				id, _ := res.LastInsertId()
				paired, err := Pair(context.Background(), tx, id)
				if err != nil || paired != (tc.paired && i == 0) {
					t.Fatalf("paired=%v err=%v", paired, err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
