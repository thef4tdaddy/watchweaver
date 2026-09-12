package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/thef4tdaddy/watchweaver/internal/credentials"
	"github.com/thef4tdaddy/watchweaver/internal/jellyfinremote"
)

const legacyJellyfinRemoteID = "default"

type jellyfinRemoteSource struct {
	ID, Name, URL, UserID, APIKey string
	Enabled                       bool
}

func remoteCredentialID(id string) string {
	if id == legacyJellyfinRemoteID {
		return "jellyfin_remote"
	}
	return "jellyfin_remote:" + id
}

func LoadJellyfinRemoteSources(ctx context.Context, db *sql.DB, store *credentials.Store, pool *jellyfinremote.Pool) error {
	rows, err := db.QueryContext(ctx, `SELECT id,name,url,user_id,enabled FROM jellyfin_remote_sources ORDER BY created_at,id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var source jellyfinRemoteSource
		var enabled int
		if err := rows.Scan(&source.ID, &source.Name, &source.URL, &source.UserID, &enabled); err != nil {
			return err
		}
		source.Enabled = enabled == 1
		source.APIKey, err = store.Get(ctx, remoteCredentialID(source.ID), "api_key")
		if err != nil {
			return err
		}
		pool.Configure(source.ID, remoteConfig(source))
	}
	return rows.Err()
}

func LoadJellyfinRemoteConfig(ctx context.Context, db *sql.DB, apiKey string) jellyfinremote.Config {
	cfg := jellyfinremote.Config{APIKey: apiKey}
	var enabled int
	if err := db.QueryRowContext(ctx, `SELECT enabled,url,user_id FROM jellyfin_remote_sources WHERE id=?`, legacyJellyfinRemoteID).Scan(&enabled, &cfg.URL, &cfg.UserID); err == nil {
		cfg.Enabled = enabled == 1
	}
	return cfg
}

func (a *API) jellyfinRemoteSources(w http.ResponseWriter, r *http.Request) {
	if a.credentials == nil || a.jellyfinRemotes == nil {
		writeError(w, http.StatusServiceUnavailable, "remote Jellyfin connections are unavailable")
		return
	}
	if r.URL.Path != "/api/integrations/jellyfin/remotes" {
		a.jellyfinRemoteSource(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		sources, err := a.remoteSources(r.Context())
		if err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sources": sources})
	case http.MethodPost:
		source, ok := decodeRemoteSource(w, r)
		if !ok {
			return
		}
		if source.APIKey == "" {
			badRequest(w, "Jellyfin API key is required")
			return
		}
		source.ID = newRemoteID()
		if err := a.saveRemoteSource(r.Context(), source, true); err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, a.publicRemote(source))
	default:
		methodNotAllowed(w)
	}
}

func (a *API) jellyfinRemoteSource(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, "/api/integrations/jellyfin/remotes/")
	if len(parts) == 0 || len(parts) > 2 {
		notFound(w)
		return
	}
	source, err := a.loadRemoteSource(r.Context(), parts[0])
	if err == sql.ErrNoRows {
		notFound(w)
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	if len(parts) == 2 {
		if parts[1] != "test" || r.Method != http.MethodPost {
			notFound(w)
			return
		}
		version, err := a.jellyfinRemotes.Test(r.Context(), remoteConfig(source))
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"connected": true, "server_version": version})
		return
	}
	switch r.Method {
	case http.MethodPut:
		updated, ok := decodeRemoteSource(w, r)
		if !ok {
			return
		}
		updated.ID = source.ID
		writeKey := updated.APIKey != ""
		if !writeKey {
			updated.APIKey = source.APIKey
		}
		if err := a.saveRemoteSource(r.Context(), updated, writeKey); err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, a.publicRemote(updated))
	case http.MethodDelete:
		if err := a.credentials.DeleteIntegration(r.Context(), remoteCredentialID(source.ID)); err != nil {
			internalError(w, err)
			return
		}
		if _, err := a.db.ExecContext(r.Context(), `DELETE FROM jellyfin_remote_sources WHERE id=?`, source.ID); err != nil {
			internalError(w, err)
			return
		}
		a.jellyfinRemotes.Remove(source.ID)
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}

func decodeRemoteSource(w http.ResponseWriter, r *http.Request) (jellyfinRemoteSource, bool) {
	var body struct {
		Name    string `json:"name"`
		URL     string `json:"url"`
		UserID  string `json:"user_id"`
		APIKey  string `json:"api_key"`
		Enabled bool   `json:"enabled"`
	}
	if !decodeJSON(w, r, &body) {
		return jellyfinRemoteSource{}, false
	}
	source := jellyfinRemoteSource{Name: strings.TrimSpace(body.Name), URL: strings.TrimRight(strings.TrimSpace(body.URL), "/"), UserID: strings.TrimSpace(body.UserID), APIKey: strings.TrimSpace(body.APIKey), Enabled: body.Enabled}
	if source.Name == "" || source.URL == "" {
		badRequest(w, "Name and Jellyfin URL are required")
		return source, false
	}
	return source, true
}

func (a *API) saveRemoteSource(ctx context.Context, source jellyfinRemoteSource, writeKey bool) error {
	values := map[string]string{}
	if writeKey {
		values["api_key"] = source.APIKey
	}
	err := a.credentials.Update(ctx, remoteCredentialID(source.ID), values, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO jellyfin_remote_sources(id,name,url,user_id,enabled) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,url=excluded.url,user_id=excluded.user_id,enabled=excluded.enabled,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`, source.ID, source.Name, source.URL, source.UserID, source.Enabled)
		return err
	})
	if err == nil {
		a.jellyfinRemotes.Configure(source.ID, remoteConfig(source))
	}
	return err
}

func (a *API) loadRemoteSource(ctx context.Context, id string) (jellyfinRemoteSource, error) {
	var source jellyfinRemoteSource
	var enabled int
	err := a.db.QueryRowContext(ctx, `SELECT id,name,url,user_id,enabled FROM jellyfin_remote_sources WHERE id=?`, id).Scan(&source.ID, &source.Name, &source.URL, &source.UserID, &enabled)
	if err != nil {
		return source, err
	}
	source.Enabled = enabled == 1
	source.APIKey, err = a.credentials.Get(ctx, remoteCredentialID(id), "api_key")
	return source, err
}

func (a *API) remoteSources(ctx context.Context) ([]map[string]any, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id FROM jellyfin_remote_sources ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		source, err := a.loadRemoteSource(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, a.publicRemote(source))
	}
	return out, rows.Err()
}

func (a *API) publicRemote(source jellyfinRemoteSource) map[string]any {
	status, _ := a.jellyfinRemotes.Status(source.ID)
	configured := source.URL != "" && source.APIKey != ""
	return map[string]any{"id": source.ID, "name": source.Name, "mode": "watchweaver_to_jellyfin", "state": remoteState(configured, source.Enabled, status), "configured": configured, "enabled": source.Enabled, "url": source.URL, "user_id": source.UserID, "connected": status.Connected, "last_attempt_at": status.LastAttemptAt, "last_connected_at": status.LastConnectedAt, "last_event_at": status.LastEventAt, "next_retry_at": status.NextRetryAt, "last_error_code": status.LastErrorCode, "last_error": status.LastError, "reconnect_count": status.ReconnectCount, "events_received": status.EventsReceived, "protocol_version": 1}
}

func remoteState(configured, enabled bool, status jellyfinremote.Status) string {
	if !configured {
		return "not_configured"
	}
	if !enabled {
		return "disabled"
	}
	if status.Connected && status.EventsReceived > 0 {
		return "receiving"
	}
	if status.Connected {
		return "connected_waiting"
	}
	if status.LastErrorCode != "" {
		return "reconnecting"
	}
	return "connecting"
}

func (a *API) jellyfinRemoteDiagnostics(ctx context.Context) []map[string]any {
	if a.jellyfinRemotes == nil {
		return nil
	}
	rows, err := a.db.QueryContext(ctx, `SELECT id,enabled FROM jellyfin_remote_sources ORDER BY created_at,id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	result := make([]map[string]any, 0)
	for rows.Next() {
		var id string
		var enabled int
		if rows.Scan(&id, &enabled) != nil {
			return result
		}
		status, ok := a.jellyfinRemotes.Status(id)
		if !ok {
			continue
		}
		result = append(result, map[string]any{"mode": "watchweaver_to_jellyfin", "enabled": enabled == 1, "connected": status.Connected, "last_attempt_at": status.LastAttemptAt, "last_connected_at": status.LastConnectedAt, "last_event_at": status.LastEventAt, "next_retry_at": status.NextRetryAt, "last_error_code": status.LastErrorCode, "reconnect_count": status.ReconnectCount, "events_received": status.EventsReceived, "protocol_version": status.ProtocolVersion})
	}
	return result
}

func remoteConfig(source jellyfinRemoteSource) jellyfinremote.Config {
	return jellyfinremote.Config{Enabled: source.Enabled, URL: source.URL, UserID: source.UserID, APIKey: source.APIKey}
}
func newRemoteID() string {
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

// Legacy read/test endpoints keep older frontends safe during rolling upgrades.
func (a *API) jellyfinRemoteConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		source, err := a.loadRemoteSource(r.Context(), legacyJellyfinRemoteID)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusOK, map[string]any{"configured": false, "enabled": false, "protocol_version": 1})
			return
		}
		if err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, a.publicRemote(source))
	case http.MethodPut:
		var body struct {
			Enabled bool   `json:"enabled"`
			URL     string `json:"url"`
			UserID  string `json:"user_id"`
			APIKey  string `json:"api_key"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		source := jellyfinRemoteSource{ID: legacyJellyfinRemoteID, Name: "Remote Jellyfin", Enabled: body.Enabled, URL: strings.TrimRight(strings.TrimSpace(body.URL), "/"), UserID: strings.TrimSpace(body.UserID), APIKey: strings.TrimSpace(body.APIKey)}
		writeKey := source.APIKey != ""
		if current, err := a.loadRemoteSource(r.Context(), source.ID); err == nil && !writeKey {
			source.APIKey = current.APIKey
		}
		if source.URL == "" || source.APIKey == "" {
			badRequest(w, "Jellyfin URL and API key are required")
			return
		}
		if err := a.saveRemoteSource(r.Context(), source, writeKey); err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, a.publicRemote(source))
	case http.MethodDelete:
		if err := a.credentials.DeleteIntegration(r.Context(), remoteCredentialID(legacyJellyfinRemoteID)); err != nil {
			internalError(w, err)
			return
		}
		_, _ = a.db.ExecContext(r.Context(), `DELETE FROM jellyfin_remote_sources WHERE id=?`, legacyJellyfinRemoteID)
		a.jellyfinRemotes.Remove(legacyJellyfinRemoteID)
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}
func (a *API) jellyfinRemoteTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	source, err := a.loadRemoteSource(r.Context(), legacyJellyfinRemoteID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "remote Jellyfin is not configured")
		return
	}
	version, err := a.jellyfinRemotes.Test(r.Context(), remoteConfig(source))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "server_version": version})
}
