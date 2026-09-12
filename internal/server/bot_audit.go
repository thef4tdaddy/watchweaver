package server

import (
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"strings"
)

type auditWriter struct {
	http.ResponseWriter
	status int
}

func (w *auditWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
		w.ResponseWriter.WriteHeader(code)
	}
}
func (w *auditWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func auditBotRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := &auditWriter{ResponseWriter: w}
		next.ServeHTTP(out, r)
		if out.status == 0 {
			out.status = 200
		}
		if r.Method == "GET" && out.status < 400 {
			return
		}
		action := "unknown"
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/bot/v1/"), "/")
		switch parts[0] {
		case "handshake", "actions", "sync", "notifications", "jobs", "capabilities", "inbox", "history", "media", "tasks", "status", "integrations", "letterboxd", "serializd":
			action = parts[0]
		}
		actor := sha256.Sum256([]byte(r.Header.Get("X-Discord-User-ID")))
		log.Printf("bot API: operation=%s actor_ref=%s status=%d", action, fmt.Sprintf("%x", actor[:6]), out.status)
	})
}
