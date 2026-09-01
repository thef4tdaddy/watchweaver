package trakt

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/thef4tdaddy/watchweaver/internal/persistence"
)

func TestHistoryImporterPaginationHierarchyAndBaseline(t *testing.T) {
	db := testHistoryDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" { t.Errorf("missing bearer token") }
		page := r.URL.Query().Get("page")
		w.Header().Set("X-Pagination-Page-Count", "2")
		w.Header().Set("Content-Type", "application/json")
		if page == "1" {
			fmt.Fprint(w, `[{"id":101,"watched_at":"2026-08-01T01:02:03Z","action":"watch","type":"movie","movie":{"title":"Movie A","year":2025,"ids":{"trakt":11,"imdb":"tt0011","tmdb":21}}}]`)
			return
		}
		fmt.Fprint(w, `[{"id":102,"watched_at":"2026-08-02T02:03:04Z","action":"watch","type":"episode","episode":{"season":2,"number":3,"title":"Episode Three","ids":{"trakt":31,"tmdb":41}},"show":{"title":"Show A","year":2024,"ids":{"trakt":51,"tmdb":61}}}]`)
	}))
	defer server.Close()

	imp := NewHistoryImporter(db, server.URL, server.Client(), "token")
	got, err := imp.ImportInitial(context.Background())
	if err != nil { t.Fatal(err) }
	if got.Imported != 2 || got.Pages != 2 { t.Fatalf("unexpected result: %+v", got) }
	assertCount(t, db, "watch_events", 2)
	assertCount(t, db, "prompt_tasks", 0)
	assertCountWhere(t, db, "media_items", "media_type='movie'", 1)
	assertCountWhere(t, db, "media_items", "media_type='show'", 1)
	assertCountWhere(t, db, "media_items", "media_type='season'", 1)
	assertCountWhere(t, db, "media_items", "media_type='episode'", 1)
	var state string
	if err := db.QueryRow(`SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='initial_history_complete'`).Scan(&state); err != nil || state != "1" { t.Fatalf("baseline state=%q err=%v", state, err) }

	again, err := imp.ImportInitial(context.Background())
	if err != nil { t.Fatal(err) }
	if again.Imported != 0 || again.Pages != 0 { t.Fatalf("completed baseline should not refetch: %+v", again) }
}

func TestHistoryImporterOverlapRewatchesAndSameTimestamp(t *testing.T) {
	db := testHistoryDB(t)
	body := `[{"id":201,"watched_at":"2026-08-03T00:00:00Z","type":"movie","movie":{"title":"Repeat","year":2020,"ids":{"trakt":77}}},{"id":202,"watched_at":"2026-08-03T00:00:00Z","type":"movie","movie":{"title":"Repeat","year":2020,"ids":{"trakt":77}}}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("X-Pagination-Page-Count","1"); fmt.Fprint(w, body) }))
	defer server.Close()
	imp := NewHistoryImporter(db, server.URL, server.Client(), "")
	first, err := imp.ImportIncremental(context.Background()); if err != nil { t.Fatal(err) }
	second, err := imp.ImportIncremental(context.Background()); if err != nil { t.Fatal(err) }
	if first.Imported != 2 || second.Skipped != 2 { t.Fatalf("first=%+v second=%+v", first, second) }
	assertCount(t, db, "watch_events", 2)
	assertCountWhere(t, db, "media_items", "media_type='movie'", 1)
}

func TestHistoryImporterInterruptedInitialSyncResumesIdempotently(t *testing.T) {
	db := testHistoryDB(t)
	failPage2 := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Pagination-Page-Count", "2")
		if r.URL.Query().Get("page") == "1" { fmt.Fprint(w, `[{"id":301,"watched_at":"2026-08-04T00:00:00Z","type":"movie","movie":{"title":"One","year":2020,"ids":{"trakt":301}}}]`); return }
		if failPage2 { http.Error(w,"boom",500); return }
		fmt.Fprint(w, `[{"id":302,"watched_at":"2026-08-05T00:00:00Z","type":"movie","movie":{"title":"Two","year":2021,"ids":{"trakt":302}}}]`)
	}))
	defer server.Close()
	imp := NewHistoryImporter(db, server.URL, server.Client(), "")
	if _, err := imp.ImportInitial(context.Background()); err == nil { t.Fatal("expected interruption") }
	assertCount(t, db, "watch_events", 1)
	assertCountWhere(t, db, "integration_state", "integration='trakt' AND state_key='initial_history_complete'", 0)
	failPage2 = false
	got, err := imp.ImportInitial(context.Background()); if err != nil { t.Fatal(err) }
	if got.Imported != 1 || got.Skipped != 1 { t.Fatalf("resume result=%+v", got) }
	assertCount(t, db, "watch_events", 2)
	assertCount(t, db, "prompt_tasks", 0)
}

func TestHistoryImporterRejectsMalformedAndUnsupportedItems(t *testing.T) {
	cases := []string{
		`[{"id":0,"watched_at":"2026-08-01T00:00:00Z","type":"movie","movie":{"title":"Bad","ids":{"trakt":1}}}]`,
		`[{"id":1,"watched_at":"not-a-time","type":"movie","movie":{"title":"Bad","ids":{"trakt":1}}}]`,
		`[{"id":1,"watched_at":"2026-08-01T00:00:00Z","type":"show"}]`,
	}
	for i, body := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			db := testHistoryDB(t)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer s.Close()
			if _, err := NewHistoryImporter(db,s.URL,s.Client(),"").ImportIncremental(context.Background()); err == nil { t.Fatal("expected error") }
			assertCount(t,db,"watch_events",0)
		})
	}
}

func TestHistoryImporterRemoteAndDecodeErrors(t *testing.T) {
	for _, tc := range []struct{name string; handler http.HandlerFunc}{
		{"http", func(w http.ResponseWriter,r *http.Request){http.Error(w,"no",503)}},
		{"json", func(w http.ResponseWriter,r *http.Request){fmt.Fprint(w,"{")}},
	} {
		t.Run(tc.name,func(t *testing.T){db:=testHistoryDB(t); s:=httptest.NewServer(tc.handler); defer s.Close(); if _,err:=NewHistoryImporter(db,s.URL,s.Client(),"").ImportIncremental(context.Background()); err==nil {t.Fatal("expected error")}})
	}
}

func testHistoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := persistence.OpenAndMigrate(persistence.Options{Path: filepath.Join(t.TempDir(),"history.db")})
	if err != nil { t.Fatal(err) }
	t.Cleanup(func(){_ = db.Close()})
	return db
}

func assertCount(t *testing.T, db *sql.DB, table string, want int) { assertCountWhere(t,db,table,"1=1",want) }
func assertCountWhere(t *testing.T, db *sql.DB, table, where string, want int) {
	t.Helper(); var got int
	if err:=db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE "+where).Scan(&got); err!=nil {t.Fatal(err)}
	if got!=want {t.Fatalf("%s count=%d want=%d",table,got,want)}
}
