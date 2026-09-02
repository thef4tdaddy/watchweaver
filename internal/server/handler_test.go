package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestHealthzReturnsOK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	NewHandler(NewReadiness()).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status payload ok, got %q", body["status"])
	}
}

func TestHealthzRejectsNonGET(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rr := httptest.NewRecorder()
	NewHandler(NewReadiness()).ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rr.Code)
	}
}

func TestReadyzReturnsServiceUnavailableBeforeReady(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	NewHandler(NewReadiness()).ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", rr.Code)
	}
}

func TestReadyzReturnsOKAfterReady(t *testing.T) {
	readiness := NewReadiness()
	readiness.MarkReady()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	NewHandler(readiness).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
}

func TestRootNotFoundWithoutStaticAssets(t *testing.T) {
	rr := httptest.NewRecorder()
	newHandler(NewReadiness(), nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestProductionHandlerServesEmbeddedFrontend(t *testing.T) {
	rr := httptest.NewRecorder()
	NewHandler(NewReadiness()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected embedded frontend status 200, got %d", rr.Code)
	}
	if contentType := rr.Header().Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Fatalf("expected HTML content type, got %q", contentType)
	}
}

func TestStaticAssetIsServed(t *testing.T) {
	h := newHandler(NewReadiness(), fstest.MapFS{"index.html": {Data: []byte("index")}, "assets/app.js": {Data: []byte("app")}})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "app" {
		t.Fatalf("expected static asset, got %d %q", rr.Code, rr.Body.String())
	}
}

func TestSPAFallbackServesIndex(t *testing.T) {
	h := newHandler(NewReadiness(), fstest.MapFS{"index.html": {Data: []byte("WatchWeaver SPA")}})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/settings", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "WatchWeaver SPA" {
		t.Fatalf("expected SPA fallback, got %d %q", rr.Code, rr.Body.String())
	}
}

func TestSPARejectsUnsupportedMethod(t *testing.T) {
	h := newHandler(NewReadiness(), fstest.MapFS{"index.html": {Data: []byte("index")}})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/settings", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}
