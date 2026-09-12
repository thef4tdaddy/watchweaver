package server

import (
	"context"
	"net/http"
	"time"
)

func (a *API) ConfigureBotAccess(enabled, overridden bool) {
	a.botEnabled = enabled
	a.botTokenOverridden = overridden
}
func (a *API) StoredBotToken() (string, error) {
	if a.credentials == nil {
		return "", nil
	}
	return a.credentials.Get(context.Background(), "discord_bot", "token")
}
func (a *API) botPairing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == "GET" {
		configured := false
		if a.credentials != nil {
			var err error
			configured, err = a.credentials.Configured(r.Context(), "discord_bot", "token")
			if err != nil {
				internalError(w)
				return
			}
		}
		var last string
		_ = a.db.QueryRowContext(r.Context(), `SELECT state_value FROM integration_state WHERE integration='discord_bot' AND state_key='last_handshake'`).Scan(&last)
		writeJSON(w, 200, map[string]any{"enabled": a.botEnabled, "configured": configured || a.botTokenOverridden, "overridden": a.botTokenOverridden, "last_handshake": last})
		return
	}
	if !a.botEnabled || a.credentials == nil {
		conflict(w, "enable the protected bot listener before pairing")
		return
	}
	if a.botTokenOverridden {
		conflict(w, "token is managed by deployment configuration")
		return
	}
	switch r.Method {
	case "POST":
		token := randomBotID() + randomBotID()
		if err := a.credentials.Set(r.Context(), "discord_bot", "token", token); err != nil {
			internalError(w)
			return
		}
		writeJSON(w, 201, map[string]string{"token": token})
	case "DELETE":
		if err := a.credentials.DeleteIntegration(r.Context(), "discord_bot"); err != nil {
			internalError(w)
			return
		}
		w.WriteHeader(204)
	default:
		methodNotAllowed(w)
	}
}
func (a *API) botHandshake(w http.ResponseWriter, r *http.Request, base string, notifications bool) {
	var body struct {
		Protocol string `json:"protocol_version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Protocol != "1" {
		writeJSON(w, 409, map[string]any{"error": "incompatible bot protocol", "supported_versions": []string{"1"}})
		return
	}
	_, err := a.db.ExecContext(r.Context(), `INSERT INTO integration_state(integration,state_key,state_value) VALUES('discord_bot','last_handshake',?) ON CONFLICT(integration,state_key) DO UPDATE SET state_value=excluded.state_value`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		internalError(w)
		return
	}
	permissions := []string{"read", "workflow.write"}
	if a.traktSync != nil {
		permissions = append(permissions, "sync.trakt")
	}
	writeJSON(w, 200, map[string]any{"connected": true, "protocol_version": "1", "app_version": a.version, "public_url": base, "notifications_enabled": notifications, "capabilities_url": "/api/bot/v1/capabilities", "permissions": permissions, "app_only": []string{"settings", "credentials", "administration", "letterboxd.workflow", "serializd.workflow"}})
}
