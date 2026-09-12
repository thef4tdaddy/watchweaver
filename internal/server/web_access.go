package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
)

// ProtectWebAccess prevents the bot from bypassing scoped routes through a
// host-published web listener. Its independent password never goes to the bot.
func ProtectWebAccess(next http.Handler, password, botToken func() (string, error)) (http.Handler, error) {
	if password == nil || botToken == nil {
		return nil, errors.New("separate web password required with bot API")
	}
	secret, err := password()
	if err != nil || len(secret) < 32 {
		return nil, errors.New("web password unavailable or too short")
	}
	token, err := botToken()
	if err != nil || secret == token {
		return nil, errors.New("web password must differ from bot token")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Jellyfin ingestion validates its own distinct bearer credential.
		if r.URL.Path == "/api/v1/ingest/jellyfin/events" {
			next.ServeHTTP(w, r)
			return
		}
		// Minimal health responses remain public; no database or integration detail.
		if (r.URL.Path == "/healthz" || r.URL.Path == "/readyz") && (r.Method == "GET" || r.Method == "HEAD") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		secret, err := password()
		token, tokenErr := botToken()
		if err != nil || tokenErr != nil || len(secret) < 32 || secret == token {
			http.Error(w, "web access configuration unavailable", 503)
			return
		}
		user, provided, ok := r.BasicAuth()
		expected := sha256.Sum256([]byte(secret))
		actual := sha256.Sum256([]byte(provided))
		if !ok || user != "watchweaver" || subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="WatchWeaver", charset="UTF-8"`)
			http.Error(w, "authentication required", 401)
			return
		}
		// Browser HTTP Basic authentication is ambient, so reject cross-origin writes.
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				http.Error(w, "cross-origin write rejected", 403)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != "https://"+r.Host && origin != "http://"+r.Host {
				http.Error(w, "cross-origin write rejected", 403)
				return
			}
		}
		next.ServeHTTP(w, r)
	}), nil
}
