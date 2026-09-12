package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"github.com/thef4tdaddy/watchweaver/internal/workflow"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// BotConfig is intentionally independent from the web/admin configuration API.
// TokenProvider is read per request so file-secret replacement revokes old access.
type BotConfig struct {
	Notifications bool
	TokenProvider func() (string, error)
	Users         []string
	PublicURL     string
}

func BotTokenFile(path string) func() (string, error) {
	return func() (string, error) {
		b, e := os.ReadFile(path)
		if e != nil {
			return "", errors.New("bot credential unavailable")
		}
		s := strings.TrimSpace(string(b))
		if len(s) < 32 {
			return "", errors.New("bot credential must contain at least 32 characters")
		}
		return s, nil
	}
}
func NewBotHandler(api *API, cfg BotConfig) (http.Handler, error) {
	u, err := url.Parse(cfg.PublicURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("bot public URL must be an HTTP(S) instance origin without credentials")
	}
	if cfg.TokenProvider == nil || len(cfg.Users) == 0 {
		return nil, errors.New("bot token and authorized users are required")
	}
	if _, err = cfg.TokenProvider(); err != nil {
		return nil, err
	}
	users := map[string]bool{}
	for _, id := range cfg.Users {
		id = strings.TrimSpace(id)
		if id == "" || strings.Trim(id, "0123456789") != "" {
			return nil, errors.New("invalid Discord user ID")
		}
		users[id] = true
	}
	base := strings.TrimRight(cfg.PublicURL, "/")
	mux := http.NewServeMux()
	api.registerBotDelivery(mux, cfg.Notifications)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { notFound(w) })
	mux.HandleFunc("GET /api/bot/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		caps := []string{"inbox.read", "history.read", "media.read", "status.read", "workflow.write"}
		if api.traktSync != nil {
			caps = append(caps, "trakt.sync")
		}
		if cfg.Notifications {
			caps = append(caps, "notifications.claim")
		}
		writeJSON(w, 200, map[string]any{"api_version": "1", "capabilities": caps, "links": map[string]string{"inbox": base + "/inbox", "history": base + "/history", "letterboxd": base + "/letterboxd", "serializd": base + "/serializd", "settings": base + "/settings"}})
	})
	// Register exact read routes only. Never mount the web API or its catch-all here.
	reads := map[string]http.HandlerFunc{"media": api.searchMedia, "inbox": api.inbox, "history": api.history, "status": api.operationalStatus, "integrations": api.integrationStatus, "letterboxd": api.letterboxdStatus, "serializd": api.serializdStatus}
	for name, handler := range reads {
		mux.HandleFunc("GET /api/bot/v1/"+name, handler)
	}
	for _, kind := range []string{"media", "tasks"} {
		kind := kind
		mux.HandleFunc("GET /api/bot/v1/"+kind+"/{id}", func(w http.ResponseWriter, r *http.Request) {
			id, ok := positiveID(w, r.PathValue("id"))
			if ok {
				api.resourceDetail(w, r, id, kind == "tasks")
			}
		})
	}

	mux.HandleFunc("POST /api/bot/v1/actions", func(w http.ResponseWriter, r *http.Request) {
		var c workflow.Command
		if !decodeJSON(w, r, &c) {
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if key == "" || len(key) > 128 || c.Revision == nil || (c.Target == "task" && c.Action == "complete" && c.MediaRevision == nil) {
			badRequest(w, "idempotency key and resource revisions are required")
			return
		}
		out, err := workflow.New(api.db).Apply(r.Context(), c, r.Header.Get("X-Discord-User-ID"), key)
		workflowResponse(w, out, err)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, err := cfg.TokenProvider()
		if err != nil || len(token) < 32 {
			writeJSON(w, 503, map[string]string{"error": "bot credential unavailable"})
			return
		}
		expected := sha256.Sum256([]byte("Bearer " + token))
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
			writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		if !users[r.Header.Get("X-Discord-User-ID")] {
			writeJSON(w, 403, map[string]string{"error": "actor not authorized"})
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
}
