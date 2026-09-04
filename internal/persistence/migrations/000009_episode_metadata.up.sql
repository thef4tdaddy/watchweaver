CREATE TABLE episode_metadata (
    media_id INTEGER PRIMARY KEY REFERENCES media_items(id) ON UPDATE RESTRICT ON DELETE CASCADE,
    finale_type TEXT NOT NULL DEFAULT 'none' CHECK (finale_type IN ('none', 'mid_season', 'season', 'series')),
    provider TEXT NOT NULL CHECK (length(trim(provider)) > 0),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
