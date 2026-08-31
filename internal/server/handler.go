package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

type healthResponse struct {
	Status string `json:"status"`
}

func NewHandler() http.Handler {
	return newHandler(discoverStaticAssetsFS())
}

func newHandler(staticAssets fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthz)
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
