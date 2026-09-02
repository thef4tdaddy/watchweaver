CREATE TABLE letterboxd_batches (
    id INTEGER PRIMARY KEY,
    state TEXT NOT NULL DEFAULT 'generated' CHECK (state IN ('generated', 'confirmed')),
    timezone TEXT NOT NULL,
    generated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    confirmed_at TEXT
);

CREATE TABLE letterboxd_batch_rows (
    id INTEGER PRIMARY KEY,
    batch_id INTEGER NOT NULL REFERENCES letterboxd_batches(id) ON DELETE CASCADE,
    media_id INTEGER NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
    representative_watch_event_id INTEGER NOT NULL REFERENCES watch_events(id) ON DELETE RESTRICT,
    watched_date TEXT NOT NULL,
    duplicate_count INTEGER NOT NULL DEFAULT 0 CHECK (duplicate_count >= 0),
    rating_revision INTEGER NOT NULL DEFAULT 0,
    review_revision INTEGER NOT NULL DEFAULT 0,
    UNIQUE(batch_id, media_id, watched_date)
);

CREATE TABLE letterboxd_batch_events (
    batch_row_id INTEGER NOT NULL REFERENCES letterboxd_batch_rows(id) ON DELETE CASCADE,
    watch_event_id INTEGER NOT NULL REFERENCES watch_events(id) ON DELETE RESTRICT,
    PRIMARY KEY (batch_row_id, watch_event_id)
);

CREATE TABLE letterboxd_batch_files (
    id INTEGER PRIMARY KEY,
    batch_id INTEGER NOT NULL REFERENCES letterboxd_batches(id) ON DELETE CASCADE,
    part_number INTEGER NOT NULL CHECK (part_number > 0),
    filename TEXT NOT NULL,
    content BLOB NOT NULL,
    size_bytes INTEGER NOT NULL CHECK (size_bytes > 0),
    UNIQUE(batch_id, part_number)
);

CREATE TABLE letterboxd_media_changes (
    media_id INTEGER PRIMARY KEY REFERENCES media_items(id) ON DELETE CASCADE,
    rating_revision INTEGER NOT NULL DEFAULT 0,
    review_revision INTEGER NOT NULL DEFAULT 0
);

INSERT INTO letterboxd_media_changes(media_id, rating_revision)
SELECT media_id, 1 FROM ratings WHERE true
ON CONFLICT(media_id) DO UPDATE SET rating_revision=1;

INSERT INTO letterboxd_media_changes(media_id, review_revision)
SELECT media_id, 1 FROM reviews WHERE true
ON CONFLICT(media_id) DO UPDATE SET review_revision=1;

CREATE TRIGGER letterboxd_rating_insert AFTER INSERT ON ratings BEGIN
    INSERT INTO letterboxd_media_changes(media_id,rating_revision) VALUES(NEW.media_id,1)
    ON CONFLICT(media_id) DO UPDATE SET rating_revision=rating_revision+1;
END;
CREATE TRIGGER letterboxd_rating_update AFTER UPDATE ON ratings BEGIN
    INSERT INTO letterboxd_media_changes(media_id,rating_revision) VALUES(NEW.media_id,1)
    ON CONFLICT(media_id) DO UPDATE SET rating_revision=rating_revision+1;
END;
CREATE TRIGGER letterboxd_rating_delete AFTER DELETE ON ratings BEGIN
    INSERT INTO letterboxd_media_changes(media_id,rating_revision) VALUES(OLD.media_id,1)
    ON CONFLICT(media_id) DO UPDATE SET rating_revision=rating_revision+1;
END;
CREATE TRIGGER letterboxd_review_insert AFTER INSERT ON reviews BEGIN
    INSERT INTO letterboxd_media_changes(media_id,review_revision) VALUES(NEW.media_id,1)
    ON CONFLICT(media_id) DO UPDATE SET review_revision=review_revision+1;
END;
CREATE TRIGGER letterboxd_review_update AFTER UPDATE ON reviews BEGIN
    INSERT INTO letterboxd_media_changes(media_id,review_revision) VALUES(NEW.media_id,1)
    ON CONFLICT(media_id) DO UPDATE SET review_revision=review_revision+1;
END;
CREATE TRIGGER letterboxd_review_delete AFTER DELETE ON reviews BEGIN
    INSERT INTO letterboxd_media_changes(media_id,review_revision) VALUES(OLD.media_id,1)
    ON CONFLICT(media_id) DO UPDATE SET review_revision=review_revision+1;
END;

CREATE INDEX letterboxd_batches_state ON letterboxd_batches(state, generated_at);
CREATE INDEX letterboxd_rows_media ON letterboxd_batch_rows(media_id, batch_id);
CREATE INDEX letterboxd_events_watch ON letterboxd_batch_events(watch_event_id);
