package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/thef4tdaddy/watchweaver/internal/jellyfin"
)

const jellyfinTokenKey = "ingest_token"

func (a *API) jellyfinConfig(w http.ResponseWriter, r *http.Request) {
	if a.credentials == nil || a.jellyfin == nil {
		writeError(w, http.StatusServiceUnavailable, "credential storage is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		configured, err := a.credentials.Configured(r.Context(), "jellyfin", jellyfinTokenKey)
		if err != nil { internalError(w); return }
		status, err := a.jellyfin.Status(r.Context(), configured)
		if err != nil { internalError(w); return }
		writeJSON(w, http.StatusOK, status)
	case http.MethodPost:
		token, err := newJellyfinToken()
		if err != nil { internalError(w); return }
		if err := a.credentials.Set(r.Context(), "jellyfin", jellyfinTokenKey, token); err != nil { internalError(w); return }
		writeJSON(w, http.StatusCreated, map[string]any{"configured": true, "token": token})
	case http.MethodDelete:
		if err := a.credentials.DeleteIntegration(r.Context(), "jellyfin"); err != nil { internalError(w); return }
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}

func (a *API) jellyfinIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { methodNotAllowed(w); return }
	if a.credentials == nil || a.jellyfin == nil { writeError(w, http.StatusServiceUnavailable, "Jellyfin ingestion is unavailable"); return }
	expected, err := a.credentials.Get(r.Context(), "jellyfin", jellyfinTokenKey)
	if err != nil { internalError(w); return }
	provided := bearerToken(r.Header.Get("Authorization"))
	if expected == "" || provided == "" || !secureEqual(expected, provided) {
		a.jellyfin.RecordAuthFailure(r.Context())
		writeError(w, http.StatusUnauthorized, "invalid Jellyfin ingestion token")
		return
	}
	var event jellyfin.Event
	if !decodeJSON(w, r, &event) { a.jellyfin.RecordRejection(r.Context(), "invalid_json"); return }
	result, err := a.jellyfin.Accept(r.Context(), event)
	if errors.Is(err, jellyfin.ErrInvalidEvent) { badRequest(w, err.Error()); return }
	if errors.Is(err, jellyfin.ErrEventConflict) { conflict(w, err.Error()); return }
	if err != nil { internalError(w, err); return }
	status := http.StatusCreated
	if result.Duplicate { status = http.StatusOK }
	writeJSON(w, status, result)
}

func newJellyfinToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil { return "", err }
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func bearerToken(value string) string {
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") { return "" }
	return parts[1]
}

func secureEqual(a, b string) bool {
	if len(a) != len(b) { return false }
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
