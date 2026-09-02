package server

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (a *API) setupStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if a.credentials == nil {
		writeJSON(w, http.StatusOK, map[string]any{"complete": a.trakt != nil && a.trakt.Status(r.Context()).Status != "not_configured", "encrypted_storage": false})
		return
	}
	traktConfigured, err := a.credentials.Configured(r.Context(), "trakt", "client_id", "client_secret")
	if err != nil {
		internalError(w)
		return
	}
	discordConfigured, err := a.credentials.Configured(r.Context(), "discord", "webhook_url")
	if err != nil {
		internalError(w)
		return
	}
	authorization := "not_configured"
	if a.trakt != nil {
		authorization = string(a.trakt.Status(r.Context()).Status)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"complete":          traktConfigured && authorization == "connected",
		"encrypted_storage": true,
		"trakt":             map[string]any{"configured": traktConfigured, "authorization_status": authorization, "client_id_overridden": a.credentials.IsOverridden("trakt", "client_id"), "client_secret_overridden": a.credentials.IsOverridden("trakt", "client_secret")},
		"discord":           map[string]any{"configured": discordConfigured, "enabled": a.discord != nil && a.discord.Configured(), "webhook_overridden": a.credentials.IsOverridden("discord", "webhook_url")},
	})
}

func (a *API) traktConfig(w http.ResponseWriter, r *http.Request) {
	if a.credentials == nil || a.trakt == nil {
		writeError(w, http.StatusServiceUnavailable, "credential storage is unavailable")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var body struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		body.ClientID, body.ClientSecret = strings.TrimSpace(body.ClientID), strings.TrimSpace(body.ClientSecret)
		if (body.ClientID != "" && a.credentials.IsOverridden("trakt", "client_id")) || (body.ClientSecret != "" && a.credentials.IsOverridden("trakt", "client_secret")) {
			conflict(w, "Trakt credentials are overridden by the server environment")
			return
		}
		currentID, currentSecret, _ := a.effectiveTraktCredentials(r.Context())
		if body.ClientID != "" {
			currentID = body.ClientID
		}
		if body.ClientSecret != "" {
			currentSecret = body.ClientSecret
		}
		if currentID == "" || currentSecret == "" {
			badRequest(w, "both Trakt client ID and client secret are required")
			return
		}
		if body.ClientID != "" {
			if err := a.credentials.Set(r.Context(), "trakt", "client_id", body.ClientID); err != nil {
				internalError(w)
				return
			}
		}
		if body.ClientSecret != "" {
			if err := a.credentials.Set(r.Context(), "trakt", "client_secret", body.ClientSecret); err != nil {
				internalError(w)
				return
			}
		}
		a.trakt.Configure(currentID, currentSecret)
		writeJSON(w, http.StatusOK, map[string]any{"configured": true})
	case http.MethodDelete:
		if a.credentials.IsOverridden("trakt", "client_id") || a.credentials.IsOverridden("trakt", "client_secret") {
			conflict(w, "Trakt credentials are overridden by the server environment")
			return
		}
		if err := a.credentials.DeleteIntegration(r.Context(), "trakt"); err != nil {
			internalError(w)
			return
		}
		_, _ = a.db.ExecContext(r.Context(), `DELETE FROM integration_state WHERE integration='trakt' AND state_key IN ('access_token','refresh_token','reauth_required')`)
		a.trakt.Configure("", "")
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}

func (a *API) effectiveTraktCredentials(ctx context.Context) (string, string, bool) {
	clientID, err := a.credentials.Get(ctx, "trakt", "client_id")
	if err != nil {
		return "", "", false
	}
	secret, err := a.credentials.Get(ctx, "trakt", "client_secret")
	return clientID, secret, err == nil && clientID != "" && secret != ""
}

func (a *API) discordConfig(w http.ResponseWriter, r *http.Request) {
	if a.credentials == nil || a.discord == nil {
		writeError(w, http.StatusServiceUnavailable, "credential storage is unavailable")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var body struct {
			WebhookURL string `json:"webhook_url"`
			Enabled    bool   `json:"enabled"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		body.WebhookURL = strings.TrimSpace(body.WebhookURL)
		if body.WebhookURL != "" {
			parsed, err := url.Parse(body.WebhookURL)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
				badRequest(w, "Discord webhook must be a valid HTTPS URL")
				return
			}
			if a.credentials.IsOverridden("discord", "webhook_url") {
				conflict(w, "Discord webhook is overridden by the server environment")
				return
			}
			if err := a.credentials.Set(r.Context(), "discord", "webhook_url", body.WebhookURL); err != nil {
				internalError(w)
				return
			}
		}
		webhook, err := a.credentials.Get(r.Context(), "discord", "webhook_url")
		if err != nil {
			internalError(w)
			return
		}
		if body.Enabled && webhook == "" {
			badRequest(w, "Discord webhook URL is required when enabled")
			return
		}
		if _, err := a.db.ExecContext(r.Context(), `INSERT INTO app_settings(setting_key,setting_value) VALUES('discord_enabled',?) ON CONFLICT(setting_key) DO UPDATE SET setting_value=excluded.setting_value,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`, strconv.FormatBool(body.Enabled)); err != nil {
			internalError(w)
			return
		}
		if body.Enabled {
			a.discord.Configure(webhook)
		} else {
			a.discord.Configure("")
		}
		writeJSON(w, http.StatusOK, map[string]any{"configured": webhook != "", "enabled": body.Enabled})
	case http.MethodDelete:
		if a.credentials.IsOverridden("discord", "webhook_url") {
			conflict(w, "Discord webhook is overridden by the server environment")
			return
		}
		if err := a.credentials.DeleteIntegration(r.Context(), "discord"); err != nil {
			internalError(w)
			return
		}
		_, _ = a.db.ExecContext(r.Context(), `INSERT INTO app_settings(setting_key,setting_value) VALUES('discord_enabled','false') ON CONFLICT(setting_key) DO UPDATE SET setting_value='false'`)
		a.discord.Configure("")
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}

func (a *API) discordTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if a.discord == nil || !a.discord.Configured() {
		badRequest(w, "Discord is not enabled and configured")
		return
	}
	if err := a.discord.Test(r.Context()); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func DiscordEnabled(ctx context.Context, db *sql.DB) bool {
	enabled, _ := DiscordPreference(ctx, db)
	return enabled
}

func DiscordPreference(ctx context.Context, db *sql.DB) (bool, bool) {
	var raw string
	err := db.QueryRowContext(ctx, `SELECT setting_value FROM app_settings WHERE setting_key='discord_enabled'`).Scan(&raw)
	return err == nil && raw == "true", err == nil
}
