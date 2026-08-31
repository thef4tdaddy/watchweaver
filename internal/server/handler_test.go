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

	NewHandler().ServeHTTP(rr, req)

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

	NewHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rr.Code)
	}
}

func TestRootPathReturnsNotFoundWhenStaticAssetsAreUnavailable(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	newHandler(nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rr.Code)
	}
}

func TestStaticAssetIsServedWhenPresent(t *testing.T) {
	handler := newHandler(fstest.MapFS{
		"index.html":       {Data: []byte("<!doctype html><html><body>WatchWeaver</body></html>")},
		"assets/app.js":    {Data: []byte("console.log('ok')")},
		"assets/style.css": {Data: []byte("body{margin:0}")},
	})

	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if rr.Body.String() != "console.log('ok')" {
		t.Fatalf("expected app asset body, got %q", rr.Body.String())
	}
}

func TestSPAFallbackServesIndexHTMLForUnknownRoute(t *testing.T) {
	handler := newHandler(fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><html><body>WatchWeaver SPA</body></html>")},
	})

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if got := rr.Body.String(); got != "<!doctype html><html><body>WatchWeaver SPA</body></html>" {
		t.Fatalf("expected index.html fallback body, got %q", got)
	}
}
