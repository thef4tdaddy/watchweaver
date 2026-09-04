package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type releaseInfo struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Prerelease bool   `json:"prerelease"`
}
type tagInfo struct {
	Name string `json:"name"`
}

type updateStatus struct {
	State      string     `json:"state"`
	Running    string     `json:"running_version"`
	Revision   string     `json:"revision,omitempty"`
	Latest     string     `json:"latest_version,omitempty"`
	ReleaseURL string     `json:"release_url,omitempty"`
	Channel    string     `json:"channel"`
	CheckedAt  *time.Time `json:"checked_at,omitempty"`
	Enabled    bool       `json:"enabled"`
	Cached     bool       `json:"cached,omitempty"`
}

type updateCache struct {
	mu      sync.Mutex
	status  updateStatus
	expires time.Time
}

func (a *API) SetBuildInfo(version, revision string) {
	a.version, a.revision = cleanVersion(version), revision
}

func (a *API) SetUpdateSource(url string, client *http.Client) {
	a.updateURL = url
	a.updateTagsURL = ""
	if client != nil {
		a.updateClient = client
	}
}

func cleanVersion(version string) string {
	version = strings.TrimSpace(strings.TrimPrefix(version, "v"))
	if version == "" {
		return "dev"
	}
	return version
}

func (a *API) update(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	settings, err := a.loadSettings(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	base := updateStatus{Running: a.version, Revision: a.revision, Enabled: settings.UpdateChecksEnabled, Channel: releaseChannel(a.version)}
	if !settings.UpdateChecksEnabled {
		base.State = "disabled"
		writeJSON(w, http.StatusOK, base)
		return
	}
	if base.Channel == "development" {
		base.State = "development"
		writeJSON(w, http.StatusOK, base)
		return
	}

	a.updateCache.mu.Lock()
	defer a.updateCache.mu.Unlock()
	force := r.URL.Query().Get("force") == "1"
	if !force && time.Now().Before(a.updateCache.expires) && a.updateCache.status.Running == a.version {
		cached := a.updateCache.status
		cached.Cached = true
		writeJSON(w, http.StatusOK, cached)
		return
	}
	status, err := a.fetchUpdate(r, base)
	if err != nil {
		if a.updateCache.status.CheckedAt != nil {
			cached := a.updateCache.status
			cached.Cached = true
			writeJSON(w, http.StatusOK, cached)
			return
		}
		base.State = "unable"
		writeJSON(w, http.StatusOK, base)
		return
	}
	a.updateCache.status, a.updateCache.expires = status, time.Now().Add(6*time.Hour)
	writeJSON(w, http.StatusOK, status)
}

func (a *API) fetchUpdate(r *http.Request, status updateStatus) (updateStatus, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, a.updateURL, nil)
	if err != nil {
		return status, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "WatchWeaver/"+a.version)
	resp, err := a.updateClient.Do(req)
	if err != nil {
		return status, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return status, errors.New("release service unavailable")
	}
	var releases []releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return status, err
	}
	compatible := make([]releaseInfo, 0, len(releases))
	for _, release := range releases {
		_, valid := semverParts(release.TagName)
		if valid && ((status.Channel == "beta" && release.Prerelease) || (status.Channel == "stable" && !release.Prerelease)) {
			compatible = append(compatible, release)
		}
	}
	if len(compatible) == 0 {
		return a.fetchTagUpdate(r, status)
	}
	sort.Slice(compatible, func(i, j int) bool { return compareSemver(compatible[i].TagName, compatible[j].TagName) > 0 })
	now := time.Now().UTC()
	status.CheckedAt = &now
	status.Latest = cleanVersion(compatible[0].TagName)
	status.ReleaseURL = compatible[0].HTMLURL
	if compareSemver(status.Latest, status.Running) > 0 {
		status.State = status.Channel + "_update_available"
	} else {
		status.State = "up_to_date"
	}
	return status, nil
}

func (a *API) fetchTagUpdate(r *http.Request, status updateStatus) (updateStatus, error) {
	if a.updateTagsURL == "" {
		return status, errors.New("no compatible release")
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, a.updateTagsURL, nil)
	if err != nil {
		return status, err
	}
	req.Header.Set("User-Agent", "WatchWeaver/"+a.version)
	resp, err := a.updateClient.Do(req)
	if err != nil {
		return status, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return status, errors.New("tag service unavailable")
	}
	var tags []tagInfo
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return status, err
	}
	compatible := make([]releaseInfo, 0, len(tags))
	for _, tag := range tags {
		_, valid := semverParts(tag.Name)
		prerelease := strings.Contains(cleanVersion(tag.Name), "-")
		if valid && ((status.Channel == "beta" && prerelease) || (status.Channel == "stable" && !prerelease)) {
			compatible = append(compatible, releaseInfo{TagName: tag.Name})
		}
	}
	if len(compatible) == 0 {
		return status, errors.New("no compatible tag")
	}
	sort.Slice(compatible, func(i, j int) bool { return compareSemver(compatible[i].TagName, compatible[j].TagName) > 0 })
	now := time.Now().UTC()
	status.CheckedAt = &now
	status.Latest = cleanVersion(compatible[0].TagName)
	status.ReleaseURL = a.compareBaseURL + "v" + status.Running + "..." + compatible[0].TagName
	if compareSemver(status.Latest, status.Running) > 0 {
		status.State = status.Channel + "_update_available"
	} else {
		status.State = "up_to_date"
	}
	return status, nil
}

func releaseChannel(version string) string {
	if version == "dev" || version == "" {
		return "development"
	}
	if strings.Contains(version, "-") {
		return "beta"
	}
	return "stable"
}

type semver struct {
	major, minor, patch int
	pre                 string
}

func semverParts(raw string) (semver, bool) {
	raw = cleanVersion(raw)
	core := strings.SplitN(raw, "+", 2)[0]
	bits := strings.SplitN(core, "-", 2)
	nums := strings.Split(bits[0], ".")
	if len(nums) != 3 {
		return semver{}, false
	}
	v := semver{}
	var err error
	if v.major, err = strconv.Atoi(nums[0]); err != nil {
		return v, false
	}
	if v.minor, err = strconv.Atoi(nums[1]); err != nil {
		return v, false
	}
	if v.patch, err = strconv.Atoi(nums[2]); err != nil {
		return v, false
	}
	if len(bits) == 2 {
		v.pre = bits[1]
	}
	return v, true
}
func compareSemver(left, right string) int {
	a, aok := semverParts(left)
	b, bok := semverParts(right)
	if !aok || !bok {
		return 0
	}
	if a.major != b.major {
		return cmp(a.major, b.major)
	}
	if a.minor != b.minor {
		return cmp(a.minor, b.minor)
	}
	if a.patch != b.patch {
		return cmp(a.patch, b.patch)
	}
	if a.pre == b.pre {
		return 0
	}
	if a.pre == "" {
		return 1
	}
	if b.pre == "" {
		return -1
	}
	ap, bp := strings.Split(a.pre, "."), strings.Split(b.pre, ".")
	for i := 0; i < len(ap) && i < len(bp); i++ {
		if ap[i] == bp[i] {
			continue
		}
		ai, ae := strconv.Atoi(ap[i])
		bi, be := strconv.Atoi(bp[i])
		if ae == nil && be == nil {
			return cmp(ai, bi)
		}
		if ae == nil {
			return -1
		}
		if be == nil {
			return 1
		}
		if ap[i] < bp[i] {
			return -1
		}
		return 1
	}
	return cmp(len(ap), len(bp))
}
func cmp(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
