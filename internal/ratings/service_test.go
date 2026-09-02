package ratings

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/persistence"
)

func testService(t *testing.T) (*Service, int64, int64) {
	t.Helper()
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "ratings.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	movie, err := db.Exec(`INSERT INTO media_items(media_type,title,year) VALUES('movie','Movie',2026)`)
	if err != nil {
		t.Fatal(err)
	}
	movieID, _ := movie.LastInsertId()
	show, err := db.Exec(`INSERT INTO media_items(media_type,title,year) VALUES('show','Show',2026)`)
	if err != nil {
		t.Fatal(err)
	}
	showID, _ := show.LastInsertId()
	return NewService(db), movieID, showID
}

func TestStarConversionExact(t *testing.T) {
	for value := 1; value <= 10; value++ {
		stars, err := Stars(value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := FromStars(stars)
		if err != nil || got != value {
			t.Fatalf("round trip %d => %v => %d, err=%v", value, stars, got, err)
		}
	}
	for _, stars := range []float64{0, 0.25, 5.5} {
		if _, err := FromStars(stars); !errors.Is(err, ErrInvalidRating) {
			t.Fatalf("FromStars(%v) error = %v", stars, err)
		}
	}
}

func TestCurrentRatingUpsertAndPendingSync(t *testing.T) {
	svc, movieID, _ := testService(t)
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	svc.SetNow(func() time.Time { return now })
	ctx := context.Background()
	if err := svc.SetLocal(ctx, movieID, 7); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err := svc.SetLocal(ctx, movieID, 9); err != nil {
		t.Fatal(err)
	}
	r, err := svc.Get(ctx, movieID)
	if err != nil || r == nil || r.Value != 9 {
		t.Fatalf("rating = %#v, err=%v", r, err)
	}
	var ratings, pending, value int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM ratings WHERE media_id=?`, movieID).Scan(&ratings); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`SELECT pending_delete,pending_rating FROM rating_sync_state WHERE media_id=?`, movieID).Scan(&pending, &value); err != nil {
		t.Fatal(err)
	}
	if ratings != 1 || pending != 0 || value != 9 {
		t.Fatalf("ratings=%d pendingDelete=%d pendingRating=%d", ratings, pending, value)
	}
}

func TestInvalidAndUnsupportedRating(t *testing.T) {
	svc, movieID, showID := testService(t)
	if err := svc.SetLocal(context.Background(), movieID, 11); !errors.Is(err, ErrInvalidRating) {
		t.Fatalf("error = %v", err)
	}
	if err := svc.SetLocal(context.Background(), showID, 8); !errors.Is(err, ErrUnsupportedTarget) {
		t.Fatalf("error = %v", err)
	}
}

func TestReviewIsOneCurrentLocalRecord(t *testing.T) {
	svc, movieID, _ := testService(t)
	ctx := context.Background()
	if err := svc.SetReview(ctx, movieID, "first"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetReview(ctx, movieID, " edited review "); err != nil {
		t.Fatal(err)
	}
	r, err := svc.GetReview(ctx, movieID)
	if err != nil || r == nil || r.Body != "edited review" {
		t.Fatalf("review=%#v err=%v", r, err)
	}
	var count int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM reviews WHERE media_id=?`, movieID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("review count=%d", count)
	}
}

func TestDeleteRatingQueuesRemoteDelete(t *testing.T) {
	svc, movieID, _ := testService(t)
	ctx := context.Background()
	if err := svc.SetLocal(ctx, movieID, 6); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteLocal(ctx, movieID); err != nil {
		t.Fatal(err)
	}
	if r, err := svc.Get(ctx, movieID); err != nil || r != nil {
		t.Fatalf("rating=%#v err=%v", r, err)
	}
	var pendingDelete int
	var pendingRating any
	if err := svc.db.QueryRow(`SELECT pending_delete,pending_rating FROM rating_sync_state WHERE media_id=?`, movieID).Scan(&pendingDelete, &pendingRating); err != nil {
		t.Fatal(err)
	}
	if pendingDelete != 1 || pendingRating != nil {
		t.Fatalf("pendingDelete=%d pendingRating=%v", pendingDelete, pendingRating)
	}
}
