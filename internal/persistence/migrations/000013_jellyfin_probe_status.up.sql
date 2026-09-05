ALTER TABLE jellyfin_ingest_status ADD COLUMN last_probe_at TEXT;
ALTER TABLE jellyfin_ingest_status ADD COLUMN last_probe_server_version TEXT;
ALTER TABLE jellyfin_ingest_status ADD COLUMN last_probe_plugin_version TEXT;
