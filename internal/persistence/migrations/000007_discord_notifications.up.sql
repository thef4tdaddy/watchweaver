CREATE TABLE discord_task_notifications (
    prompt_task_id INTEGER PRIMARY KEY REFERENCES prompt_tasks(id) ON DELETE CASCADE,
    state TEXT NOT NULL CHECK (state IN ('pending', 'sent')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TEXT,
    last_error TEXT,
    sent_at TEXT,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX discord_task_retry ON discord_task_notifications(state, next_attempt_at);
