package persistence

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestBotUpgradePreservesWebhookOwnershipAndDeliveryReceipts(t *testing.T) {
	old := fstest.MapFS{}
	entries, err := fs.ReadDir(embeddedMigrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "000017" {
			continue
		}
		path := "migrations/" + entry.Name()
		data, err := embeddedMigrations.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		old[path] = &fstest.MapFile{Data: data}
	}
	path := filepath.Join(t.TempDir(), "ww.db")
	db, err := OpenAndMigrate(Options{Path: path, MigrationsFS: old})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO media_items(id,media_type,title) VALUES(1,'movie','Existing')`,
		`INSERT INTO prompt_tasks(id,media_id,task_type,state) VALUES(1,1,'rating','pending')`,
		`INSERT INTO discord_task_notifications(prompt_task_id,state) VALUES(1,'sent')`,
	} {
		if _, err = db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	db, err = OpenAndMigrate(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	var state string
	if err = db.QueryRow(`SELECT state FROM discord_task_notifications WHERE prompt_task_id=1`).Scan(&state); err != nil || state != "sent" {
		t.Fatal(state, err)
	}
	for _, query := range []string{
		`INSERT INTO bot_notifications(task_id,revision,state,lease,message_id) VALUES(1,1,'sent','lease','message')`,
		`INSERT INTO bot_requests(actor,request_id,fingerprint,result) VALUES('123','interaction','hash','{"id":1}')`,
	} {
		if _, err = db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	db, err = OpenAndMigrate(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var message, result string
	var attempts int
	if err = db.QueryRow(`SELECT message_id,attempt_count FROM bot_notifications WHERE task_id=1`).Scan(&message, &attempts); err != nil || message != "message" || attempts != 0 {
		t.Fatal(message, attempts, err)
	}
	if err = db.QueryRow(`SELECT result FROM bot_requests WHERE actor='123'`).Scan(&result); err != nil || !strings.Contains(result, `"id":1`) {
		t.Fatal(result, err)
	}
}
