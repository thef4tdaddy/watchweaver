ALTER TABLE media_items ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;
ALTER TABLE prompt_tasks ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;
CREATE TRIGGER bot_task_revision AFTER UPDATE OF state,snoozed_until ON prompt_tasks BEGIN
 UPDATE prompt_tasks SET revision=revision+1 WHERE id=NEW.id;
END;
CREATE TRIGGER bot_rating_insert AFTER INSERT ON ratings BEGIN UPDATE media_items SET revision=revision+1 WHERE id=NEW.media_id; END;
CREATE TRIGGER bot_rating_update AFTER UPDATE ON ratings BEGIN UPDATE media_items SET revision=revision+1 WHERE id=NEW.media_id; END;
CREATE TRIGGER bot_rating_delete AFTER DELETE ON ratings BEGIN UPDATE media_items SET revision=revision+1 WHERE id=OLD.media_id; END;
CREATE TRIGGER bot_review_insert AFTER INSERT ON reviews BEGIN UPDATE media_items SET revision=revision+1 WHERE id=NEW.media_id; END;
CREATE TRIGGER bot_review_update AFTER UPDATE ON reviews BEGIN UPDATE media_items SET revision=revision+1 WHERE id=NEW.media_id; END;
CREATE TRIGGER bot_review_delete AFTER DELETE ON reviews BEGIN UPDATE media_items SET revision=revision+1 WHERE id=OLD.media_id; END;
CREATE TABLE bot_requests (
 actor TEXT NOT NULL,
 request_id TEXT NOT NULL,
 fingerprint TEXT NOT NULL,
 result TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 PRIMARY KEY(actor,request_id)
);
CREATE TABLE prompt_ignored_media (media_id INTEGER PRIMARY KEY REFERENCES media_items(id));
CREATE TABLE bot_notifications (
 id INTEGER PRIMARY KEY,
 task_id INTEGER NOT NULL REFERENCES prompt_tasks(id),
 revision INTEGER NOT NULL,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','leased','sent','expired')),
 lease TEXT,
 lease_until TEXT,
 message_id TEXT,
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 UNIQUE(task_id,revision)
);
CREATE TRIGGER bot_ignore_new_prompt AFTER INSERT ON prompt_tasks
WHEN EXISTS (SELECT 1 FROM prompt_ignored_media i JOIN media_items m ON m.id=NEW.media_id LEFT JOIN media_items p ON p.id=m.parent_id WHERE i.media_id=m.id OR i.media_id=m.parent_id OR i.media_id=p.parent_id)
BEGIN UPDATE prompt_tasks SET state='ignored',snoozed_until=NULL WHERE id=NEW.id; END;
CREATE TABLE bot_sync_jobs (
 id TEXT PRIMARY KEY,
 actor TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','running','completed','failed')),
 result TEXT,
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
