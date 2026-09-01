CREATE TABLE media_items (
    id INTEGER PRIMARY KEY,
    media_type TEXT NOT NULL CHECK (media_type IN ('movie', 'show', 'season', 'episode')),
    title TEXT NOT NULL CHECK (length(trim(title)) > 0),
    year INTEGER CHECK (year IS NULL OR year >= 1888),
    parent_id INTEGER REFERENCES media_items(id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    season_number INTEGER,
    episode_number INTEGER,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CHECK (
        (media_type IN ('movie', 'show') AND parent_id IS NULL AND season_number IS NULL AND episode_number IS NULL)
        OR (media_type = 'season' AND parent_id IS NOT NULL AND season_number IS NOT NULL AND season_number >= 0 AND episode_number IS NULL)
        OR (media_type = 'episode' AND parent_id IS NOT NULL AND season_number IS NULL AND episode_number IS NOT NULL AND episode_number >= 0)
    )
);

CREATE UNIQUE INDEX media_season_number_unique
    ON media_items(parent_id, season_number)
    WHERE media_type = 'season';

CREATE UNIQUE INDEX media_episode_number_unique
    ON media_items(parent_id, episode_number)
    WHERE media_type = 'episode';

CREATE TRIGGER media_items_validate_parent_insert
BEFORE INSERT ON media_items
WHEN NEW.parent_id IS NOT NULL
BEGIN
    SELECT CASE
        WHEN NEW.media_type = 'season' AND NOT EXISTS (
            SELECT 1 FROM media_items WHERE id = NEW.parent_id AND media_type = 'show'
        ) THEN RAISE(ABORT, 'season parent must be a show')
        WHEN NEW.media_type = 'episode' AND NOT EXISTS (
            SELECT 1 FROM media_items WHERE id = NEW.parent_id AND media_type = 'season'
        ) THEN RAISE(ABORT, 'episode parent must be a season')
    END;
END;

CREATE TRIGGER media_items_validate_parent_update
BEFORE UPDATE OF media_type, parent_id ON media_items
WHEN NEW.parent_id IS NOT NULL
BEGIN
    SELECT CASE
        WHEN NEW.media_type = 'season' AND NOT EXISTS (
            SELECT 1 FROM media_items WHERE id = NEW.parent_id AND media_type = 'show'
        ) THEN RAISE(ABORT, 'season parent must be a show')
        WHEN NEW.media_type = 'episode' AND NOT EXISTS (
            SELECT 1 FROM media_items WHERE id = NEW.parent_id AND media_type = 'season'
        ) THEN RAISE(ABORT, 'episode parent must be a season')
    END;
END;

CREATE TABLE external_ids (
    id INTEGER PRIMARY KEY,
    media_id INTEGER NOT NULL REFERENCES media_items(id) ON UPDATE RESTRICT ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (length(trim(provider)) > 0),
    external_id TEXT NOT NULL CHECK (length(trim(external_id)) > 0),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(provider, external_id),
    UNIQUE(media_id, provider)
);

CREATE TABLE watch_events (
    id INTEGER PRIMARY KEY,
    media_id INTEGER NOT NULL REFERENCES media_items(id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    source TEXT NOT NULL CHECK (length(trim(source)) > 0),
    source_event_id TEXT,
    watched_at_utc TEXT NOT NULL CHECK (watched_at_utc GLOB '????-??-??T??:??:??*'),
    source_watched_at TEXT NOT NULL CHECK (length(trim(source_watched_at)) > 0),
    source_timezone TEXT,
    imported_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    deleted_at TEXT,
    CHECK (source_event_id IS NULL OR length(trim(source_event_id)) > 0)
);

CREATE UNIQUE INDEX watch_events_source_identity_unique
    ON watch_events(source, source_event_id)
    WHERE source_event_id IS NOT NULL;

CREATE INDEX watch_events_media_watched_at
    ON watch_events(media_id, watched_at_utc);

CREATE TABLE ratings (
    media_id INTEGER PRIMARY KEY REFERENCES media_items(id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    rating INTEGER NOT NULL CHECK (rating BETWEEN 1 AND 10),
    source TEXT NOT NULL CHECK (length(trim(source)) > 0),
    remote_updated_at TEXT,
    local_updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TRIGGER ratings_validate_target_insert
BEFORE INSERT ON ratings
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM media_items
        WHERE id = NEW.media_id AND media_type IN ('movie', 'season', 'episode')
    ) THEN RAISE(ABORT, 'unsupported rating target') END;
END;

CREATE TRIGGER ratings_validate_target_update
BEFORE UPDATE OF media_id ON ratings
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM media_items
        WHERE id = NEW.media_id AND media_type IN ('movie', 'season', 'episode')
    ) THEN RAISE(ABORT, 'unsupported rating target') END;
END;

CREATE TABLE reviews (
    id INTEGER PRIMARY KEY,
    media_id INTEGER NOT NULL REFERENCES media_items(id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    body TEXT NOT NULL CHECK (length(trim(body)) > 0),
    source TEXT NOT NULL DEFAULT 'local' CHECK (length(trim(source)) > 0),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TRIGGER reviews_validate_target_insert
BEFORE INSERT ON reviews
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM media_items
        WHERE id = NEW.media_id AND media_type IN ('movie', 'season', 'episode')
    ) THEN RAISE(ABORT, 'unsupported review target') END;
END;

CREATE TABLE prompt_tasks (
    id INTEGER PRIMARY KEY,
    media_id INTEGER NOT NULL REFERENCES media_items(id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    watch_event_id INTEGER REFERENCES watch_events(id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    task_type TEXT NOT NULL CHECK (task_type IN ('rating', 'review', 'rating_review')),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'snoozed', 'completed', 'skipped', 'ignored')),
    snoozed_until TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CHECK ((state = 'snoozed' AND snoozed_until IS NOT NULL) OR (state <> 'snoozed' AND snoozed_until IS NULL))
);

CREATE INDEX prompt_tasks_state_created_at ON prompt_tasks(state, created_at);

CREATE TRIGGER prompt_tasks_validate_target_insert
BEFORE INSERT ON prompt_tasks
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM media_items
        WHERE id = NEW.media_id AND media_type IN ('movie', 'season', 'episode')
    ) THEN RAISE(ABORT, 'unsupported prompt target') END;
END;

CREATE TRIGGER prompt_tasks_validate_watch_event_insert
BEFORE INSERT ON prompt_tasks
WHEN NEW.watch_event_id IS NOT NULL
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM watch_events
        WHERE id = NEW.watch_event_id AND media_id = NEW.media_id
    ) THEN RAISE(ABORT, 'prompt watch event must target same media') END;
END;

CREATE TABLE integration_state (
    integration TEXT NOT NULL CHECK (length(trim(integration)) > 0),
    state_key TEXT NOT NULL CHECK (length(trim(state_key)) > 0),
    state_value TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (integration, state_key)
);

CREATE TABLE app_settings (
    setting_key TEXT PRIMARY KEY CHECK (length(trim(setting_key)) > 0),
    setting_value TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
