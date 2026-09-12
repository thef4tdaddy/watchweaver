package workflow

import (
	"context"
	"database/sql"
	"errors"
	"github.com/thef4tdaddy/watchweaver/internal/persistence"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) (*Service, *sql.DB, int64) {
	t.Helper()
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "workflow.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	result, err := db.Exec(`INSERT INTO media_items(media_type,title) VALUES('movie','Fixture')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	return New(db), db, id
}
func TestAtomicRollbackAndRetry(t *testing.T) {
	s, db, id := fixture(t)
	r, err := db.Exec(`INSERT INTO prompt_tasks(media_id,task_type) VALUES(?,'rating_review')`, id)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := r.LastInsertId()
	rating := 8
	review := " "
	revision := int64(1)
	command := Command{Target: "task", ID: task, Action: "complete", Rating: &rating, Review: &review, Revision: &revision, MediaRevision: &revision}
	if _, err = s.Apply(context.Background(), command, "actor", "key"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	for _, table := range []string{"ratings", "bot_requests"} {
		var n int
		if err = db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s was not rolled back", table)
		}
	}
	review = "A review"
	first, err := s.Apply(context.Background(), command, "actor", "key")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Apply(context.Background(), command, "actor", "key")
	if err != nil || first != again {
		t.Fatalf("retry: %+v %v", again, err)
	}
	if _, err = s.Apply(context.Background(), command, "actor", "another-key"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestIgnoreSuppressesFuturePromptsAndUnignorePreservesHistory(t *testing.T) {
	s, db, id := fixture(t)
	ctx := context.Background()
	if _, err := s.Apply(ctx, Command{Target: "media", ID: id, Action: "ignore"}, "", ""); err != nil {
		t.Fatal(err)
	}
	r, err := db.Exec(`INSERT INTO prompt_tasks(media_id,task_type) VALUES(?,'rating')`, id)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := r.LastInsertId()
	var state string
	if err = db.QueryRow(`SELECT state FROM prompt_tasks WHERE id=?`, task).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "ignored" {
		t.Fatal(state)
	}
	if _, err = s.Apply(ctx, Command{Target: "media", ID: id, Action: "unignore"}, "", ""); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT state FROM prompt_tasks WHERE id=?`, task).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "ignored" {
		t.Fatal("unignore resurrected historical task")
	}
}
func TestMediaWritesRejectStaleStateAndSynchronizeDeletion(t *testing.T) {
	s, db, id := fixture(t)
	ctx := context.Background()
	rating := 7
	revision := int64(1)
	if _, err := s.Apply(ctx, Command{Target: "media", ID: id, Action: "rating", Rating: &rating, Revision: &revision}, "u", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(ctx, Command{Target: "media", ID: id, Action: "delete-rating", Revision: &revision}, "u", "b"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	revision = 2
	if _, err := s.Apply(ctx, Command{Target: "media", ID: id, Action: "delete-rating", Revision: &revision}, "u", "b"); err != nil {
		t.Fatal(err)
	}
	var deleted int
	if err := db.QueryRow(`SELECT pending_delete FROM rating_sync_state WHERE media_id=?`, id).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatal("deletion not queued for Trakt")
	}
}

func TestWebAndBotConcurrentEditsConflict(t *testing.T) {
	s, db, id := fixture(t)
	revision := int64(1)
	rating := 8
	review := "From web"
	results := make(chan error, 2)
	start := make(chan struct{})
	go func() {
		<-start
		_, err := s.Apply(context.Background(), Command{Target: "media", ID: id, Action: "edit", Rating: &rating, Review: &review, Revision: &revision}, "", "")
		results <- err
	}()
	go func() {
		<-start
		_, err := s.Apply(context.Background(), Command{Target: "media", ID: id, Action: "rating", Rating: &rating, Revision: &revision}, "discord", "interaction")
		results <- err
	}()
	close(start)
	a, b := <-results, <-results
	if !((a == nil && errors.Is(b, ErrConflict)) || (b == nil && errors.Is(a, ErrConflict))) {
		t.Fatalf("expected one commit and one conflict: %v, %v", a, b)
	}
	var current int64
	if err := db.QueryRow(`SELECT revision FROM media_items WHERE id=?`, id).Scan(&current); err != nil || current < 2 {
		t.Fatal(current, err)
	}
}

func TestReviewCRUDAndHalfStarRatingRange(t *testing.T) {
	s, db, id := fixture(t)
	ctx := context.Background()
	for rating := 1; rating <= 10; rating++ {
		if _, err := s.Apply(ctx, Command{Target: "media", ID: id, Action: "rating", Rating: &rating}, "", ""); err != nil {
			t.Fatal(err)
		}
		var saved int
		if err := db.QueryRow(`SELECT rating FROM ratings WHERE media_id=?`, id).Scan(&saved); err != nil || saved != rating {
			t.Fatal(saved, err)
		}
	}
	text := "  A useful review  "
	if _, err := s.Apply(ctx, Command{Target: "media", ID: id, Action: "review", Review: &text}, "", ""); err != nil {
		t.Fatal(err)
	}
	var saved string
	if err := db.QueryRow(`SELECT body FROM reviews WHERE media_id=?`, id).Scan(&saved); err != nil || saved != "A useful review" {
		t.Fatal(saved, err)
	}
	if _, err := s.Apply(ctx, Command{Target: "media", ID: id, Action: "delete-review"}, "", ""); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM reviews WHERE media_id=?`, id).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestInvalidWorkflowPayloadsLeaveNoChanges(t *testing.T) {
	s, db, id := fixture(t)
	rating := 11
	text := "review"
	until := "invalid"
	for _, command := range []Command{
		{Target: "media", ID: id, Action: "rating", Rating: &rating},
		{Target: "media", ID: id, Action: "rating"},
		{Target: "media", ID: id, Action: "review"},
		{Target: "media", ID: id, Action: "ignore", Rating: &rating},
		{Target: "media", ID: id, Action: "rating", ClearRating: true},
		{Target: "media", ID: id, Action: "edit", Rating: &rating, ClearRating: true},
		{Target: "media", ID: id, Action: "edit", Review: &text, ClearReview: true},
		{Target: "media", ID: id, Action: "edit"},
		{Target: "media", ID: id, Action: "rating", Until: &until},
		{Target: "media", ID: id, Action: "admin"},
		{Target: "settings", ID: id, Action: "edit"},
	} {
		if _, err := s.Apply(context.Background(), command, "actor", "invalid"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: %v", command, err)
		}
	}
	for _, table := range []string{"ratings", "reviews", "bot_requests", "prompt_ignored_media"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
	if _, err := s.Apply(context.Background(), Command{Target: "media", ID: 999999, Action: "ignore"}, "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
