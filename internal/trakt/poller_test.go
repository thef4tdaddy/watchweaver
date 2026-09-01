package trakt

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/persistence"
)

type fakeIncremental struct { calls []time.Time; failures int }
func(f *fakeIncremental)ImportIncrementalSince(_ context.Context,since time.Time)(HistoryImportResult,error){f.calls=append(f.calls,since);if f.failures>0{f.failures--;return HistoryImportResult{},errors.New("temporary trakt failure")};return HistoryImportResult{Imported:1},nil}

func pollDB(t *testing.T) interface{ Close() error } { t.Helper(); return nil }

func TestPollerDefaults(t *testing.T){p:=NewPoller(nil,&fakeIncremental{},PollerOptions{});if p.interval!=5*time.Minute{t.Fatalf("interval=%v",p.interval)};if p.overlap!=10*time.Minute{t.Fatalf("overlap=%v",p.overlap)}}

func TestPollerRetriesAndPersistsCheckpoint(t *testing.T){db,err:=persistence.OpenAndMigrate(persistence.Options{Path:filepath.Join(t.TempDir(),"poll.db")});if err!=nil{t.Fatal(err)};defer db.Close();base:=time.Date(2026,9,1,12,0,0,0,time.UTC);if _,err=db.Exec(`INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt','history_poll_checkpoint',?)`,base.Format(time.RFC3339Nano));err!=nil{t.Fatal(err)};f:=&fakeIncremental{failures:2};var sleeps []time.Duration;p:=NewPoller(db,f,PollerOptions{Now:func()time.Time{return base.Add(5*time.Minute)},Sleep:func(_ context.Context,d time.Duration)error{sleeps=append(sleeps,d);return nil}});if err=p.Poll(context.Background());err!=nil{t.Fatal(err)};if len(f.calls)!=3{t.Fatalf("calls=%d",len(f.calls))};want:=base.Add(-10*time.Minute);if !f.calls[0].Equal(want){t.Fatalf("since=%v want %v",f.calls[0],want)};if len(sleeps)!=2||sleeps[0]!=time.Second||sleeps[1]!=2*time.Second{t.Fatalf("sleeps=%v",sleeps)};status,err:=p.Status(context.Background());if err!=nil{t.Fatal(err)};if status.LastSuccess==nil||!status.LastSuccess.Equal(base.Add(5*time.Minute))||status.ConsecutiveFailures!=0||status.LastError!=""{t.Fatalf("status=%+v",status)}}

func TestPollerFailureDoesNotAdvanceCheckpointAndRestartOverlaps(t *testing.T){db,err:=persistence.OpenAndMigrate(persistence.Options{Path:filepath.Join(t.TempDir(),"restart.db")});if err!=nil{t.Fatal(err)};defer db.Close();base:=time.Date(2026,9,1,12,0,0,0,time.UTC);if _,err=db.Exec(`INSERT INTO integration_state(integration,state_key,state_value) VALUES('trakt','history_poll_checkpoint',?)`,base.Format(time.RFC3339Nano));err!=nil{t.Fatal(err)};bad:=&fakeIncremental{failures:5};p:=NewPoller(db,bad,PollerOptions{MaxRetries:2,Sleep:func(context.Context,time.Duration)error{return nil},Now:func()time.Time{return base.Add(time.Hour)}});if err=p.Poll(context.Background());err==nil{t.Fatal("expected failure")};var raw string;if err=db.QueryRow(`SELECT state_value FROM integration_state WHERE integration='trakt' AND state_key='history_poll_checkpoint'`).Scan(&raw);err!=nil{t.Fatal(err)};if raw!=base.Format(time.RFC3339Nano){t.Fatalf("checkpoint advanced: %s",raw)};good:=&fakeIncremental{};restarted:=NewPoller(db,good,PollerOptions{Now:func()time.Time{return base.Add(2*time.Hour)}});if err=restarted.Poll(context.Background());err!=nil{t.Fatal(err)};if len(good.calls)!=1||!good.calls[0].Equal(base.Add(-10*time.Minute)){t.Fatalf("restart since=%v",good.calls)}}
