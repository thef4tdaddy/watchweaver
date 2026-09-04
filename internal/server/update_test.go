package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompareSemverPrereleaseOrdering(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"1.0.0", "1.0.0-beta.9", 1},
		{"1.0.0-beta.10", "1.0.0-beta.9", 1},
		{"0.2.0-beta.1", "0.1.9-beta.99", 1},
		{"v1.2.3", "1.2.3", 0},
	}
	for _, tc := range cases {
		if got := compareSemver(tc.left, tc.right); got != tc.want {
			t.Errorf("compareSemver(%q,%q)=%d want %d", tc.left, tc.right, got, tc.want)
		}
	}
}

func TestUpdateStatusSelectsInstalledChannel(t *testing.T) {
	releases := `[{"tag_name":"v1.1.0","html_url":"https://example/stable","prerelease":false},{"tag_name":"v1.2.0-beta.2","html_url":"https://example/beta","prerelease":true},{"tag_name":"bad","prerelease":true}]`
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing user agent")
		}
		_, _ = w.Write([]byte(releases))
	}))
	defer remote.Close()

	for _, tc := range []struct{ version, state, latest string }{
		{"1.0.0", "stable_update_available", "1.1.0"},
		{"1.2.0-beta.1", "beta_update_available", "1.2.0-beta.2"},
		{"1.2.0-beta.3", "up_to_date", "1.2.0-beta.2"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			f := newAPIFixture(t, nil)
			f.api.SetBuildInfo(tc.version, "abc123")
			f.api.SetUpdateSource(remote.URL, remote.Client())
			body := decodeMap(t, f.request(http.MethodGet, "/api/update", ""))
			if body["state"] != tc.state || body["latest_version"] != tc.latest {
				t.Fatalf("unexpected status: %#v", body)
			}
		})
	}
}

func TestUpdateStatusFailsQuietlyAndCanBeDisabled(t *testing.T) {
	f := newAPIFixture(t, nil)
	f.api.SetBuildInfo("1.0.0", "")
	f.api.SetUpdateSource("http://127.0.0.1:1", &http.Client{})
	if body := decodeMap(t, f.request(http.MethodGet, "/api/update", "")); body["state"] != "unable" {
		t.Fatalf("unexpected offline status: %#v", body)
	}
	if _, err := f.db.Exec(`INSERT INTO app_settings(setting_key,setting_value) VALUES('update_checks_enabled','false')`); err != nil {
		t.Fatal(err)
	}
	if body := decodeMap(t, f.request(http.MethodGet, "/api/update", "")); body["state"] != "disabled" {
		t.Fatalf("unexpected disabled status: %#v", body)
	}
}

func TestUpdateStatusHandlesRateLimitAndMalformedResponses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"rate limited", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }},
		{"malformed", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"nope":`)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote := httptest.NewServer(tc.handler)
			defer remote.Close()
			f := newAPIFixture(t, nil)
			f.api.SetBuildInfo("1.0.0", "")
			f.api.SetUpdateSource(remote.URL, remote.Client())
			if body := decodeMap(t, f.request(http.MethodGet, "/api/update", "")); body["state"] != "unable" {
				t.Fatalf("unexpected status: %#v", body)
			}
		})
	}
}

func TestUpdateStatusKeepsLastSuccessfulResultOnTemporaryFailure(t *testing.T) {
	failing := false
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if failing {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`[{"tag_name":"v1.1.0","html_url":"https://example/release","prerelease":false}]`))
	}))
	defer remote.Close()
	f := newAPIFixture(t, nil)
	f.api.SetBuildInfo("1.0.0", "")
	f.api.SetUpdateSource(remote.URL, remote.Client())
	first := decodeMap(t, f.request(http.MethodGet, "/api/update", ""))
	failing = true
	second := decodeMap(t, f.request(http.MethodGet, "/api/update?force=1", ""))
	if second["state"] != first["state"] || second["latest_version"] != "1.1.0" || second["cached"] != true {
		t.Fatalf("did not preserve cached status: %#v", second)
	}
}

func TestUpdateStatusFallsBackToExistingGitTags(t *testing.T) {
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) }))
	defer releases.Close()
	tags := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"v0.1.0-beta.7"},{"name":"v0.1.0-beta.6"}]`))
	}))
	defer tags.Close()
	f := newAPIFixture(t, nil)
	f.api.SetBuildInfo("0.1.0-beta.6", "")
	f.api.updateURL = releases.URL
	f.api.updateTagsURL = tags.URL
	f.api.compareBaseURL = "https://example/compare/"
	f.api.updateClient = releases.Client()
	body := decodeMap(t, f.request(http.MethodGet, "/api/update", ""))
	if body["state"] != "beta_update_available" || body["latest_version"] != "0.1.0-beta.7" || body["release_url"] != "https://example/compare/v0.1.0-beta.6...v0.1.0-beta.7" {
		t.Fatalf("unexpected tag fallback: %#v", body)
	}
}

func TestDevelopmentBuildNeverClaimsOutdated(t *testing.T) {
	f := newAPIFixture(t, nil)
	body := decodeMap(t, f.request(http.MethodGet, "/api/update", ""))
	if body["state"] != "development" || body["running_version"] != "dev" {
		t.Fatalf("unexpected dev status: %#v", body)
	}
}
