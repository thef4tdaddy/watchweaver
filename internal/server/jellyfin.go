package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/jellyfin"
)

const jellyfinTokenKey = "ingest_token"

func (a *API) jellyfinService() *jellyfin.Service { return jellyfin.NewService(a.db) }

func (a *API) jellyfinConfig(w http.ResponseWriter, r *http.Request) {
	if a.credentials == nil {
		writeError(w, http.StatusServiceUnavailable, "credential storage is unavailable")
		return
	}
	svc := a.jellyfinService()
	switch r.Method {
	case http.MethodGet:
		configured, err := a.credentials.Configured(r.Context(), "jellyfin", jellyfinTokenKey)
		if err != nil {
			internalError(w)
			return
		}
		status, err := svc.Status(r.Context(), configured)
		if err != nil {
			internalError(w)
			return
		}
		writeJSON(w, http.StatusOK, status)
	case http.MethodPost:
		token, err := newJellyfinToken()
		if err != nil {
			internalError(w)
			return
		}
		if err := a.credentials.Set(r.Context(), "jellyfin", jellyfinTokenKey, token); err != nil {
			internalError(w)
			return
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, _ = a.db.ExecContext(r.Context(), `INSERT INTO integration_state(integration,state_key,state_value) VALUES('jellyfin','token_rotated_at',?) ON CONFLICT(integration,state_key) DO UPDATE SET state_value=excluded.state_value,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`, now)
		writeJSON(w, http.StatusCreated, map[string]any{"configured": true, "token": token, "protocol_version": 1, "rotated_at": now})
	case http.MethodDelete:
		if err := a.credentials.DeleteIntegration(r.Context(), "jellyfin"); err != nil {
			internalError(w)
			return
		}
		_, _ = a.db.ExecContext(r.Context(), `DELETE FROM integration_state WHERE integration='jellyfin' AND state_key='token_rotated_at'`)
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}

func (a *API) jellyfinIngest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
	w.Header().Set("Access-Control-Allow-Methods", "HEAD, POST, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("X-WatchWeaver-Protocol-Version", "1")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if a.credentials == nil {
		writeError(w, http.StatusServiceUnavailable, "Jellyfin ingestion is unavailable")
		return
	}
	svc := a.jellyfinService()
	expected, err := a.credentials.Get(r.Context(), "jellyfin", jellyfinTokenKey)
	if err != nil {
		internalError(w)
		return
	}
	provided := bearerToken(r.Header.Get("Authorization"))
	if expected == "" || provided == "" || !secureEqual(expected, provided) {
		svc.RecordAuthFailure(r.Context())
		writeError(w, http.StatusUnauthorized, "invalid Jellyfin ingestion token")
		return
	}
	var event jellyfin.Event
	if !decodeJSON(w, r, &event) {
		svc.RecordRejection(r.Context(), "invalid_json")
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" && key != event.EventID {
		svc.RecordRejection(r.Context(), "idempotency_key_mismatch")
		writeCodeError(w, http.StatusBadRequest, "idempotency_key_mismatch", "Idempotency-Key must match event_id")
		return
	}
	result, err := svc.Accept(r.Context(), event)
	if errors.Is(err, jellyfin.ErrInvalidEvent) {
		writeCodeError(w, http.StatusBadRequest, "invalid_event", err.Error())
		return
	}
	if errors.Is(err, jellyfin.ErrEventConflict) {
		svc.RecordRejection(r.Context(), "event_conflict")
		writeCodeError(w, http.StatusConflict, "event_conflict", err.Error())
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	status := http.StatusAccepted
	if result.Duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, result)
}

func writeCodeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "error": message})
}

func newJellyfinToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func bearerToken(value string) string {
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func secureEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
