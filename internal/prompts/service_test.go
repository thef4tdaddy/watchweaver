package prompts

import (
	"context"
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
