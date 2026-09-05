package prompts

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/thef4tdaddy/watchweaver/internal/persistence"
)

func TestServiceApplyPersistsTaskWithoutRatingSnapshot(t *testing.T) {
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "prompts.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	result, err := db.Exec(`INSERT INTO media_items(media_type,title,year) VALUES('movie','Example',2026)`)
	if err != nil {
		t.Fatal(err)
	}
	movieID, _ := result.LastInsertId()
	if _, err := db.Exec(`INSERT INTO ratings(media_id,rating,source) VALUES(?,8,'trakt')`, movieID); err != nil {
		t.Fatal(err)
	}

	service := NewService(db)
	batch := Batch{NewMovieWatches: []int64{movieID}}
	first, err := service.Apply(context.Background(), batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].MediaID != movieID {
		t.Fatalf("first Apply() = %#v, want one created movie decision", first)
	}
	second, err := service.Apply(context.Background(), batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("second Apply() = %#v, want no newly created decisions", second)
	}

	var tasks int
	if err := db.QueryRow(`SELECT COUNT(*) FROM prompt_tasks WHERE media_id=? AND state='pending'`, movieID).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if tasks != 1 {
		t.Fatalf("pending tasks=%d, want 1", tasks)
	}

	var ratings int
	var value int
	if err := db.QueryRow(`SELECT COUNT(*), MAX(rating) FROM ratings WHERE media_id=?`, movieID).Scan(&ratings, &value); err != nil {
		t.Fatal(err)
	}
	if ratings != 1 || value != 8 {
		t.Fatalf("ratings count=%d value=%d, want one current rating of 8", ratings, value)
	}
}

func TestServiceApplyDeduplicatesRepeatedDecisionsWithinBatch(t *testing.T) {
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "duplicate-batch.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	result, err := db.Exec(`INSERT INTO media_items(media_type,title,year) VALUES('movie','Duplicate',2026)`)
	if err != nil {
		t.Fatal(err)
	}
	movieID, _ := result.LastInsertId()

	created, err := NewService(db).Apply(context.Background(), Batch{NewMovieWatches: []int64{movieID, movieID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || created[0].MediaID != movieID {
		t.Fatalf("Apply() = %#v, want one newly persisted decision", created)
	}
}

func TestServiceApplyAllowsNewMoviePromptAfterCompletedPriorTask(t *testing.T) {
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "rewatch.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	result, err := db.Exec(`INSERT INTO media_items(media_type,title,year) VALUES('movie','Rewatch',2026)`)
	if err != nil {
		t.Fatal(err)
	}
	movieID, _ := result.LastInsertId()
	if _, err := db.Exec(`INSERT INTO prompt_tasks(media_id,task_type,state) VALUES(?,'rating','completed')`, movieID); err != nil {
		t.Fatal(err)
	}

	created, err := NewService(db).Apply(context.Background(), Batch{NewMovieWatches: []int64{movieID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || created[0].MediaID != movieID {
		t.Fatalf("Apply() = %#v, want one new rewatch decision", created)
	}

	var pending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM prompt_tasks WHERE media_id=? AND state='pending'`, movieID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("pending=%d, want 1", pending)
	}
}

func TestServiceApplyHonorsPromptPreferences(t *testing.T) {
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "preferences.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result, err := db.Exec(`INSERT INTO media_items(media_type,title,year) VALUES('movie','Disabled',2026)`)
	if err != nil {
		t.Fatal(err)
	}
	movieID, _ := result.LastInsertId()
	if _, err := db.Exec(`INSERT INTO app_settings(setting_key,setting_value) VALUES('prompt_movies_enabled','false'),('prompt_tv_enabled','true')`); err != nil {
		t.Fatal(err)
	}
	created, err := NewService(db).Apply(context.Background(), Batch{NewMovieWatches: []int64{movieID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 0 {
		t.Fatalf("disabled movie prompts created=%v", created)
	}
}

func TestIndependentRatingAndReviewPreferences(t *testing.T) {
	for _, test := range []struct {
		name                  string
		ratings, reviews      bool
		movieType, seasonType string
		episodeCount          int
	}{
		{"both", true, true, "rating_review", "rating_review", 1},
		{"rating only", true, false, "rating", "rating", 1},
		{"review only", false, true, "review", "review", 0},
		{"neither", false, false, "", "", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "preferences.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			movie := mustMedia(t, db, `INSERT INTO media_items(media_type,title) VALUES('movie','Movie')`)
			show := mustMedia(t, db, `INSERT INTO media_items(media_type,title) VALUES('show','Show')`)
			season := mustMedia(t, db, `INSERT INTO media_items(media_type,title,parent_id,season_number) VALUES('season','Season',?,1)`, show)
			episode := mustMedia(t, db, `INSERT INTO media_items(media_type,title,parent_id,episode_number) VALUES('episode','Episode',?,1)`, season)
			_, err = db.Exec(`INSERT INTO app_settings(setting_key,setting_value) VALUES('prompt_ratings_enabled',?),('prompt_reviews_enabled',?) ON CONFLICT(setting_key) DO UPDATE SET setting_value=excluded.setting_value`, fmt.Sprint(test.ratings), fmt.Sprint(test.reviews))
			if err != nil {
				t.Fatal(err)
			}
			_, err = NewService(db).Apply(context.Background(), Batch{NewMovieWatches: []int64{movie}, CompletedSeasonIDs: []int64{season}, NewEpisodeIDs: []int64{episode}, Seasons: []SeasonState{{SeasonID: season + 100, ShowID: show, InventoryKnown: true, Episodes: []Episode{{ID: episode, Number: 1, Released: true, Watched: true, Normal: true}, {ID: -1, Number: 2, Released: false, Normal: true}}}}})
			if err != nil {
				t.Fatal(err)
			}
			assertTaskType(t, db, movie, test.movieType)
			assertTaskType(t, db, season, test.seasonType)
			var count int
			_ = db.QueryRow(`SELECT COUNT(*) FROM prompt_tasks WHERE media_id=?`, episode).Scan(&count)
			if count != test.episodeCount {
				t.Fatalf("episode prompts=%d want=%d", count, test.episodeCount)
			}
		})
	}
}

func mustMedia(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	result, err := db.Exec(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	return id
}
func assertTaskType(t *testing.T, db *sql.DB, id int64, want string) {
	t.Helper()
	var got string
	err := db.QueryRow(`SELECT task_type FROM prompt_tasks WHERE media_id=?`, id).Scan(&got)
	if want == "" {
		if err != sql.ErrNoRows {
			t.Fatalf("media %d task=%q err=%v", id, got, err)
		}
		return
	}
	if err != nil || got != want {
		t.Fatalf("media %d task=%q err=%v want=%q", id, got, err, want)
	}
}
