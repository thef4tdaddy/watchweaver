package server

import (
	"encoding/json"
	"net/http"
	"sync"
)

type healthResponse struct {
	Status string `json:"status"`
}

type Readiness struct {
	mu    sync.RWMutex
	ready bool
}

func NewReadiness() *Readiness {
	return &Readiness{}
}

func (r *Readiness) MarkReady() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ready = true
}

func (r *Readiness) IsReady() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ready
}

func NewHandler(readiness *Readiness) http.Handler {
	if readiness == nil {
		readiness = NewReadiness()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz)
	mux.HandleFunc("/readyz", readyz(readiness))
	return mux
}

func healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
}

func readyz(readiness *Readiness) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if readiness.IsReady() {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(healthResponse{Status: "ready"})
			return
		}

		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(healthResponse{Status: "not_ready"})
	}
}
