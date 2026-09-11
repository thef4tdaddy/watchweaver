CREATE TABLE jellyfin_remote_sources (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    url TEXT NOT NULL CHECK (length(trim(url)) > 0),
    user_id TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

INSERT INTO jellyfin_remote_sources(id,name,url,user_id,enabled)
SELECT 'default','Remote Jellyfin',
       COALESCE((SELECT setting_value FROM app_settings WHERE setting_key='jellyfin_remote_url'),''),
       COALESCE((SELECT setting_value FROM app_settings WHERE setting_key='jellyfin_remote_user_id'),''),
       CASE COALESCE((SELECT setting_value FROM app_settings WHERE setting_key='jellyfin_remote_enabled'),'false') WHEN 'true' THEN 1 ELSE 0 END
WHERE COALESCE((SELECT setting_value FROM app_settings WHERE setting_key='jellyfin_remote_url'),'') <> '';
