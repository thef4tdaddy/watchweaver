ALTER TABLE bot_notifications ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE bot_notifications ADD COLUMN next_attempt_at TEXT;
ALTER TABLE bot_notifications ADD COLUMN last_error_code TEXT;
CREATE INDEX bot_notifications_delivery ON bot_notifications(state,next_attempt_at,id);
CREATE INDEX bot_sync_jobs_queue ON bot_sync_jobs(state,created_at,id);
