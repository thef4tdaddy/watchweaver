package letterboxd

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"strconv"
	"time"
)

const DefaultMaxFileBytes = 1_000_000

var ErrNothingPending = errors.New("no Letterboxd activity is pending")
var ErrBatchNotFound = errors.New("Letterboxd batch not found")
var ErrBatchConfirmed = errors.New("Letterboxd batch is already confirmed")

type Status struct {
	PendingRows       int `json:"pending_rows"`
	PendingEvents     int `json:"pending_events"`
	DuplicateWarnings int `json:"duplicate_warnings"`
	GeneratedBatches  int `json:"generated_batches"`
}

type Batch struct {
	ID                int64      `json:"id"`
	State             string     `json:"state"`
	Timezone          string     `json:"timezone"`
	GeneratedAt       time.Time  `json:"generated_at"`
	ConfirmedAt       *time.Time `json:"confirmed_at,omitempty"`
	RowCount          int        `json:"row_count"`
	EventCount        int        `json:"event_count"`
	DuplicateWarnings int        `json:"duplicate_warnings"`
	Files             []FileInfo `json:"files"`
}

type FileInfo struct {
	PartNumber int    `json:"part_number"`
	Filename   string `json:"filename"`
	SizeBytes  int    `json:"size_bytes"`
}

type File struct {
	FileInfo
	Content []byte
}

type Service struct {
	db           *sql.DB
	maxFileBytes int
	now          func() time.Time
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db, maxFileBytes: DefaultMaxFileBytes, now: time.Now}
}

func (s *Service) SetMaxFileBytes(value int) {
	if value > 0 {
		s.maxFileBytes = value
	}
}

func (s *Service) SetNow(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

type exportRow struct {
	mediaID               int64
	representativeEventID int64
	eventIDs              []int64
	title                 string
	year                  sql.NullInt64
	tmdbID                string
	imdbID                string
	watchedDate           string
	rewatch               bool
	duplicateCount        int
	rating                sql.NullInt64
	ratingRevision        int
	review                sql.NullString
	reviewRevision        int
}

type watch struct {
	id      int64
	mediaID int64
	title   string
	year    sql.NullInt64
	stamp   time.Time
	tmdbID  string
	imdbID  string
}

func (s *Service) Status(ctx context.Context, timezone string) (Status, error) {
	rows, err := s.pendingRows(ctx, timezone)
	if err != nil {
		return Status{}, err
	}
	status := Status{PendingRows: len(rows)}
	for _, row := range rows {
		status.PendingEvents += len(row.eventIDs)
		status.DuplicateWarnings += row.duplicateCount
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM letterboxd_batches WHERE state='generated'`).Scan(&status.GeneratedBatches); err != nil {
		return Status{}, err
	}
	return status, nil
}

func (s *Service) Generate(ctx context.Context, timezone string) (Batch, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return Batch{}, fmt.Errorf("invalid timezone: %w", err)
	}
	rows, err := s.pendingRowsAt(ctx, location)
	if err != nil {
		return Batch{}, err
	}
	if len(rows) == 0 {
		return Batch{}, ErrNothingPending
	}
	files, err := chunkCSV(rows, s.maxFileBytes)
	if err != nil {
		return Batch{}, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Batch{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO letterboxd_batches(state,timezone,generated_at) VALUES('generated',?,?)`, timezone, now)
	if err != nil {
		return Batch{}, err
	}
	batchID, err := result.LastInsertId()
	if err != nil {
		return Batch{}, err
	}
	for _, row := range rows {
		result, err = tx.ExecContext(ctx, `INSERT INTO letterboxd_batch_rows(batch_id,media_id,representative_watch_event_id,watched_date,duplicate_count,rating_revision,review_revision) VALUES(?,?,?,?,?,?,?)`, batchID, row.mediaID, row.representativeEventID, row.watchedDate, row.duplicateCount, row.ratingRevision, row.reviewRevision)
		if err != nil {
			return Batch{}, err
		}
		rowID, err := result.LastInsertId()
		if err != nil {
			return Batch{}, err
		}
		for _, eventID := range row.eventIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO letterboxd_batch_events(batch_row_id,watch_event_id) VALUES(?,?)`, rowID, eventID); err != nil {
				return Batch{}, err
			}
		}
	}
	for i, content := range files {
		filename := fmt.Sprintf("watchweaver-letterboxd-%d-part-%02d.csv", batchID, i+1)
		if _, err := tx.ExecContext(ctx, `INSERT INTO letterboxd_batch_files(batch_id,part_number,filename,content,size_bytes) VALUES(?,?,?,?,?)`, batchID, i+1, filename, content, len(content)); err != nil {
			return Batch{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Batch{}, err
	}
	return s.GetBatch(ctx, batchID)
}

func (s *Service) Confirm(ctx context.Context, batchID int64) (Batch, error) {
	now := s.now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `UPDATE letterboxd_batches SET state='confirmed',confirmed_at=? WHERE id=? AND state='generated'`, now, batchID)
	if err != nil {
		return Batch{}, err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		batch, getErr := s.GetBatch(ctx, batchID)
		if errors.Is(getErr, ErrBatchNotFound) {
			return Batch{}, getErr
		}
		if getErr != nil {
			return Batch{}, getErr
		}
		if batch.State == "confirmed" {
			return Batch{}, ErrBatchConfirmed
		}
	}
	return s.GetBatch(ctx, batchID)
}

func (s *Service) GetBatch(ctx context.Context, batchID int64) (Batch, error) {
	var batch Batch
	var generated string
	var confirmed sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,state,timezone,generated_at,confirmed_at FROM letterboxd_batches WHERE id=?`, batchID).Scan(&batch.ID, &batch.State, &batch.Timezone, &generated, &confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return Batch{}, ErrBatchNotFound
	}
	if err != nil {
		return Batch{}, err
	}
	batch.GeneratedAt, err = time.Parse(time.RFC3339Nano, generated)
	if err != nil {
		return Batch{}, err
	}
	if confirmed.Valid {
		value, parseErr := time.Parse(time.RFC3339Nano, confirmed.String)
		if parseErr != nil {
			return Batch{}, parseErr
		}
		batch.ConfirmedAt = &value
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(duplicate_count),0) FROM letterboxd_batch_rows WHERE batch_id=?`, batchID).Scan(&batch.RowCount, &batch.DuplicateWarnings); err != nil {
		return Batch{}, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM letterboxd_batch_events e JOIN letterboxd_batch_rows r ON r.id=e.batch_row_id WHERE r.batch_id=?`, batchID).Scan(&batch.EventCount); err != nil {
		return Batch{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT part_number,filename,size_bytes FROM letterboxd_batch_files WHERE batch_id=? ORDER BY part_number`, batchID)
	if err != nil {
		return Batch{}, err
	}
	defer rows.Close()
	batch.Files = []FileInfo{}
	for rows.Next() {
		var file FileInfo
		if err := rows.Scan(&file.PartNumber, &file.Filename, &file.SizeBytes); err != nil {
			return Batch{}, err
		}
		batch.Files = append(batch.Files, file)
	}
	return batch, rows.Err()
}

func (s *Service) ListBatches(ctx context.Context) ([]Batch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM letterboxd_batches ORDER BY generated_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	batches := make([]Batch, 0, len(ids))
	for _, id := range ids {
		batch, err := s.GetBatch(ctx, id)
		if err != nil {
			return nil, err
		}
		batches = append(batches, batch)
	}
	return batches, nil
}

func (s *Service) GetFile(ctx context.Context, batchID int64, part int) (File, error) {
	var file File
	err := s.db.QueryRowContext(ctx, `SELECT part_number,filename,size_bytes,content FROM letterboxd_batch_files WHERE batch_id=? AND part_number=?`, batchID, part).Scan(&file.PartNumber, &file.Filename, &file.SizeBytes, &file.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, ErrBatchNotFound
	}
	return file, err
}

func (s *Service) pendingRows(ctx context.Context, timezone string) ([]exportRow, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone: %w", err)
	}
	return s.pendingRowsAt(ctx, location)
}

func (s *Service) pendingRowsAt(ctx context.Context, location *time.Location) ([]exportRow, error) {
	watches, err := s.loadWatches(ctx)
	if err != nil {
		return nil, err
	}
	groups := map[string][]watch{}
	order := []string{}
	for _, item := range watches {
		date := item.stamp.In(location).Format("2006-01-02")
		key := strconv.FormatInt(item.mediaID, 10) + "/" + date
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], item)
	}
	latestKey := map[int64]string{}
	for _, key := range order {
		group := groups[key]
		latestKey[group[0].mediaID] = key
	}
	rows := make([]exportRow, 0, len(groups))
	prior := map[int64]int{}
	for _, key := range order {
		group := groups[key]
		representative := group[0]
		row := exportRow{mediaID: representative.mediaID, representativeEventID: representative.id, title: representative.title, year: representative.year, tmdbID: representative.tmdbID, imdbID: representative.imdbID, watchedDate: representative.stamp.In(location).Format("2006-01-02"), rewatch: prior[representative.mediaID] > 0, duplicateCount: len(group) - 1}
		for _, item := range group {
			row.eventIDs = append(row.eventIDs, item.id)
		}
		prior[representative.mediaID] += len(group)
		if latestKey[row.mediaID] == key {
			if err := s.loadCurrentMetadata(ctx, &row); err != nil {
				return nil, err
			}
		}
		pendingEvents, err := s.hasUnconfirmedEvents(ctx, row.eventIDs)
		if err != nil {
			return nil, err
		}
		metadataPending, err := s.metadataPending(ctx, row)
		if err != nil {
			return nil, err
		}
		if pendingEvents || metadataPending {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (s *Service) loadWatches(ctx context.Context) ([]watch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT w.id,m.id,m.title,m.year,w.watched_at_utc,COALESCE(tmdb.external_id,''),COALESCE(imdb.external_id,'') FROM watch_events w JOIN media_items m ON m.id=w.media_id AND m.media_type='movie' LEFT JOIN external_ids tmdb ON tmdb.media_id=m.id AND tmdb.provider='tmdb' LEFT JOIN external_ids imdb ON imdb.media_id=m.id AND imdb.provider='imdb' WHERE w.deleted_at IS NULL ORDER BY w.watched_at_utc,w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []watch{}
	for rows.Next() {
		var item watch
		var raw string
		if err := rows.Scan(&item.id, &item.mediaID, &item.title, &item.year, &raw, &item.tmdbID, &item.imdbID); err != nil {
			return nil, err
		}
		item.stamp, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *Service) loadCurrentMetadata(ctx context.Context, row *exportRow) error {
	err := s.db.QueryRowContext(ctx, `SELECT rating FROM ratings WHERE media_id=?`, row.mediaID).Scan(&row.rating)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	err = s.db.QueryRowContext(ctx, `SELECT body FROM reviews WHERE media_id=?`, row.mediaID).Scan(&row.review)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	err = s.db.QueryRowContext(ctx, `SELECT rating_revision,review_revision FROM letterboxd_media_changes WHERE media_id=?`, row.mediaID).Scan(&row.ratingRevision, &row.reviewRevision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}
func (s *Service) hasUnconfirmedEvents(ctx context.Context, eventIDs []int64) (bool, error) {
	for _, id := range eventIDs {
		var exists int
		err := s.db.QueryRowContext(ctx, `SELECT 1 FROM letterboxd_batch_events e JOIN letterboxd_batch_rows r ON r.id=e.batch_row_id JOIN letterboxd_batches b ON b.id=r.batch_id WHERE e.watch_event_id=? AND b.state='confirmed' LIMIT 1`, id).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
	}
	return false, nil
}
func (s *Service) metadataPending(ctx context.Context, row exportRow) (bool, error) {
	if row.ratingRevision == 0 && row.reviewRevision == 0 {
		return false, nil
	}
	var rating, review int
	err := s.db.QueryRowContext(ctx, `SELECT r.rating_revision,r.review_revision FROM letterboxd_batch_rows r JOIN letterboxd_batches b ON b.id=r.batch_id WHERE r.media_id=? AND b.state='confirmed' ORDER BY b.confirmed_at DESC,b.id DESC,r.id DESC LIMIT 1`, row.mediaID).Scan(&rating, &review)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return row.ratingRevision > rating || row.reviewRevision > review, nil
}

func chunkCSV(rows []exportRow, maxBytes int) ([][]byte, error) {
	header := []string{"tmdbID", "imdbID", "Title", "Year", "Rating", "WatchedDate", "Rewatch", "Review"}
	headerBytes, err := encodeRecord(header)
	if err != nil {
		return nil, err
	}
	files := [][]byte{}
	current := append([]byte(nil), headerBytes...)
	for _, row := range rows {
		record := row.record()
		encoded, err := encodeRecord(record)
		if err != nil {
			return nil, err
		}
		if len(headerBytes)+len(encoded) >= maxBytes {
			return nil, fmt.Errorf("Letterboxd row exceeds maximum file size")
		}
		if len(current)+len(encoded) >= maxBytes {
			files = append(files, current)
			current = append([]byte(nil), headerBytes...)
		}
		current = append(current, encoded...)
	}
	if len(current) > len(headerBytes) {
		files = append(files, current)
	}
	return files, nil
}
func encodeRecord(record []string) ([]byte, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write(record); err != nil {
		return nil, err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
func (r exportRow) record() []string {
	tmdb, imdb, title, year := r.tmdbID, "", "", ""
	if tmdb == "" {
		imdb = r.imdbID
	}
	if tmdb == "" && imdb == "" {
		title = r.title
		if r.year.Valid {
			year = strconv.FormatInt(r.year.Int64, 10)
		}
	}
	rating := ""
	if r.rating.Valid {
		rating = strconv.FormatFloat(float64(r.rating.Int64)/2, 'f', 1, 64)
	}
	review := ""
	if r.review.Valid {
		review = r.review.String
	}
	return []string{tmdb, imdb, title, year, rating, r.watchedDate, strconv.FormatBool(r.rewatch), review}
}
