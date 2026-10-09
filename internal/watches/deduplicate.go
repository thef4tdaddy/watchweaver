package watches

import (
	"context"
	"database/sql"
)

// Pair retains both reports but counts them as one viewing. Only opposite
// sources for the same media within two minutes can pair, once per report.
func Pair(ctx context.Context, tx *sql.Tx, eventID int64) (bool, error) {
	var candidate int64
	err := tx.QueryRowContext(ctx, `SELECT other.id
		FROM watch_events current JOIN watch_events other ON other.media_id=current.media_id
		WHERE current.id=? AND current.source IN ('trakt','jellyfin')
		AND other.source=CASE current.source WHEN 'trakt' THEN 'jellyfin' ELSE 'trakt' END
		AND other.deleted_at IS NULL AND other.duplicate_of IS NULL
		AND NOT EXISTS (SELECT 1 FROM watch_events paired WHERE paired.duplicate_of=other.id)
		AND ABS(unixepoch(other.watched_at_utc)-unixepoch(current.watched_at_utc))<=120
		ORDER BY ABS(unixepoch(other.watched_at_utc)-unixepoch(current.watched_at_utc)),other.id LIMIT 1`, eventID).Scan(&candidate)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE watch_events SET duplicate_of=? WHERE id=?`, candidate, eventID)
	return err == nil, err
}
