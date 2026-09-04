package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/thef4tdaddy/watchweaver/internal/serializd"
	"github.com/thef4tdaddy/watchweaver/internal/trakt"
)

type operationalComponent struct {
	State  string `json:"state"`
	Label  string `json:"label"`
	Detail string `json:"detail"`
	Action string `json:"action,omitempty"`
}

type backupStatus struct {
	operationalComponent
	LastBackup *time.Time `json:"last_backup,omitempty"`
	SizeBytes  int64      `json:"size_bytes,omitempty"`
}

type operationalStatus struct {
	Overall    string                          `json:"overall"`
	CheckedAt  time.Time                       `json:"checked_at"`
	Components map[string]operationalComponent `json:"components"`
	Backup     backupStatus                    `json:"backup"`
}

func (a *API) operationalStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	status, err := a.buildOperationalStatus(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *API) diagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	status, err := a.buildOperationalStatus(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	counts := map[string]int{}
	for key, query := range map[string]string{
		"media_items": `SELECT COUNT(*) FROM media_items`, "watch_events": `SELECT COUNT(*) FROM watch_events`,
		"pending_tasks": `SELECT COUNT(*) FROM prompt_tasks WHERE state='pending'`, "pending_rating_sync": `SELECT COUNT(*) FROM rating_sync_state WHERE pending_rating IS NOT NULL OR pending_delete=1`,
	} {
		var count int
		if err := a.db.QueryRowContext(r.Context(), query).Scan(&count); err != nil {
			internalError(w)
			return
		}
		counts[key] = count
	}
	diagnostic := map[string]any{
		"generated_at": time.Now().UTC(), "go_version": runtime.Version(), "status": status,
		"counts": counts, "privacy": "Credential values, tokens, webhook URLs, media titles, external IDs, database paths, and detailed remote errors are excluded.",
	}
	// Detailed remote messages are useful in the UI but omitted from portable diagnostics.
	if traktComponent, ok := status.Components["trakt"]; ok && strings.Contains(traktComponent.Detail, "error") {
		traktComponent.Detail = "The latest Trakt synchronization has an error."
		status.Components["trakt"] = traktComponent
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="watchweaver-diagnostics.json"`)
	_ = json.NewEncoder(w).Encode(diagnostic)
}

func (a *API) buildOperationalStatus(ctx context.Context) (operationalStatus, error) {
	result := operationalStatus{Overall: "working", CheckedAt: time.Now().UTC(), Components: map[string]operationalComponent{}}
	settings, err := a.loadSettings(ctx)
	if err != nil {
		return result, err
	}
	auth := trakt.PublicStatus{Status: trakt.StatusNotConfigured}
	if a.trakt != nil {
		auth = a.trakt.Status(ctx)
	}
	traktState, traktDetail, traktAction := "working", "Connected and ready to synchronize.", "sync"
	if auth.Status != trakt.StatusConnected {
		traktState, traktDetail, traktAction = "needs_attention", "Trakt needs to be configured or reconnected.", "configure"
	}
	if a.traktSync != nil {
		syncStatus, err := a.traktSync.Status(ctx)
		if err != nil {
			return result, err
		}
		if syncStatus.Running {
			traktDetail = "Synchronization is running now."
		} else if syncStatus.LastError != "" && auth.Status == trakt.StatusConnected {
			traktState, traktDetail, traktAction = "needs_attention", "The latest Trakt synchronization has an error: "+syncStatus.LastError, "retry"
		}
	}
	result.Components["trakt"] = operationalComponent{State: traktState, Label: "Trakt", Detail: traktDetail, Action: traktAction}
	discordEnabled := a.discord != nil && a.discord.Configured()
	if discordEnabled {
		result.Components["discord"] = operationalComponent{State: "working", Label: "Discord", Detail: "Announcements are enabled and configured.", Action: "test"}
	} else {
		result.Components["discord"] = operationalComponent{State: "disabled", Label: "Discord", Detail: "Optional announcements are disabled.", Action: "configure"}
	}
	letterboxdStatus, err := a.letterboxd.Status(ctx, settings.Timezone)
	if err != nil {
		return result, err
	}
	if letterboxdStatus.PendingRows > 0 {
		result.Components["letterboxd"] = operationalComponent{State: "working", Label: "Letterboxd", Detail: "Movie activity is ready to export.", Action: "open"}
	} else {
		result.Components["letterboxd"] = operationalComponent{State: "working", Label: "Letterboxd", Detail: "No movie exports are waiting.", Action: "open"}
	}
	serialStatus, err := a.serializd.Status(ctx, serializdOptionsFromSettings(settings))
	if err != nil {
		return result, err
	}
	if !settings.SerializdEnabled {
		result.Components["serializd"] = operationalComponent{State: "disabled", Label: "Serializd", Detail: "Optional reminders are disabled.", Action: "configure"}
	} else if serialStatus.Due {
		result.Components["serializd"] = operationalComponent{State: "needs_attention", Label: "Serializd", Detail: "It is time to run the Serializd importer.", Action: "open"}
	} else {
		result.Components["serializd"] = operationalComponent{State: "working", Label: "Serializd", Detail: "Reminder thresholds have not been reached.", Action: "open"}
	}
	if err := a.db.PingContext(ctx); err != nil {
		result.Components["database"] = operationalComponent{State: "needs_attention", Label: "Database", Detail: "The database is unavailable."}
	} else {
		result.Components["database"] = operationalComponent{State: "working", Label: "Database", Detail: "Persistent storage is available.", Action: "diagnostics"}
	}
	result.Backup = a.latestBackup(ctx)
	for _, component := range result.Components {
		if component.State == "needs_attention" {
			result.Overall = "needs_attention"
		}
	}
	if result.Backup.State == "needs_attention" {
		result.Overall = "needs_attention"
	}
	return result, nil
}

func serializdOptionsFromSettings(settings settingsJSON) serializd.Options {
	return serializd.Options{Enabled: settings.SerializdEnabled, ReminderChanges: settings.SerializdReminderChanges, ReminderDays: settings.SerializdReminderDays}
}

func (a *API) latestBackup(ctx context.Context) backupStatus {
	status := backupStatus{operationalComponent: operationalComponent{State: "needs_attention", Label: "Backups", Detail: "No application backup was found.", Action: "instructions"}}
	var sequence int
	var name, dbPath string
	if err := a.db.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&sequence, &name, &dbPath); err != nil || dbPath == "" {
		return status
	}
	backupDir := filepath.Join(filepath.Dir(dbPath), "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			status.Detail = "The backup directory could not be inspected."
		}
		return status
	}
	latestName := ""
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".db") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		modified := info.ModTime().UTC()
		if status.LastBackup == nil || modified.After(*status.LastBackup) {
			status.LastBackup, status.SizeBytes = &modified, info.Size()
			latestName = entry.Name()
		}
	}
	if status.LastBackup != nil {
		status.State, status.Detail = "working", "A recent application backup is available."
		if _, err := os.Stat(filepath.Join(backupDir, latestName+".key")); err != nil {
			status.State, status.Detail = "needs_attention", "The newest database backup is missing its companion encryption key."
		} else if time.Since(*status.LastBackup) > 7*24*time.Hour {
			status.State, status.Detail = "needs_attention", "The newest application backup is more than seven days old."
		}
	}
	return status
}
