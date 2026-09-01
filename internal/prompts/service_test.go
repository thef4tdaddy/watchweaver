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
	if _, err := service.Apply(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(context.Background(), batch); err != nil {
		t.Fatal(err)
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

	if _, err := NewService(db).Apply(context.Background(), Batch{NewMovieWatches: []int64{movieID}}); err != nil {
		t.Fatal(err)
	}

	var pending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM prompt_tasks WHERE media_id=? AND state='pending'`, movieID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("pending=%d, want 1", pending)
	}
}
