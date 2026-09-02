CREATE UNIQUE INDEX reviews_one_current_per_media
    ON reviews(media_id);

CREATE TABLE rating_sync_state (
    media_id INTEGER PRIMARY KEY REFERENCES media_items(id) ON UPDATE RESTRICT ON DELETE CASCADE,
    last_local_change_at TEXT,
    last_remote_change_at TEXT,
    last_remote_rating INTEGER CHECK (last_remote_rating IS NULL OR last_remote_rating BETWEEN 1 AND 10),
    pending_rating INTEGER CHECK (pending_rating IS NULL OR pending_rating BETWEEN 1 AND 10),
    pending_delete INTEGER NOT NULL DEFAULT 0 CHECK (pending_delete IN (0, 1)),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TEXT,
    last_error TEXT,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CHECK (NOT (pending_rating IS NOT NULL AND pending_delete = 1))
);
