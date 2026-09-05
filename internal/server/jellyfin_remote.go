package server

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"github.com/thef4tdaddy/watchweaver/internal/jellyfinremote"
)

const jellyfinRemoteCredential = "jellyfin_remote"

func LoadJellyfinRemoteConfig(ctx context.Context, db *sql.DB, apiKey string) jellyfinremote.Config {
	cfg := jellyfinremote.Config{APIKey: apiKey}
	rows, err := db.QueryContext(ctx, `SELECT setting_key,setting_value FROM app_settings WHERE setting_key IN ('jellyfin_remote_enabled','jellyfin_remote_url','jellyfin_remote_user_id')`)
	if err != nil {
		return cfg
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if rows.Scan(&key, &value) != nil {
			continue
		}
		switch key {
		case "jellyfin_remote_enabled":
			cfg.Enabled = value == "true"
		case "jellyfin_remote_url":
			cfg.URL = value
		case "jellyfin_remote_user_id":
			cfg.UserID = value
		}
	}
	return cfg
}

func (a *API) jellyfinRemoteConfig(w http.ResponseWriter, r *http.Request) {
	if a.credentials == nil || a.jellyfinRemote == nil {
		writeError(w, http.StatusServiceUnavailable, "remote Jellyfin connection is unavailable")
		return
	}
	currentKey, err := a.credentials.Get(r.Context(), jellyfinRemoteCredential, "api_key")
	if err != nil {
		internalError(w, err)
		return
	}
	current := LoadJellyfinRemoteConfig(r.Context(), a.db, currentKey)
	switch r.Method {
	case http.MethodGet:
		status := a.jellyfinRemote.Status()
		writeJSON(w, http.StatusOK, map[string]any{"configured": status.Configured, "enabled": current.Enabled, "url": current.URL, "user_id": current.UserID, "connected": status.Connected, "last_connected_at": status.LastConnectedAt, "last_event_at": status.LastEventAt, "last_error": status.LastError, "reconnect_count": status.ReconnectCount, "events_received": status.EventsReceived, "protocol_version": 1})
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
		body.URL = strings.TrimRight(strings.TrimSpace(body.URL), "/")
		body.UserID = strings.TrimSpace(body.UserID)
		body.APIKey = strings.TrimSpace(body.APIKey)
		if body.APIKey != "" {
			currentKey = body.APIKey
		}
		if body.Enabled && (body.URL == "" || currentKey == "") {
			badRequest(w, "Jellyfin URL and API key are required when enabled")
			return
		}
		updates := map[string]string{}
		if body.APIKey != "" {
			updates["api_key"] = body.APIKey
		}
		err = a.credentials.Update(r.Context(), jellyfinRemoteCredential, updates, func(ctx context.Context, tx *sql.Tx) error {
			for key, value := range map[string]string{"jellyfin_remote_enabled": boolString(body.Enabled), "jellyfin_remote_url": body.URL, "jellyfin_remote_user_id": body.UserID} {
				if _, err := tx.ExecContext(ctx, `INSERT INTO app_settings(setting_key,setting_value) VALUES(?,?) ON CONFLICT(setting_key) DO UPDATE SET setting_value=excluded.setting_value,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`, key, value); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			internalError(w, err)
			return
		}
		cfg := jellyfinremote.Config{Enabled: body.Enabled, URL: body.URL, UserID: body.UserID, APIKey: currentKey}
		a.jellyfinRemote.Configure(cfg)
		writeJSON(w, http.StatusOK, map[string]any{"configured": cfg.URL != "" && cfg.APIKey != "", "enabled": cfg.Enabled, "url": cfg.URL, "user_id": cfg.UserID})
	case http.MethodDelete:
		if err := a.credentials.DeleteIntegration(r.Context(), jellyfinRemoteCredential); err != nil {
			internalError(w, err)
			return
		}
		_, _ = a.db.ExecContext(r.Context(), `DELETE FROM app_settings WHERE setting_key IN ('jellyfin_remote_enabled','jellyfin_remote_url','jellyfin_remote_user_id')`)
		a.jellyfinRemote.Configure(jellyfinremote.Config{})
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
	if a.credentials == nil || a.jellyfinRemote == nil {
		writeError(w, http.StatusServiceUnavailable, "remote Jellyfin connection is unavailable")
		return
	}
	key, err := a.credentials.Get(r.Context(), jellyfinRemoteCredential, "api_key")
	if err != nil {
		internalError(w, err)
		return
	}
	cfg := LoadJellyfinRemoteConfig(r.Context(), a.db, key)
	version, err := a.jellyfinRemote.Test(r.Context(), cfg)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "server_version": version})
}
func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
