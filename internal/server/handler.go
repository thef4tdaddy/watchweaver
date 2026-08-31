package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
)

type healthResponse struct {
	Status string `json:"status"`
}

type Readiness struct {
	mu    sync.RWMutex
	ready bool
}

func NewReadiness() *Readiness { return &Readiness{} }

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
	return newHandler(readiness, discoverStaticAssetsFS())
}

func newHandler(readiness *Readiness, staticAssets fs.FS) http.Handler {
	if readiness == nil {
		readiness = NewReadiness()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz)
	mux.HandleFunc("/readyz", readyz(readiness))
	if staticAssets != nil {
		mux.Handle("/", newSPAHandler(staticAssets))
	}
	return mux
}

func discoverStaticAssetsFS() fs.FS {
	if _, err := os.Stat("web/dist/index.html"); err != nil {
		return nil
	}
	return os.DirFS("web/dist")
}

func newSPAHandler(staticAssets fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(staticAssets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		cleanPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if cleanPath == "" || cleanPath == "." {
			cleanPath = "index.html"
		}
		if stat, err := fs.Stat(staticAssets, cleanPath); err == nil && !stat.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		fallback := r.Clone(r.Context())
		fallback.URL.Path = "/index.html"
		fileServer.ServeHTTP(w, fallback)
	})
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
