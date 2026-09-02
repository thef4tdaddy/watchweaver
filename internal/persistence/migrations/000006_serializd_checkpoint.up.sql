CREATE TABLE serializd_changes (
    id INTEGER PRIMARY KEY,
    media_id INTEGER NOT NULL REFERENCES media_items(id) ON DELETE RESTRICT,
    watch_event_id INTEGER REFERENCES watch_events(id) ON DELETE RESTRICT,
    change_type TEXT NOT NULL CHECK (change_type IN ('episode_watch', 'episode_rating')),
    occurred_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE serializd_checkpoint (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    last_change_id INTEGER NOT NULL DEFAULT 0,
    confirmed_at TEXT,
    due INTEGER NOT NULL DEFAULT 0 CHECK (due IN (0, 1)),
    reminder_announced INTEGER NOT NULL DEFAULT 0 CHECK (reminder_announced IN (0, 1)),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

INSERT INTO serializd_checkpoint(singleton) VALUES(1);

CREATE TRIGGER serializd_episode_watch_insert
AFTER INSERT ON watch_events
WHEN NEW.is_baseline = 0 AND EXISTS (
    SELECT 1 FROM media_items WHERE id=NEW.media_id AND media_type='episode'
)
BEGIN
    INSERT INTO serializd_changes(media_id,watch_event_id,change_type)
    VALUES(NEW.media_id,NEW.id,'episode_watch');
END;

CREATE TRIGGER serializd_episode_rating_insert
AFTER INSERT ON ratings
WHEN EXISTS (SELECT 1 FROM media_items WHERE id=NEW.media_id AND media_type='episode')
AND (
    NEW.source='local' OR EXISTS (
        SELECT 1 FROM integration_state
        WHERE integration='trakt' AND state_key='initial_ratings_complete' AND state_value='1'
    )
)
BEGIN
    INSERT INTO serializd_changes(media_id,change_type) VALUES(NEW.media_id,'episode_rating');
END;

CREATE TRIGGER serializd_episode_rating_update
AFTER UPDATE OF rating ON ratings
WHEN OLD.rating <> NEW.rating AND EXISTS (
    SELECT 1 FROM media_items WHERE id=NEW.media_id AND media_type='episode'
)
AND (
    NEW.source='local' OR EXISTS (
        SELECT 1 FROM integration_state
        WHERE integration='trakt' AND state_key='initial_ratings_complete' AND state_value='1'
    )
)
BEGIN
    INSERT INTO serializd_changes(media_id,change_type) VALUES(NEW.media_id,'episode_rating');
END;

CREATE INDEX serializd_changes_checkpoint ON serializd_changes(id, occurred_at);
