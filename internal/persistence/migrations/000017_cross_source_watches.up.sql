ALTER TABLE watch_events ADD COLUMN duplicate_of INTEGER REFERENCES watch_events(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX watch_events_duplicate_pair ON watch_events(duplicate_of) WHERE duplicate_of IS NOT NULL;

-- Backfill only mutually closest pairs with established shared media identity.
-- Keep both reports, including source IDs needed for replay protection.
WITH candidates AS (
    SELECT j.id AS jellyfin_id,t.id AS trakt_id,
        ROW_NUMBER() OVER (PARTITION BY j.id ORDER BY ABS(unixepoch(j.watched_at_utc)-unixepoch(t.watched_at_utc)),t.id) AS j_rank,
        ROW_NUMBER() OVER (PARTITION BY t.id ORDER BY ABS(unixepoch(j.watched_at_utc)-unixepoch(t.watched_at_utc)),j.id) AS t_rank
    FROM watch_events j JOIN watch_events t ON t.media_id=j.media_id AND t.source='trakt'
    WHERE j.source='jellyfin' AND j.deleted_at IS NULL AND t.deleted_at IS NULL
        AND ABS(unixepoch(j.watched_at_utc)-unixepoch(t.watched_at_utc))<=120
), pairs AS (
    SELECT MAX(jellyfin_id,trakt_id) AS duplicate_id,MIN(jellyfin_id,trakt_id) AS canonical_id
    FROM candidates WHERE j_rank=1 AND t_rank=1
)
UPDATE watch_events SET duplicate_of=(SELECT canonical_id FROM pairs WHERE duplicate_id=watch_events.id)
WHERE id IN (SELECT duplicate_id FROM pairs);
