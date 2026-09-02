package letterboxd

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/persistence"
)

func TestFullExportRewatchesDuplicatesAndCurrentMetadata(t *testing.T) {
	db := testDB(t)
	movie := addMovie(t, db, "Movie", 2020, "tmdb", "55")
	addWatch(t, db, movie, "1", "2026-01-01T12:00:00Z")
	addWatch(t, db, movie, "2", "2026-01-02T10:00:00Z")
	addWatch(t, db, movie, "3", "2026-01-02T20:00:00Z")
	_, _ = db.Exec(`INSERT INTO ratings(media_id,rating,source,local_updated_at) VALUES(?,7,'local','2026-01-03T00:00:00Z')`, movie)
	_, _ = db.Exec(`INSERT INTO reviews(media_id,body,updated_at) VALUES(?,'Great, "really" great','2026-01-03T00:00:00Z')`, movie)
	service := NewService(db)
	batch, err := service.Generate(context.Background(), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if batch.RowCount != 2 || batch.EventCount != 3 || batch.DuplicateWarnings != 1 || batch.State != "generated" {
		t.Fatalf("batch=%+v", batch)
	}
	file, err := service.GetFile(context.Background(), batch.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	records := readCSV(t, file.Content)
	if len(records) != 3 {
		t.Fatalf("records=%v", records)
	}
	if records[1][0] != "55" || records[1][4] != "" || records[1][6] != "false" || records[1][7] != "" {
		t.Fatalf("first row=%v", records[1])
	}
	if records[2][4] != "3.5" || records[2][6] != "true" || records[2][7] != `Great, "really" great` {
		t.Fatalf("latest row=%v", records[2])
	}
}

func TestTimezoneAndIdentityFallbacks(t *testing.T) {
	db := testDB(t)
	tmdb := addMovie(t, db, "TMDB", 2020, "tmdb", "10")
	imdb := addMovie(t, db, "IMDb", 2021, "imdb", "tt20")
	title := addMovie(t, db, "Title Only", 2022, "", "")
	addWatch(t, db, tmdb, "1", "2026-01-02T04:30:00Z")
	addWatch(t, db, imdb, "2", "2026-01-03T00:00:00Z")
	addWatch(t, db, title, "3", "2026-01-04T00:00:00Z")
	service := NewService(db)
	batch, err := service.Generate(context.Background(), "America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	file, _ := service.GetFile(context.Background(), batch.ID, 1)
	records := readCSV(t, file.Content)
	if records[1][0] != "10" || records[1][5] != "2026-01-01" {
		t.Fatalf("tmdb/timezone=%v", records[1])
	}
	if records[2][1] != "tt20" || records[2][2] != "" {
		t.Fatalf("imdb=%v", records[2])
	}
	if records[3][2] != "Title Only" || records[3][3] != "2022" {
		t.Fatalf("fallback=%v", records[3])
	}
}

func TestGeneratedRegenerationConfirmationAndRatingChange(t *testing.T) {
	db := testDB(t)
	movie := addMovie(t, db, "Movie", 2020, "tmdb", "55")
	addWatch(t, db, movie, "1", "2026-01-01T00:00:00Z")
	_, _ = db.Exec(`INSERT INTO ratings(media_id,rating,source) VALUES(?,6,'local')`, movie)
	service := NewService(db)
	service.SetNow(func() time.Time { return time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC) })
	first, err := service.Generate(context.Background(), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	status, _ := service.Status(context.Background(), "UTC")
	if status.PendingRows != 1 || status.GeneratedBatches != 1 {
		t.Fatalf("generated should remain pending: %+v", status)
	}
	second, err := service.Generate(context.Background(), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatal("expected regeneratable new batch")
	}
	if _, err = service.Confirm(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	status, _ = service.Status(context.Background(), "UTC")
	if status.PendingRows != 0 {
		t.Fatalf("confirmed still pending: %+v", status)
	}
	_, _ = db.Exec(`UPDATE ratings SET rating=9,local_updated_at='2026-01-03T00:00:00Z' WHERE media_id=?`, movie)
	status, _ = service.Status(context.Background(), "UTC")
	if status.PendingRows != 1 || status.PendingEvents != 1 {
		t.Fatalf("rating change not pending: %+v", status)
	}
	third, err := service.Generate(context.Background(), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	file, _ := service.GetFile(context.Background(), third.ID, 1)
	records := readCSV(t, file.Content)
	if len(records) != 2 || records[1][4] != "4.5" {
		t.Fatalf("delta=%v", records)
	}
	if _, err = service.Confirm(context.Background(), first.ID); err != ErrBatchConfirmed {
		t.Fatalf("repeat confirm=%v", err)
	}
}

func TestReviewDeletionCreatesBlankMetadataDelta(t *testing.T) {
	db := testDB(t)
	movie := addMovie(t, db, "Movie", 2020, "", "")
	addWatch(t, db, movie, "1", "2026-01-01T00:00:00Z")
	_, _ = db.Exec(`INSERT INTO reviews(media_id,body) VALUES(?,'Review')`, movie)
	service := NewService(db)
	service.SetNow(func() time.Time { return time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC) })
	batch, _ := service.Generate(context.Background(), "UTC")
	_, _ = service.Confirm(context.Background(), batch.ID)
	_, _ = db.Exec(`DELETE FROM reviews WHERE media_id=?`, movie)
	status, _ := service.Status(context.Background(), "UTC")
	if status.PendingRows != 1 {
		t.Fatalf("deletion not pending: %+v", status)
	}
	delta, _ := service.Generate(context.Background(), "UTC")
	file, _ := service.GetFile(context.Background(), delta.ID, 1)
	records := readCSV(t, file.Content)
	if records[1][7] != "" {
		t.Fatalf("review deletion not blank: %v", records[1])
	}
}

func TestChunkingRepeatsHeadersAndStaysBelowLimit(t *testing.T) {
	db := testDB(t)
	for i := 0; i < 5; i++ {
		movie := addMovie(t, db, "A moderately long movie title", 2020, "", "")
		addWatch(t, db, movie, string(rune('a'+i)), time.Date(2026, 1, i+1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339))
	}
	service := NewService(db)
	service.SetMaxFileBytes(120)
	batch, err := service.Generate(context.Background(), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Files) < 2 {
		t.Fatalf("expected chunks: %+v", batch.Files)
	}
	for _, info := range batch.Files {
		if info.SizeBytes >= 120 {
			t.Fatalf("oversized: %+v", info)
		}
		file, _ := service.GetFile(context.Background(), batch.ID, info.PartNumber)
		records := readCSV(t, file.Content)
		if strings.Join(records[0], ",") != "tmdbID,imdbID,Title,Year,Rating,WatchedDate,Rewatch,Review" {
			t.Fatalf("header=%v", records[0])
		}
	}
}

func TestNothingPendingAndMissingBatch(t *testing.T) {
	service := NewService(testDB(t))
	if _, err := service.Generate(context.Background(), "UTC"); err != ErrNothingPending {
		t.Fatalf("err=%v", err)
	}
	if _, err := service.GetBatch(context.Background(), 999); err != ErrBatchNotFound {
		t.Fatalf("err=%v", err)
	}
	if _, err := service.Confirm(context.Background(), 999); err != ErrBatchNotFound {
		t.Fatalf("err=%v", err)
	}
}

func TestConfirmWaitsForConcurrentWriter(t *testing.T) {
	db := testDB(t)
	movie := addMovie(t, db, "Movie", 2020, "", "")
	addWatch(t, db, movie, "1", "2026-01-01T00:00:00Z")
	service := NewService(db)
	batch, err := service.Generate(context.Background(), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	writer, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`INSERT INTO app_metadata(key,value) VALUES('trakt-sync','active')`); err != nil {
		t.Fatal(err)
	}
	confirmed := make(chan error, 1)
	go func() {
		_, confirmErr := service.Confirm(context.Background(), batch.ID)
		confirmed <- confirmErr
	}()
	time.Sleep(100 * time.Millisecond)
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-confirmed; err != nil {
		t.Fatalf("confirmation did not wait for writer: %v", err)
	}
	stored, err := service.GetBatch(context.Background(), batch.ID)
	if err != nil || stored.State != "confirmed" {
		t.Fatalf("batch=%+v err=%v", stored, err)
	}
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "letterboxd.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
func addMovie(t *testing.T, db *sql.DB, title string, year int, provider, id string) int64 {
	t.Helper()
	result, err := db.Exec(`INSERT INTO media_items(media_type,title,year) VALUES('movie',?,?)`, title, year)
	if err != nil {
		t.Fatal(err)
	}
	mediaID, _ := result.LastInsertId()
	if provider != "" {
		if _, err = db.Exec(`INSERT INTO external_ids(media_id,provider,external_id) VALUES(?,?,?)`, mediaID, provider, id); err != nil {
			t.Fatal(err)
		}
	}
	return mediaID
}
func addWatch(t *testing.T, db *sql.DB, mediaID int64, sourceID, stamp string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at,is_baseline) VALUES(?,'trakt',?,?,?,0)`, mediaID, sourceID, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}
func readCSV(t *testing.T, content []byte) [][]string {
	t.Helper()
	records, err := csv.NewReader(bytes.NewReader(content)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return records
}
