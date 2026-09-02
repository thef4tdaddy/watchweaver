ALTER TABLE watch_events
    ADD COLUMN is_baseline INTEGER NOT NULL DEFAULT 1 CHECK (is_baseline IN (0, 1));

CREATE INDEX watch_events_incremental_import
    ON watch_events(is_baseline, imported_at, id);
