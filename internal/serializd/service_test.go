package serializd

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/persistence"
)

func TestCountThresholdAndTransferableChanges(t *testing.T) {
	db, episode, season := serializdDB(t)
	service := NewService(db)
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	service.SetNow(func() time.Time { return now })
	addEpisodeWatch(t, db, episode, "1", false)
	addEpisodeWatch(t, db, episode, "2", false)
	_, _ = db.Exec(`INSERT INTO ratings(media_id,rating,source) VALUES(?,7,'local')`, episode)
	_, _ = db.Exec(`UPDATE ratings SET rating=8 WHERE media_id=?`, episode)
	_, _ = db.Exec(`UPDATE ratings SET rating=8 WHERE media_id=?`, episode)
	_, _ = db.Exec(`INSERT INTO ratings(media_id,rating,source) VALUES(?,9,'local')`, season)
	_, _ = db.Exec(`INSERT INTO reviews(media_id,body) VALUES(?,'episode review')`, episode)
	status, err := service.Status(context.Background(), Options{Enabled: true, ReminderChanges: 4, ReminderDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingChanges != 4 || status.PendingEpisodeWatches != 2 || status.PendingRatingChanges != 2 || !status.CountThresholdReached || !status.Due {
		t.Fatalf("status=%+v", status)
	}
	if status.UnsupportedSeasonRatings != 1 || status.UnsupportedTVReviews != 1 {
		t.Fatalf("unsupported=%+v", status)
	}
	if status.ImportURL != "https://www.serializd.com/trakt" {
		t.Fatalf("import URL=%q", status.ImportURL)
	}
}

func TestBaselineAndSeasonActivityDoNotCount(t *testing.T) {
	db, episode, season := serializdDB(t)
	addEpisodeWatch(t, db, episode, "baseline", true)
	stamp := "2026-09-01T00:00:00Z"
	if _, err := db.Exec(`INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at,is_baseline) VALUES(?,'trakt','season',?,?,0)`, season, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO ratings(media_id,rating,source) VALUES(?,8,'trakt')`, episode); err != nil {
		t.Fatal(err)
	}
	status, err := NewService(db).Status(context.Background(), Options{Enabled: true, ReminderChanges: 1, ReminderDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingChanges != 0 || status.Due {
		t.Fatalf("status=%+v", status)
	}
	if status.TrackedEpisodeWatches != 1 {
		t.Fatalf("baseline history was not reported: %+v", status)
	}
	synced, err := NewService(db).MarkSynced(context.Background(), Options{Enabled: true, ReminderChanges: 1, ReminderDays: 1})
	if err != nil || synced.LastConfirmedAt == nil {
		t.Fatalf("zero-change baseline could not be confirmed: %+v err=%v", synced, err)
	}
}

func TestElapsedThresholdRequiresPendingActivity(t *testing.T) {
	db, episode, _ := serializdDB(t)
	service := NewService(db)
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	service.SetNow(func() time.Time { return now })
	status, _ := service.Status(context.Background(), Options{Enabled: true, ReminderChanges: 20, ReminderDays: 14})
	if status.Due || status.ElapsedThresholdReached {
		t.Fatalf("zero pending became due: %+v", status)
	}
	addEpisodeWatch(t, db, episode, "1", false)
	_, _ = db.Exec(`UPDATE serializd_changes SET occurred_at='2026-09-01T00:00:00Z'`)
	status, _ = service.Status(context.Background(), Options{Enabled: true, ReminderChanges: 20, ReminderDays: 14})
	if !status.ElapsedThresholdReached || !status.Due {
		t.Fatalf("elapsed not due: %+v", status)
	}
	disabled, _ := service.Status(context.Background(), Options{Enabled: false, ReminderChanges: 20, ReminderDays: 14})
	if disabled.Due {
		t.Fatalf("disabled due: %+v", disabled)
	}
}

func TestMarkSyncedAndReminderTransitionPersistence(t *testing.T) {
	db, episode, _ := serializdDB(t)
	service := NewService(db)
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	service.SetNow(func() time.Time { return now })
	addEpisodeWatch(t, db, episode, "1", false)
	options := Options{Enabled: true, ReminderChanges: 1, ReminderDays: 14}
	status, _ := service.Status(context.Background(), options)
	if !status.ReminderAnnouncementPending {
		t.Fatalf("first due transition=%+v", status)
	}
	if err := service.MarkReminderAnnounced(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, _ = service.Status(context.Background(), options)
	if status.ReminderAnnouncementPending {
		t.Fatalf("duplicate reminder: %+v", status)
	}
	synced, err := service.MarkSynced(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if synced.PendingChanges != 0 || synced.Due || synced.LastConfirmedAt == nil || !synced.LastConfirmedAt.Equal(now) {
		t.Fatalf("synced=%+v", synced)
	}
	restarted := NewService(db)
	restarted.SetNow(func() time.Time { return now.Add(time.Hour) })
	status, _ = restarted.Status(context.Background(), options)
	if status.PendingChanges != 0 || status.Due {
		t.Fatalf("restart=%+v", status)
	}
	addEpisodeWatch(t, db, episode, "2", false)
	status, _ = restarted.Status(context.Background(), options)
	if !status.Due || !status.ReminderAnnouncementPending {
		t.Fatalf("new transition=%+v", status)
	}
}

func serializdDB(t *testing.T) (*sql.DB, int64, int64) {
	t.Helper()
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(), "serializd.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	show := insert(t, db, `INSERT INTO media_items(media_type,title) VALUES('show','Show')`)
	season := insert(t, db, `INSERT INTO media_items(media_type,title,parent_id,season_number) VALUES('season','Season',?,1)`, show)
	episode := insert(t, db, `INSERT INTO media_items(media_type,title,parent_id,episode_number) VALUES('episode','Episode',?,1)`, season)
	return db, episode, season
}
func insert(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	result, err := db.Exec(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	return id
}
func addEpisodeWatch(t *testing.T, db *sql.DB, episode int64, id string, baseline bool) {
	t.Helper()
	value := 0
	if baseline {
		value = 1
	}
	stamp := "2026-09-01T00:00:00Z"
	if _, err := db.Exec(`INSERT INTO watch_events(media_id,source,source_event_id,watched_at_utc,source_watched_at,is_baseline) VALUES(?,'trakt',?,?,?,?)`, episode, id, stamp, stamp, value); err != nil {
		t.Fatal(err)
	}
}
