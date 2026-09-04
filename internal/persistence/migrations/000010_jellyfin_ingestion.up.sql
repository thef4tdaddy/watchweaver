CREATE TABLE jellyfin_ingest_events (
    id INTEGER PRIMARY KEY,
    server_id TEXT NOT NULL CHECK (length(trim(server_id)) > 0),
    event_id TEXT NOT NULL CHECK (length(trim(event_id)) > 0),
    event_type TEXT NOT NULL CHECK (event_type IN ('played', 'marked_played')),
    fingerprint TEXT NOT NULL CHECK (length(fingerprint) = 64),
    watch_event_id INTEGER NOT NULL REFERENCES watch_events(id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    plugin_version TEXT NOT NULL,
    server_version TEXT NOT NULL,
    accepted_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(server_id, event_id)
);

CREATE INDEX jellyfin_ingest_events_accepted_at
    ON jellyfin_ingest_events(accepted_at, id);

CREATE TABLE jellyfin_ingest_status (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    accepted_count INTEGER NOT NULL DEFAULT 0 CHECK (accepted_count >= 0),
    auth_failure_count INTEGER NOT NULL DEFAULT 0 CHECK (auth_failure_count >= 0),
    last_accepted_at TEXT,
    last_server_version TEXT,
    last_plugin_version TEXT,
    last_rejection_at TEXT,
    last_rejection_code TEXT,
    last_auth_failure_at TEXT,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

INSERT INTO jellyfin_ingest_status(singleton) VALUES(1);

