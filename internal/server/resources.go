package server

import (
	"context"
	"database/sql"
	"errors"
	"github.com/thef4tdaddy/watchweaver/internal/workflow"
	"net/http"
	"strconv"
	"strings"
)

func (a *API) loadMedia(ctx context.Context, id int64) (mediaJSON, error) {
	var m mediaJSON
	var year sql.NullString
	var season, episode, seasonID sql.NullInt64
	err := a.db.QueryRowContext(ctx, `SELECT m.id,m.media_type,m.title,m.year,
 CASE WHEN m.media_type='episode' THEN p.season_number ELSE m.season_number END,m.episode_number,
 CASE WHEN m.media_type='episode' THEN p.id END,
 CASE WHEN m.media_type='season' THEN p.title WHEN m.media_type='episode' THEN gp.title ELSE '' END
 FROM media_items m LEFT JOIN media_items p ON p.id=m.parent_id LEFT JOIN media_items gp ON gp.id=p.parent_id WHERE m.id=?`, id).Scan(&m.ID, &m.Type, &m.Title, &year, &season, &episode, &seasonID, &m.ShowTitle)
	if err != nil {
		return m, err
	}
	setOptionalMediaFields(&m, year, season, episode)
	if seasonID.Valid {
		m.SeasonID = &seasonID.Int64
	}
	m.ExternalIDs = a.externalIDs(ctx, id)
	return m, nil
}
func (a *API) resourceDetail(w http.ResponseWriter, r *http.Request, id int64, task bool) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	mediaID := id
	var t taskJSON
	var taskRevision int64
	if task {
		var snooze sql.NullString
		err := a.db.QueryRowContext(r.Context(), `SELECT id,media_id,task_type,state,snoozed_until,created_at,revision FROM prompt_tasks WHERE id=?`, id).Scan(&t.ID, &mediaID, &t.Type, &t.State, &snooze, &t.CreatedAt, &taskRevision)
		if err == sql.ErrNoRows {
			notFound(w)
			return
		}
		if err != nil {
			internalError(w)
			return
		}
		if snooze.Valid {
			t.SnoozedUntil = &snooze.String
		}
	}
	m, err := a.loadMedia(r.Context(), mediaID)
	if err == sql.ErrNoRows {
		notFound(w)
		return
	}
	if err != nil {
		internalError(w)
		return
	}
	var revision int64
	var rating sql.NullInt64
	var review sql.NullString
	if err := a.db.QueryRowContext(r.Context(), `SELECT m.revision,ra.rating,re.body FROM media_items m LEFT JOIN ratings ra ON ra.media_id=m.id LEFT JOIN reviews re ON re.media_id=m.id WHERE m.id=?`, mediaID).Scan(&revision, &rating, &review); err != nil {
		internalError(w)
		return
	}
	response := map[string]any{"media": m, "media_revision": revision, "revision": revision}
	if rating.Valid {
		response["rating"] = rating.Int64
	}
	if review.Valid {
		response["review"] = review.String
	}
	if task {
		response["revision"] = taskRevision
	}
	var ignored bool
	if err := a.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM prompt_ignored_media WHERE media_id=?)`, mediaID).Scan(&ignored); err != nil {
		internalError(w)
		return
	}
	response["ignored"] = ignored
	rows, err := a.db.QueryContext(r.Context(), `SELECT id FROM media_items WHERE parent_id=? ORDER BY season_number,episode_number,id`, mediaID)
	if err != nil {
		internalError(w)
		return
	}
	ids := []int64{}
	for rows.Next() {
		var childID int64
		if err = rows.Scan(&childID); err != nil {
			rows.Close()
			internalError(w)
			return
		}
		ids = append(ids, childID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		internalError(w)
		return
	}
	children := []mediaJSON{}
	for _, childID := range ids {
		child, err := a.loadMedia(r.Context(), childID)
		if err != nil {
			internalError(w)
			return
		}
		children = append(children, child)
	}
	response["children"] = children

	if task {
		t.Media = m
		t.Revision = taskRevision
		t.MediaRevision = revision
		response["task"] = t
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *API) applyWorkflow(w http.ResponseWriter, r *http.Request, c workflow.Command) {
	if raw := r.Header.Get("If-Match"); raw != "" {
		v, err := strconv.ParseInt(strings.Trim(raw, `"`), 10, 64)
		if err != nil || v < 1 {
			badRequest(w, "invalid resource revision")
			return
		}
		c.Revision = &v
	}
	out, err := workflow.New(a.db).Apply(r.Context(), c, "", "")
	workflowResponse(w, out, err)
}
func workflowResponse(w http.ResponseWriter, out workflow.Result, err error) {
	switch {
	case errors.Is(err, workflow.ErrConflict):
		conflict(w, err.Error())
	case errors.Is(err, workflow.ErrNotFound):
		notFound(w)
	case errors.Is(err, workflow.ErrInvalid):
		badRequest(w, err.Error())
	case err != nil:
		internalError(w)
	default:
		writeJSON(w, http.StatusOK, out)
	}
}

func (a *API) searchMedia(w http.ResponseWriter, r *http.Request) {
	page, perPage, ok := pagination(w, r)
	if !ok {
		return
	}
	q := "%" + r.URL.Query().Get("q") + "%"
	kind := r.URL.Query().Get("type")
	if kind != "" && kind != "movie" && kind != "show" && kind != "season" && kind != "episode" {
		badRequest(w, "invalid media type")
		return
	}
	where := ` FROM media_items m LEFT JOIN media_items p ON p.id=m.parent_id LEFT JOIN media_items gp ON gp.id=p.parent_id WHERE (m.title LIKE ? OR p.title LIKE ? OR gp.title LIKE ?) AND (?='' OR m.media_type=?)`
	var total int
	if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*)`+where, q, q, q, kind, kind).Scan(&total); err != nil {
		internalError(w)
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT m.id`+where+` ORDER BY m.title,m.id LIMIT ? OFFSET ?`, q, q, q, kind, kind, perPage, (page-1)*perPage)
	if err != nil {
		internalError(w)
		return
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			internalError(w)
			return
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		internalError(w)
		return
	}
	items := []mediaJSON{}
	for _, id := range ids {
		m, err := a.loadMedia(r.Context(), id)
		if err != nil {
			internalError(w)
			return
		}
		items = append(items, m)
	}
	writeJSON(w, 200, newPage(page, perPage, total, items))
}

func (a *API) editMedia(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != "POST" {
		methodNotAllowed(w)
		return
	}
	var body struct {
		Rating   *int   `json:"rating"`
		Review   string `json:"review"`
		Revision *int64 `json:"revision"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Revision == nil {
		badRequest(w, "revision is required")
		return
	}
	c := workflow.Command{Target: "media", ID: id, Action: "edit", Rating: body.Rating, Revision: body.Revision, ClearRating: body.Rating == nil}
	if strings.TrimSpace(body.Review) == "" {
		c.ClearReview = true
	} else {
		c.Review = &body.Review
	}
	a.applyWorkflow(w, r, c)
}
