import { useCallback, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import "./App.css";
import TraktAccessNote from "./TraktAccessNote";
import NetworkBoundaryNote from "./NetworkBoundaryNote";
import {
  APIError,
  type Batch,
  type HistoryItem,
  type Integrations,
  type JellyfinRemote,
  type LetterboxdStatus,
  type OperationalStatus,
  type Page,
  type Rating,
  type Review,
  type SerializdStatus,
  type SerializdReview,
  type Settings,
  type SetupStatus,
  type Task,
  type UpdateStatus,
  request,
} from "./api";

type View = "inbox" | "history" | "movies" | "tv" | "status" | "settings";
const nav: [View, string, string][] = [
  ["inbox", "Inbox", "inbox"],
  ["history", "History", "history"],
  ["movies", "Movies", "movies"],
  ["tv", "Television", "television"],
  ["status", "Status", "status"],
  ["settings", "Settings", "settings"],
];
const buildVersion = import.meta.env.VITE_APP_VERSION || "dev";

function App() {
  const [view, setView] = useState<View>("inbox");
  const [inboxCount, setInboxCount] = useState(0);
  const [integrations, setIntegrations] = useState<Integrations>();
  const [error, setError] = useState("");
  const [updateStatus, setUpdateStatus] = useState<UpdateStatus>();
  const refreshIntegrations = useCallback(
    () =>
      request<Integrations>("/api/integrations")
        .then(setIntegrations)
        .catch((e) => setError(e.message)),
    [],
  );
  const refreshUpdate = useCallback(() => request<UpdateStatus>("/api/update").then(setUpdateStatus).catch(() => undefined), []);
  const refreshInboxCount = useCallback(
    () => request<Page<Task>>("/api/inbox").then((data) => setInboxCount(data.total)).catch(() => undefined),
    [],
  );
  useEffect(() => {
    void refreshIntegrations();
    void refreshUpdate();
    void refreshInboxCount();
  }, [refreshInboxCount, refreshIntegrations, refreshUpdate]);
  useEffect(() => {
    const timer = window.setInterval(() => {
      void refreshIntegrations();
      void refreshInboxCount();
    }, 3000);
    return () => window.clearInterval(timer);
  }, [refreshInboxCount, refreshIntegrations]);
  const traktConnected = integrations?.trakt.authorization.status === "connected";
  const jellyfinState = getJellyfinState(integrations?.jellyfin);
  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <img className="brand-mark" src="/brand/watchweaver-icon.png" alt="" />
          <div>
            <strong>WatchWeaver</strong>
            <small>Your watch life, in sync.</small>
          </div>
        </div>
        <nav aria-label="Main navigation">
          {nav.map(([id, label, icon]) => (
            <button
              key={id}
              className={view === id ? "active" : ""}
              onClick={() => setView(id)}
              aria-label={label}
              title={label}
            >
              <span className={`nav-icon ${icon}`} aria-hidden="true" />
              {label}
              {id === "inbox" && inboxCount > 0 && (
                <i aria-label={`${inboxCount} inbox item${inboxCount === 1 ? "" : "s"} waiting`}>•</i>
              )}
            </button>
          ))}
        </nav>
        <div className="sidebar-foot">
          <StatusDot ok={traktConnected} label={`Trakt · ${traktConnected ? "Connected" : "Not connected"}`} />
          <StatusDot ok={jellyfinState.ok} label={`Jellyfin · ${jellyfinState.label}`} />
          <small className="version-label">Version {updateStatus?.running_version || buildVersion}</small>
          <StatusDot
            ok={integrations?.discord.enabled === true}
            label={`Discord · ${integrations?.discord.enabled ? "On" : "Off"}`}
          />
        </div>
      </aside>
      <main className="content">
        <header className="topbar">
          <div>
            <p className="eyebrow">WATCHWEAVER / {view.toUpperCase()}</p>
            <h1>{nav.find((n) => n[0] === view)?.[1]}</h1>
          </div>
        </header>
        {error && (
          <div className="alert" role="alert">
            <span>{error}</span>
            <button onClick={() => setError("")}>×</button>
          </div>
        )}
        {view === "inbox" && (
          <Inbox
            onError={setError}
            syncRunning={integrations?.trakt.sync.running === true}
            syncPhase={integrations?.trakt.poll.phase}
            syncError={integrations?.trakt.sync.last_error || integrations?.trakt.poll.last_error}
            integrationLoaded={integrations !== undefined}
            onCountChange={setInboxCount}
          />
        )}{" "}
        {view === "history" && <History onError={setError} />}{" "}
        {view === "movies" && <Movies onError={setError} />}{" "}
        {view === "tv" && <Television onError={setError} />}{" "}
        {view === "status" && (
          <StatusView
            updateStatus={updateStatus}
            setUpdateStatus={setUpdateStatus}
            onNavigate={setView}
            onError={setError}
            refreshIntegrations={refreshIntegrations}
          />
        )}{" "}
        {view === "settings" && (
          <SettingsView
            updateStatus={updateStatus}
            refreshUpdate={refreshUpdate}
            integrations={integrations}
            refreshIntegrations={refreshIntegrations}
            onError={setError}
          />
        )}
      </main>
    </div>
  );
}

function StatusView({
  updateStatus,
  setUpdateStatus,
  onNavigate,
  onError,
  refreshIntegrations,
}: {
  updateStatus?: UpdateStatus;
  setUpdateStatus: (value: UpdateStatus) => void;
  onNavigate: (view: View) => void;
  onError: (value: string) => void;
  refreshIntegrations: () => void;
}) {
  const [status, setStatus] = useState<OperationalStatus>();
  const [busy, setBusy] = useState("");
  const [showBackupHelp, setShowBackupHelp] = useState(false);
  const load = useCallback(
    () =>
      request<OperationalStatus>("/api/status")
        .then(setStatus)
        .catch((e) => onError(e.message)),
    [onError],
  );
  useEffect(() => {
    void load();
  }, [load]);
  const act = async (name: string, action?: string) => {
    if (action === "configure") {
      onNavigate("settings");
      return;
    }
    if (action === "open") {
      onNavigate(name === "letterboxd" ? "movies" : "tv");
      return;
    }
    if (action === "instructions") {
      setShowBackupHelp(true);
      return;
    }
    setBusy(name);
    try {
      if (action === "sync" || action === "retry")
        await request("/api/integrations/trakt/sync", { method: "POST" });
      if (action === "test")
        await request("/api/integrations/discord/test", { method: "POST" });
      refreshIntegrations();
      await load();
    } catch (e) {
      onError((e as Error).message);
      await load();
    } finally {
      setBusy("");
    }
  };
  if (!status) return <Loading />;
  const groups: [string, string, string[]][] = [
    ["Sources", "Automated watch-history connections", ["trakt", "jellyfin"]],
    ["Manual transfers", "Workflows that need a step from you", ["letterboxd", "serializd"]],
    ["Notifications", "Optional outbound alerts", ["discord"]],
    ["System", "Storage, recovery, diagnostics, and updates", ["database", "backup"]],
  ];
  return (
    <section>
      <div className="section-heading">
        <div>
          <p className="eyebrow">OPERATIONS</p>
          <h2>
            {status.overall === "working"
              ? "Everything is working"
              : "Some items need attention"}
          </h2>
          <p>Checked {formatDate(status.checked_at)}</p>
        </div>
        <button className="secondary" onClick={() => void load()}>
          Refresh status
        </button>
      </div>
      <NetworkBoundaryNote />
      {groups.map(([heading, description, names]) => <div className="status-group" key={heading}>
        <div className="status-group-heading"><div><p className="eyebrow">{heading}</p><h2>{description}</h2></div></div>
        <div className="settings-grid">
        {names.map((name) => {
          const component = name === "backup" ? status.backup : status.components[name];
          if (!component) return null;
          return (
          <article className="settings-card" key={name}>
            <div className="card-heading">
              <h2>{component.label}</h2>
              <span
                className={`state ${component.state === "working" ? "confirmed" : "pending"}`}
              >
                {humanStatus(component.state)}
              </span>
            </div>
            <p>{component.detail}</p>
            {name === "trakt" && component.state !== "working" && (
              <TraktAccessNote compact />
            )}
            {name === "backup" && status.backup.last_backup && (
              <p>
                Latest backup: {formatDate(status.backup.last_backup)} ·{" "}
                {Math.ceil((status.backup.size_bytes || 0) / 1024)} KB
              </p>
            )}
            <div className="setup-actions">
              {component.action && component.action !== "diagnostics" && (
                <button
                  className="primary"
                  disabled={busy === name}
                  onClick={() => void act(name, component.action)}
                >
                  {busy === name ? "Working…" : actionLabel(component.action)}
                </button>
              )}
              {name === "backup" && showBackupHelp && (
                <div className="override-note">
                  Run{" "}
                  <code>
                    docker compose exec watchweaver watchweaver backup
                  </code>
                  . Keep the generated <code>.db</code> and companion{" "}
                  <code>.key</code> files together.
                </div>
              )}
            </div>
          </article>
        )})}
        {heading === "System" && <article className="settings-card"><div className="card-heading"><h2>Diagnostics</h2><span className="state confirmed">Available</span></div><p>Download safe troubleshooting metadata. It excludes credentials and private media details.</p><a className="primary" href="/api/diagnostics" download>Download diagnostics</a></article>}
        {heading === "System" && <UpdateCard status={updateStatus} onUpdate={setUpdateStatus} onError={onError} />}
        </div>
      </div>)}
    </section>
  );
}

function actionLabel(action?: string) {
  return (
    (
      {
        sync: "Sync now",
        retry: "Retry sync",
        configure: "Open settings",
        test: "Send test",
        open: "Open workflow",
        instructions: "Show backup command",
      } as Record<string, string>
    )[action || ""] || "Open"
  );
}

function StatusDot({ ok, label }: { ok: boolean; label: string }) {
  return (
    <div className="status-dot">
      <span className={ok ? "ok" : ""} />
      {label}
    </div>
  );
}
function getJellyfinState(value?: Integrations["jellyfin"]) {
  if (!value?.configured) return { ok: false, label: "Not configured" };
  const rejected = value.last_rejection_at ? Date.parse(value.last_rejection_at) : 0;
  const accepted = value.last_accepted_at ? Date.parse(value.last_accepted_at) : 0;
  const probed = value.last_probe_at ? Date.parse(value.last_probe_at) : 0;
  const authFailure = value.last_auth_failure_at ? Date.parse(value.last_auth_failure_at) : 0;
  if (Math.max(rejected, authFailure) > Math.max(accepted, probed)) return { ok: false, label: "Needs attention" };
  if (accepted) return { ok: true, label: "Receiving" };
  if (probed) return { ok: true, label: "Connected · waiting" };
  return { ok: true, label: "Ready" };
}
function humanStatus(value?: string) {
  if (value === "needs_attention") return "Action needed";
  return (value || "not configured").replaceAll("_", " ");
}
function formatDate(value?: string) {
  if (!value) return "Never";
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(value));
}

const fallbackTimezones = ["UTC", "America/New_York", "America/Chicago", "America/Denver", "America/Phoenix", "America/Los_Angeles", "America/Anchorage", "Pacific/Honolulu", "Europe/London", "Europe/Paris", "Asia/Tokyo", "Australia/Sydney"];
function supportedTimezones(current: string) {
  const intl = Intl as typeof Intl & { supportedValuesOf?: (key: string) => string[] };
  const values = intl.supportedValuesOf?.("timeZone") || fallbackTimezones;
  return Array.from(new Set([current, ...values].filter(Boolean))).sort();
}
function timezoneLabel(zone: string) {
  try {
    const name = new Intl.DateTimeFormat(undefined, { timeZone: zone, timeZoneName: "long" }).formatToParts(new Date()).find((part) => part.type === "timeZoneName")?.value;
    return name && name !== zone ? `${zone} — ${name}` : zone;
  } catch { return `${zone} — saved legacy value`; }
}
function isValidTimezone(zone: string) {
  try { new Intl.DateTimeFormat(undefined, { timeZone: zone }).format(); return true; }
  catch { return false; }
}
function TimezoneSelector({value,onChange}:{value:string;onChange:(value:string)=>void}) {
  const device = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  const zones = supportedTimezones(value);
  return <><input aria-label="Timezone" list="watchweaver-timezones" value={value} onChange={(event)=>onChange(event.target.value)} aria-describedby="timezone-help"/><datalist id="watchweaver-timezones">{zones.map((zone)=><option key={zone} value={zone}>{timezoneLabel(zone)}</option>)}</datalist><small id="timezone-help">Search supported timezones. This device suggests {timezoneLabel(device)}.</small></>;
}
function mediaLabel(media: Task["media"]) {
  if (media.type === "episode")
    return `${media.show_title} · S${media.season_number} E${media.episode_number}`;
  return [media.title, media.year].filter(Boolean).join(" · ");
}

async function copyToClipboard(value: string) {
	if (navigator.clipboard?.writeText) return navigator.clipboard.writeText(value);
	const field = document.createElement("textarea");
	field.value = value;
	field.style.position = "fixed";
	field.style.opacity = "0";
	document.body.appendChild(field);
	field.select();
	const copied = document.execCommand("copy");
	field.remove();
	if (!copied) throw new Error("Copy is unavailable. Select and copy the token manually.");
}

function StarRating({ value, onChange, label, emptyLabel = "Choose rating" }: { value?: number; onChange: (value: number) => void; label: string; emptyLabel?: string }) {
  const [preview, setPreview] = useState<number>();
  const [dragging, setDragging] = useState(false);
  const shown = preview ?? value ?? 0;
  const ratingAtPointer = (event: ReactPointerEvent<HTMLDivElement>) => {
    const bounds = event.currentTarget.getBoundingClientRect();
    return Math.max(1, Math.min(10, Math.ceil(((event.clientX - bounds.left) / bounds.width) * 10)));
  };
  return <div className="rating-row" aria-label={label} onMouseLeave={() => setPreview(undefined)}>
    <div
      className="star-picker"
      onPointerDown={(event) => {
        event.currentTarget.setPointerCapture(event.pointerId);
        setDragging(true);
        const rating = ratingAtPointer(event);
        setPreview(rating);
        onChange(rating);
      }}
      onPointerMove={(event) => {
        if (!dragging) return;
        const rating = ratingAtPointer(event);
        setPreview(rating);
        onChange(rating);
      }}
      onPointerUp={(event) => {
        setDragging(false);
        event.currentTarget.releasePointerCapture(event.pointerId);
      }}
      onPointerCancel={() => setDragging(false)}
    >
      {[1, 2, 3, 4, 5].map((star) => {
        const half = star * 2 - 1;
        const full = star * 2;
        const fill = shown >= full ? "full" : shown >= half ? "half" : "empty";
        return <span className={`rating-star ${fill}`} key={star}>
          <span className="star-base" aria-hidden="true">★</span>
          <span className="star-fill" aria-hidden="true">★</span>
          {[half, full].map((rating) => <button
            type="button"
            className={`star-hit ${rating === half ? "left" : "right"}`}
            key={rating}
            title={`${rating / 2} stars`}
            aria-label={`${rating / 2} stars`}
            onMouseEnter={() => setPreview(rating)}
            onFocus={() => setPreview(rating)}
            onBlur={() => setPreview(undefined)}
            onClick={() => onChange(rating)}
          />)}
        </span>;
      })}
    </div>
    <b>{shown ? `${shown / 2} / 5` : emptyLabel}</b>
  </div>;
}

function Inbox({ onError, syncRunning, syncPhase, syncError, integrationLoaded, onCountChange }: { onError: (value: string) => void; syncRunning: boolean; syncPhase?: string; syncError?: string; integrationLoaded: boolean; onCountChange: (count: number) => void }) {
  const [page, setPage] = useState<Page<Task>>();
  const [ratings, setRatings] = useState<Record<number, number>>({});
  const [drafts, setDrafts] = useState<
    Record<number, { rating?: number; review: string }>
  >({});
  const [busy, setBusy] = useState<number>();
  const syncActive = syncRunning;
  const previousSyncActive = useRef(syncActive);
  const load = useCallback(async () => {
    try {
      const data = await request<Page<Task>>("/api/inbox");
      setPage(data);
      onCountChange(data.total);
      const pairs = await Promise.all(
        data.items.map(async (task) => {
          try {
            return [
              task.media.id,
              (await request<Rating>(`/api/media/${task.media.id}/rating`))
                .rating,
            ] as const;
          } catch (e) {
            if (e instanceof APIError && e.status === 404)
              return [task.media.id, undefined] as const;
            throw e;
          }
        }),
      );
      const current: Record<number, number> = {};
      pairs.forEach(([id, value]) => {
        if (value !== undefined) current[id] = value;
      });
      setRatings(current);
      setDrafts(
        Object.fromEntries(
          data.items.map((t) => [
            t.id,
            { rating: current[t.media.id], review: "" },
          ]),
        ),
      );
    } catch (e) {
      onError((e as Error).message);
    }
  }, [onCountChange, onError]);
  // oxlint-disable react/set-state-in-effect -- the effect starts an external API synchronization.
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    if (previousSyncActive.current && !syncActive) void load();
    previousSyncActive.current = syncActive;
  }, [load, syncActive]);
  // oxlint-enable react/set-state-in-effect
  const act = async (id: number, action: string, body?: unknown) => {
    setBusy(id);
    try {
      await request(`/api/tasks/${id}/${action}`, {
        method: "POST",
        body: body ? JSON.stringify(body) : undefined,
      });
      await load();
    } catch (e) {
      onError((e as Error).message);
    } finally {
      setBusy(undefined);
    }
  };
  if (!page) return <Loading />;
  return (
    <section>
      <div className="section-heading">
        <div>
          <p className="eyebrow">ACTION QUEUE</p>
          <h2>
            {page.total} item{page.total === 1 ? "" : "s"} waiting
          </h2>
        </div>
      </div>
      {syncActive && <SyncProgress phase={syncPhase || "synchronizing"} />}
      {!syncActive && syncError && <div className="alert" role="status">Latest Trakt check failed: {syncError}. WatchWeaver will retry automatically.</div>}
      {page.items.length === 0 && !integrationLoaded ? (
        <Loading />
      ) : page.items.length === 0 && !syncActive ? (
        <Empty title="No prompts waiting" body="Eligible rating and review prompts will appear here after watch activity is processed. Episode reviews can always be added from History." />
      ) : (
        <div className="task-list">
          {page.items.map((task) => {
            const draft = drafts[task.id] || { review: "" };
            const existing = ratings[task.media.id];
            return (
              <article className="task-card" key={task.id}>
                <div className={`poster ${task.media.type}`}>
                  {task.media.type === "movie" ? "FILM" : "TV"}
                </div>
                <div className="task-main">
                  <div className="task-title">
                    <div>
                      <span className="tag">{task.media.type}</span>
                      <h3>{task.media.title}</h3>
                      <p>{mediaLabel(task.media)}</p>
                    </div>
                    <span className={`state ${task.state}`}>{task.state}</span>
                  </div>
                  {task.type !== "review" && existing && (
                    <div className="current-note">
                      Current rating: <strong>{existing / 2} ★</strong> ·
                      Submitting changes the current rating; it does not create
                      a historical snapshot.
                    </div>
                  )}
                  {task.type !== "review" && <StarRating
                    label={`Rating for ${task.media.title}`}
                    value={draft.rating}
                    onChange={(rating) => setDrafts((d) => ({
                      ...d,
                      [task.id]: { ...draft, rating },
                    }))}
                  />}
                  {task.media.type !== "episode" && task.type !== "rating" && (
                    <textarea
                      value={draft.review}
                      onChange={(e) =>
                        setDrafts((d) => ({
                          ...d,
                          [task.id]: { ...draft, review: e.target.value },
                        }))
                      }
                      placeholder="Add an optional review…"
                      aria-label={`Review for ${task.media.title}`}
                    />
                  )}
                  {task.media.type === "episode" && (
                    <p className="destination-note">
                      This prompt is for the episode rating only. Add an
                      optional episode review from History whenever an episode
                      stands out.
                    </p>
                  )}
                  <div className="actions">
                    <button
                      className="primary"
                      disabled={
                        busy === task.id ||
                        (task.type !== "review" && !draft.rating && (task.type === "rating" || !draft.review.trim())) ||
                        (task.type === "review" && !draft.review.trim())
                      }
                      onClick={() =>
                        void act(task.id, "complete", {
                          ...(task.type !== "review" && draft.rating ? { rating: draft.rating } : {}),
                          ...(task.media.type !== "episode" && task.type !== "rating" && draft.review.trim()
                            ? { review: draft.review }
                            : {}),
                        })
                      }
                    >
                      Save & complete
                    </button>
                    <button
                      className="secondary"
                      disabled={busy === task.id}
                      onClick={() =>
                        void act(task.id, "snooze", {
                          until: new Date(Date.now() + 86400000).toISOString(),
                        })
                      }
                    >
                      Tomorrow
                    </button>
                    <button
                      className="text-button"
                      disabled={busy === task.id}
                      onClick={() => void act(task.id, "skip")}
                    >
                      Skip
                    </button>
                  </div>
                </div>
              </article>
            );
          })}
        </div>
      )}
    </section>
  );
}

function History({ onError }: { onError: (v: string) => void }) {
  const [pageNo, setPageNo] = useState(1);
  const [data, setData] = useState<Page<HistoryItem>>();
  const [editing, setEditing] = useState<number>();
  useEffect(() => {
    request<Page<HistoryItem>>(`/api/history?page=${pageNo}&per_page=20`)
      .then(setData)
      .catch((e) => onError(e.message));
  }, [pageNo, onError]);
  if (!data) return <Loading />;
  return (
    <section>
      <div className="section-heading">
        <div>
          <p className="eyebrow">LOCAL ARCHIVE</p>
          <h2>{data.total} distinct watches</h2>
        </div>
      </div>
      {data.items.length === 0 ? (
        <Empty
          title="No history yet"
          body="Connect Trakt to import your movie and episode history."
        />
      ) : (
        <div className="timeline">
          {data.items.map((item) => (
            <article key={item.id}>
              <div className="timeline-date">
                <strong>
                  {new Date(item.watched_at).toLocaleDateString(undefined, {
                    month: "short",
                    day: "numeric",
                  })}
                </strong>
                <small>{new Date(item.watched_at).getFullYear()}</small>
              </div>
              <span className="timeline-line" />
              <div className="history-card">
                <span className="tag">{item.media.type}</span>
                <h3>{item.media.title}</h3>
                <p>
                  {item.media.show_title
                    ? `${item.media.show_title} · S${item.media.season_number} E${item.media.episode_number}`
                    : item.media.year}
                </p>
                <small>{formatDate(item.watched_at)}</small>
                <button className="secondary history-edit-button" onClick={() => setEditing(editing === item.id ? undefined : item.id)}>
                  {editing === item.id ? "Close editor" : "Rate or review"}
                </button>
                {editing === item.id && <HistoryEditor item={item} onError={onError} />}
              </div>
            </article>
          ))}
        </div>
      )}
      <Pager page={data.page} pages={data.total_pages} setPage={setPageNo} />
    </section>
  );
}

function HistoryEditor({ item, onError }: { item: HistoryItem; onError: (v: string) => void }) {
  const targets = item.media.type === "episode" && item.media.season_id
    ? [{ id: item.media.id, label: "This episode", type: "episode" }, { id: item.media.season_id, label: `Season ${item.media.season_number}`, type: "season" }]
    : [{ id: item.media.id, label: "This movie", type: "movie" }];
  const [target, setTarget] = useState(targets[0]);
  const [rating, setRating] = useState<number>();
  const [review, setReview] = useState("");
  const [original, setOriginal] = useState<{ rating?: number; review: string }>({ review: "" });
  const [busy, setBusy] = useState(true);
  const [saved, setSaved] = useState("");
  // oxlint-disable react/set-state-in-effect -- changing targets starts an external API synchronization.
  useEffect(() => {
    setBusy(true);
    setSaved("");
    Promise.all([
      request<Rating>(`/api/media/${target.id}/rating`).catch((e) => e instanceof APIError && e.status === 404 ? undefined : Promise.reject(e)),
      request<Review>(`/api/media/${target.id}/review`).catch((e) => e instanceof APIError && e.status === 404 ? undefined : Promise.reject(e)),
    ]).then(([currentRating, currentReview]) => {
      const value = { rating: currentRating?.rating, review: currentReview?.body || "" };
      setRating(value.rating);
      setReview(value.review);
      setOriginal(value);
    }).catch((e) => onError(e.message)).finally(() => setBusy(false));
  }, [target.id, onError]);
  // oxlint-enable react/set-state-in-effect
  const save = async () => {
    setBusy(true);
    setSaved("");
    try {
      if (rating !== original.rating) await request(`/api/media/${target.id}/rating`, rating === undefined ? { method: "DELETE" } : { method: "PUT", body: JSON.stringify({ rating }) });
      const trimmed = review.trim();
      if (trimmed !== original.review) await request(`/api/media/${target.id}/review`, trimmed ? { method: "PUT", body: JSON.stringify({ body: trimmed }) } : { method: "DELETE" });
      const value = { rating, review: trimmed };
      setReview(trimmed);
      setOriginal(value);
      setSaved("Saved");
    } catch (e) {
      onError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const destination = target.type === "movie"
    ? "The rating syncs to Trakt. The review stays local and is included in your next Letterboxd CSV."
    : target.type === "episode"
      ? "The rating syncs to Trakt. The review stays local because Serializd cannot import it from Trakt."
      : "The season rating syncs to Trakt. The season review stays local and must be entered manually elsewhere.";
  return <div className="history-editor">
    {targets.length > 1 && <div className="target-tabs">{targets.map((value) => <button key={value.id} className={target.id === value.id ? "active" : ""} onClick={() => setTarget(value)}>{value.label}</button>)}</div>}
    {busy ? <Loading /> : <>
      <StarRating label={`Rating for ${target.label}`} value={rating} onChange={setRating} emptyLabel="Not rated" />
      {rating !== undefined && <button className="text-button" onClick={() => setRating(undefined)}>Clear rating</button>}
      <textarea value={review} onChange={(e) => setReview(e.target.value)} placeholder="Add an optional review…" aria-label={`Review for ${target.label}`} />
      <p className="destination-note">{destination}</p>
      <div className="actions"><button className="primary" disabled={rating === original.rating && review.trim() === original.review} onClick={() => void save()}>Save changes</button>{saved && <span className="saved-note">✓ {saved}</span>}</div>
    </>}
  </div>;
}

function Movies({ onError }: { onError: (v: string) => void }) {
  const [status, setStatus] = useState<LetterboxdStatus>();
  const [batches, setBatches] = useState<Batch[]>([]);
  const [busy, setBusy] = useState(false);
  const load = useCallback(async () => {
    try {
      const [s, b] = await Promise.all([
        request<LetterboxdStatus>("/api/letterboxd"),
        request<{ items: Batch[] }>("/api/letterboxd/batches"),
      ]);
      setStatus(s);
      setBatches(b.items);
    } catch (e) {
      onError((e as Error).message);
    }
  }, [onError]);
  // oxlint-disable react/set-state-in-effect -- the effect starts an external API synchronization.
  useEffect(() => {
    void load();
  }, [load]);
  // oxlint-enable react/set-state-in-effect
  const generate = async () => {
    setBusy(true);
    try {
      await request("/api/letterboxd/batches", { method: "POST" });
      await load();
    } catch (e) {
      onError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const confirm = async (id: number) => {
    try {
      await request(`/api/letterboxd/batches/${id}/confirm`, {
        method: "POST",
      });
      await load();
    } catch (e) {
      onError((e as Error).message);
    }
  };
  if (!status) return <Loading />;
  return (
    <section>
      <div className="hero-panel">
        <div>
          <p className="eyebrow">LETTERBOXD EXPORT</p>
          <h2>
            {status.pending_events} movie event
            {status.pending_events === 1 ? "" : "s"} ready
          </h2>
          <p>
            Generate official-import-compatible CSV files. Nothing is marked
            imported until you confirm it.
          </p>
        </div>
        <button
          className="primary large"
          disabled={busy || status.pending_rows === 0}
          onClick={() => void generate()}
        >
          Generate export
        </button>
      </div>
      <div className="metric-grid">
        <Metric value={status.pending_rows} label="Diary rows" />
        <Metric
          value={status.duplicate_warnings}
          label="Same-day limitations"
          tone={status.duplicate_warnings ? "warn" : undefined}
        />
        <Metric
          value={status.generated_batches}
          label="Awaiting confirmation"
        />
      </div>
      <h3 className="subheading">Export batches</h3>
      {batches.length === 0 ? (
        <Empty
          title="No exports yet"
          body="Your first batch can include all representable movie history."
        />
      ) : (
        <div className="batch-list">
          {batches.map((batch) => (
            <article key={batch.id}>
              <div>
                <span className={`state ${batch.state}`}>{batch.state}</span>
                <h3>Batch #{batch.id}</h3>
                <p>
                  {batch.row_count} rows · {batch.event_count} local events ·{" "}
                  {batch.timezone}
                </p>
                {batch.duplicate_warnings > 0 && (
                  <small className="warning">
                    ⚠ {batch.duplicate_warnings} same-day duplicate
                    limitation(s)
                  </small>
                )}
              </div>
              <div className="batch-actions">
                {batch.files.map((file) => (
                  <a
                    className="secondary"
                    key={file.part_number}
                    href={`/api/letterboxd/batches/${batch.id}/files/${file.part_number}`}
                  >
                    Download part {file.part_number}
                  </a>
                ))}
                {batch.state === "generated" && (
                  <button
                    className="primary"
                    onClick={() => void confirm(batch.id)}
                  >
                    Mark imported
                  </button>
                )}
              </div>
            </article>
          ))}
        </div>
      )}
    </section>
  );
}

function Television({ onError }: { onError: (v: string) => void }) {
  const [status, setStatus] = useState<SerializdStatus>();
  const [marking, setMarking] = useState(false);
  const [reviews, setReviews] = useState<SerializdReview[]>([]);
  const [showTransferred, setShowTransferred] = useState(false);
  const [reviewBusy, setReviewBusy] = useState<number>();
  const load = useCallback(
    () =>
      request<SerializdStatus>("/api/serializd")
        .then(setStatus)
        .catch((e) => onError(e.message)),
    [onError],
  );
  useEffect(() => {
    void load();
  }, [load]);
  const loadReviews = useCallback(() => request<{items: SerializdReview[]}>(`/api/serializd/reviews?include_transferred=${showTransferred}`).then((value) => setReviews(value.items)).catch((e) => onError(e.message)), [onError, showTransferred]);
  useEffect(() => { void loadReviews(); }, [loadReviews]);
  const transfer = async (review: SerializdReview, transferred: boolean) => {
    setReviewBusy(review.review_id);
    try { await request(`/api/serializd/reviews/${review.review_id}/transferred`, {method:"PUT", body:JSON.stringify({transferred})}); await loadReviews(); }
    catch (e) { onError((e as Error).message); } finally { setReviewBusy(undefined); }
  };
  const mark = async () => {
    setMarking(true);
    try {
      const updated = await request<SerializdStatus>(
        "/api/serializd/mark-synced",
        { method: "POST" },
      );
      setStatus(updated);
    } catch (e) {
      onError((e as Error).message);
    } finally {
      setMarking(false);
    }
  };
  if (!status) return <Loading />;
  return (
    <section>
      <div className={`hero-panel tv ${status.due ? "due" : ""}`}>
        <div>
          <p className="eyebrow">SERIALIZD CHECKPOINT</p>
          <h2>
            {status.due
              ? "Your Trakt import is due"
              : "Television is in rhythm"}
          </h2>
          <p>
            {status.pending_changes
              ? `${status.pending_changes} transferable changes since your last confirmation.`
              : "No transferable TV activity is waiting."}
          </p>
        </div>
        <div className="hero-actions">
          <a
            className="primary large"
            href={status.import_url}
            target="_blank"
            rel="noreferrer"
          >
            Open importer ↗
          </a>
          <button
            className="secondary"
            disabled={marking}
            onClick={() => void mark()}
          >
            {marking ? "Saving…" : "I completed the Serializd import"}
          </button>
        </div>
      </div>
      <div className="metric-grid">
        <Metric
          value={status.tracked_episode_watches}
          label="Total episodes in history"
        />
        <Metric
          value={status.pending_episode_watches}
          label="New episode watches"
        />
        <Metric value={status.pending_rating_changes} label="Rating changes" />
        <Metric
          value={
            status.last_confirmed_at
              ? formatDate(status.last_confirmed_at)
              : "Never"
          }
          label="Last Serializd import confirmed"
        />
      </div>
      {!status.last_confirmed_at && status.tracked_episode_watches > 0 && (
        <div className="notice">
          <strong>Imported history is ready</strong>
          <p>
            Existing Trakt history is your starting baseline, so it is tracked
            without being counted as new. After running the Serializd importer,
            choose I completed the Serializd import to start counting changes
            from today.
          </p>
        </div>
      )}
      {(status.unsupported_season_ratings > 0 ||
        status.unsupported_tv_reviews > 0) && (
        <div className="notice">
          <strong>Manual data stays local</strong>
          <p>
            {status.unsupported_season_ratings} season rating(s) and{" "}
            {status.unsupported_tv_reviews} TV review(s) are not carried by
            Serializd’s Trakt importer.
          </p>
        </div>
      )}
      <div className="rule-card">
        <h3>Reminder rule</h3>
        <p>
          Due after <strong>{status.reminder_changes} changes</strong> or{" "}
          <strong>{status.reminder_days} days</strong> with pending activity.
        </p>
        <div>
          <StatusDot
            ok={status.count_threshold_reached}
            label="Count threshold"
          />
          <StatusDot
            ok={status.elapsed_threshold_reached}
            label="Elapsed threshold"
          />
        </div>
      </div>
      <div className="checkpoint-explanation">
        <strong>Why confirmation is manual</strong>
        <p>
          Serializd does not report a completed import back to WatchWeaver. The
          confirmation time changes only when you say the import is complete;
          it is not your latest Trakt check or episode watch.
        </p>
      </div>
      <div className="review-transfer-section">
        <div className="section-heading"><div><p className="eyebrow">MANUAL BRIDGE</p><h2>Reviews to copy</h2><p>Serializd cannot receive review text through its Trakt importer. Copy each review, paste it into Serializd, then mark it transferred.</p></div><label className="inline-check"><input type="checkbox" checked={showTransferred} onChange={(e)=>setShowTransferred(e.target.checked)}/> Show transferred</label></div>
        {reviews.length === 0 ? <Empty title={showTransferred ? "No television reviews" : "No reviews waiting"} body={showTransferred ? "Write a season or episode review from History to see it here." : "Transferred reviews stay safely stored and can be shown at any time."}/> : <div className="review-transfer-list">{reviews.map((review) => <article className="settings-card" key={review.review_id}><div className="card-heading"><div><span className="tag">{review.media_type}</span><h3>{review.show_title || review.title}{review.season_number !== undefined ? ` · S${review.season_number}` : ""}{review.episode_number !== undefined ? ` E${review.episode_number}` : ""}</h3></div><span className={`state ${review.transferred_at ? "confirmed" : "pending"}`}>{review.transferred_at ? "Transferred" : "Waiting"}</span></div>{review.rating && <p>{review.rating / 2} ★</p>}<blockquote>{review.body}</blockquote><small>Updated {formatDate(review.review_updated_at)}</small><div className="actions"><button className="primary" onClick={() => void copyToClipboard(review.body).catch((e)=>onError(e.message))}>Copy review</button><button className="secondary" disabled={reviewBusy===review.review_id} onClick={()=>void transfer(review,!review.transferred_at)}>{review.transferred_at ? "Mark not transferred" : "Mark transferred"}</button></div></article>)}</div>}
      </div>
    </section>
  );
}

function SettingsView({
  updateStatus,
  refreshUpdate,
  integrations,
  refreshIntegrations,
  onError,
}: {
  updateStatus?: UpdateStatus;
  refreshUpdate: () => void;
  integrations?: Integrations;
  refreshIntegrations: () => void;
  onError: (v: string) => void;
}) {
  const [settings, setSettings] = useState<Settings>();
  const [authBusy, setAuthBusy] = useState(false);
  const [syncBusy, setSyncBusy] = useState(false);
  const [setup, setSetup] = useState<SetupStatus>();
  const [clientID, setClientID] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [traktEditorOpen, setTraktEditorOpen] = useState(false);
  const [webhook, setWebhook] = useState("");
  const [discordEnabled, setDiscordEnabled] = useState(false);
  const [integrationBusy, setIntegrationBusy] = useState(false);
  const [integrationMessage, setIntegrationMessage] = useState("");
	const [jellyfinToken, setJellyfinToken] = useState("");
  const [jellyfinRemote, setJellyfinRemote] = useState<JellyfinRemote>();
  const [jellyfinURL, setJellyfinURL] = useState("");
  const [jellyfinAPIKey, setJellyfinAPIKey] = useState("");
  const [jellyfinUserID, setJellyfinUserID] = useState("");
  useEffect(() => {
    request<Settings>("/api/settings")
      .then(setSettings)
      .catch((e) => onError(e.message));
    request<SetupStatus>("/api/setup")
      .then((value) => {
        setSetup(value);
        setDiscordEnabled(value.discord.enabled);
      })
      .catch((e) => onError(e.message));
    request<JellyfinRemote>("/api/integrations/jellyfin/remote")
      .then((value) => { setJellyfinRemote(value); setJellyfinURL(value.url || ""); setJellyfinUserID(value.user_id || ""); })
      .catch((e) => onError(e.message));
  }, [onError]);
  const runIntegrationAction = async (action: () => Promise<string>) => {
    setIntegrationBusy(true);
    setIntegrationMessage("");
    try {
      setIntegrationMessage(await action());
      const value = await request<SetupStatus>("/api/setup");
      setSetup(value);
      setDiscordEnabled(value.discord.enabled);
      refreshIntegrations();
    } catch (e) {
      onError((e as Error).message);
    } finally {
      setIntegrationBusy(false);
    }
  };
  const saveTraktCredentials = () =>
    runIntegrationAction(async () => {
      await request("/api/integrations/trakt/config", {
        method: "PUT",
        body: JSON.stringify({
          client_id: clientID,
          client_secret: clientSecret,
        }),
      });
      setClientID("");
      setClientSecret("");
      setTraktEditorOpen(false);
      return "Trakt credentials saved.";
    });
  const saveDiscord = () =>
    runIntegrationAction(async () => {
      await request("/api/integrations/discord/config", {
        method: "PUT",
        body: JSON.stringify({ webhook_url: webhook, enabled: discordEnabled }),
      });
      setWebhook("");
      return "Discord settings saved.";
    });
  const testDiscord = () =>
    runIntegrationAction(async () => {
      await request("/api/integrations/discord/test", { method: "POST" });
      return "Test announcement sent.";
    });
	const generateJellyfinToken = () =>
		runIntegrationAction(async () => {
			const result = await request<{ token: string }>("/api/integrations/jellyfin", { method: "POST" });
			setJellyfinToken(result.token);
			return "Jellyfin token generated. Copy it now; it will not be shown again.";
		});
	const revokeJellyfinToken = () =>
		runIntegrationAction(async () => {
			await request("/api/integrations/jellyfin", { method: "DELETE" });
			setJellyfinToken("");
			return "Jellyfin connection token revoked.";
		});
  const saveRemoteJellyfin = () =>
    runIntegrationAction(async () => {
      const value = await request<JellyfinRemote>("/api/integrations/jellyfin/remote", { method: "PUT", body: JSON.stringify({ enabled: true, url: jellyfinURL, user_id: jellyfinUserID, api_key: jellyfinAPIKey }) });
      setJellyfinRemote(value); setJellyfinAPIKey("");
      return "Remote Jellyfin connection saved. WatchWeaver is connecting now.";
    });
  const testRemoteJellyfin = () =>
    runIntegrationAction(async () => {
      const value = await request<{server_version:string}>("/api/integrations/jellyfin/remote/test", { method: "POST" });
      return `Connected to Jellyfin ${value.server_version}.`;
    });
  const save = async () => {
    if (!settings) return;
    if (!isValidTimezone(settings.timezone)) {
      onError("Choose a supported timezone from the list.");
      return;
    }
    try {
      setSettings(
        await request<Settings>("/api/settings", {
          method: "PUT",
          body: JSON.stringify(settings),
        }),
      );
      refreshUpdate();
    } catch (e) {
      onError((e as Error).message);
    }
  };
  const authorize = async (action: "authorize" | "authorize/poll") => {
    setAuthBusy(true);
    try {
      await request(`/api/integrations/trakt/${action}`, { method: "POST" });
      refreshIntegrations();
    } catch (e) {
      onError((e as Error).message);
    } finally {
      setAuthBusy(false);
    }
  };
  useEffect(() => {
    if (integrations?.trakt.authorization.status !== "authorization_pending")
      return;
    const timer = window.setInterval(
      () => {
        void request("/api/integrations/trakt/authorize/poll", {
          method: "POST",
        })
          .then(refreshIntegrations)
          .catch((e) => onError(e.message));
      },
      Math.max(1, integrations.trakt.authorization.poll_after_seconds ?? 5) *
        1000,
    );
    return () => window.clearInterval(timer);
  }, [
    integrations?.trakt.authorization.status,
    integrations?.trakt.authorization.poll_after_seconds,
    onError,
    refreshIntegrations,
  ]);
  const syncNow = async () => {
    setSyncBusy(true);
    try {
      await request("/api/integrations/trakt/sync", { method: "POST" });
      refreshIntegrations();
    } catch (e) {
      onError((e as Error).message);
      refreshIntegrations();
    } finally {
      setSyncBusy(false);
    }
  };
  if (!settings || !integrations || !setup) return <Loading />;
  const trakt = integrations.trakt;
  const traktConnected = trakt.authorization.status === "connected";
  const showTraktEditor = !traktConnected || traktEditorOpen;
  return (
    <section className="settings-grid">
      {integrationMessage && (
        <p className="success-message settings-message" role="status">
          {integrationMessage}
        </p>
      )}
		<div className="settings-card">
			<div className="card-heading">
				<div><p className="eyebrow">AUTOMATED SOURCE</p><h2>Jellyfin</h2></div>
				<span className={`state ${setup.jellyfin?.configured ? "confirmed" : "pending"}`}>{setup.jellyfin?.configured ? "Ready" : "Not configured"}</span>
			</div>
			<p>Connect the WatchWeaver Jellyfin plugin with a dedicated, write-only token. Jellyfin can be your only history source or work alongside Trakt.</p>
			<div className="connection-info">
				{integrations.jellyfin?.last_accepted_at ? <>
					<StatusDot ok label="Events received" />
					<p>Last event: {formatDate(integrations.jellyfin.last_accepted_at)}</p>
					<p>Jellyfin {integrations.jellyfin.last_server_version || "unknown"} · plugin {integrations.jellyfin.last_plugin_version || "unknown"}</p>
				</> : <><StatusDot ok={Boolean(integrations.jellyfin?.last_probe_at)} label={integrations.jellyfin?.last_probe_at ? "Plugin connected · waiting for the first event" : setup.jellyfin?.configured ? "Waiting for the plugin heartbeat" : "Generate a token to connect"} />{integrations.jellyfin?.last_probe_at && <p>Last heartbeat: {formatDate(integrations.jellyfin.last_probe_at)} · Jellyfin {integrations.jellyfin.last_probe_server_version || "unknown"} · plugin {integrations.jellyfin.last_probe_plugin_version || "unknown"}</p>}</>}
				{integrations.jellyfin?.last_rejection_code && <small className="warning">Latest rejected delivery: {integrations.jellyfin.last_rejection_code}</small>}
			</div>
			<div className="settings-actions">
				<button className="primary" disabled={integrationBusy} onClick={() => void generateJellyfinToken()}>{setup.jellyfin?.configured ? "Rotate token" : "Generate token"}</button>
				{setup.jellyfin?.configured && <button className="secondary" disabled={integrationBusy} onClick={() => void revokeJellyfinToken()}>Revoke token</button>}
			</div>
			{jellyfinToken && <div className="auth-code jellyfin-token"><p>Paste this token into the Jellyfin plugin now:</p><strong>{jellyfinToken}</strong><button className="secondary" onClick={() => void copyToClipboard(jellyfinToken).then(() => setIntegrationMessage("Jellyfin token copied.")).catch((error) => onError(error.message))}>Copy token</button></div>}
			<div className="credential-fields">
				<p className="eyebrow">REMOTE JELLYFIN (RECOMMENDED FOR SEEDBOXES)</p>
				<p>WatchWeaver connects outward to Jellyfin, so your private WatchWeaver server does not need to be exposed.</p>
				<label>Jellyfin URL<input type="url" value={jellyfinURL} onChange={(event)=>setJellyfinURL(event.target.value)} placeholder="https://jellyfin.example.com" /></label>
				<label>Jellyfin API key<input type="password" value={jellyfinAPIKey} onChange={(event)=>setJellyfinAPIKey(event.target.value)} placeholder={jellyfinRemote?.configured ? "Leave blank to keep the saved key" : "API key"} autoComplete="new-password" /></label>
				<label>Jellyfin user ID (optional)<input value={jellyfinUserID} onChange={(event)=>setJellyfinUserID(event.target.value)} placeholder="Limit recovery checks to one user" /></label>
				<div className="connection-info"><StatusDot ok={jellyfinRemote?.connected === true} label={jellyfinRemote?.connected ? "Remote stream connected" : jellyfinRemote?.configured ? "Remote stream reconnecting" : "Remote stream not configured"} />{jellyfinRemote?.last_event_at && <p>Last event: {formatDate(jellyfinRemote.last_event_at)} · {jellyfinRemote.events_received} received</p>}{jellyfinRemote?.last_error && <small className="warning">{jellyfinRemote.last_error}</small>}</div>
				<div className="settings-actions"><button className="primary" disabled={integrationBusy || !jellyfinURL || (!jellyfinAPIKey && !jellyfinRemote?.configured)} onClick={()=>void saveRemoteJellyfin()}>Save and connect</button><button className="secondary" disabled={integrationBusy || !jellyfinRemote?.configured} onClick={()=>void testRemoteJellyfin()}>Test connection</button></div>
			</div>
			<div className="override-note">This receiver is intended for private LAN/VPN use. The token is encrypted at rest and never returned after this screen is dismissed.</div>
		</div>
      <div className="settings-card">
        <div className="card-heading">
          <div>
			<p className="eyebrow">OPTIONAL SOURCE</p>
            <h2>Trakt</h2>
          </div>
          <span
            className={`state ${trakt.authorization.status === "connected" ? "confirmed" : "pending"}`}
          >
            {humanStatus(trakt.authorization.status)}
          </span>
        </div>
        {trakt.authorization.status === "authorization_pending" ? (
          <div className="auth-code">
            <p>Open the verification page and enter:</p>
            <strong>{trakt.authorization.user_code}</strong>
            <a
              href={trakt.authorization.verification_url}
              target="_blank"
              rel="noreferrer"
            >
              Open Trakt ↗
            </a>
            <button
              className="primary"
              disabled={authBusy}
              onClick={() => void authorize("authorize/poll")}
            >
              I’ve authorized
            </button>
          </div>
        ) : trakt.authorization.status !== "connected" ? (
          <button
            className="primary"
            disabled={
              authBusy || trakt.authorization.status === "not_configured"
            }
            onClick={() => void authorize("authorize")}
          >
            Connect Trakt
          </button>
        ) : (
          <div className="connection-info">
            <StatusDot ok label="Account connected" />
            <p>
              History sync: <strong>{humanStatus(trakt.poll.phase)}</strong>
            </p>
            <p>Last success: {formatDate(trakt.poll.last_success)}</p>
            {trakt.poll.last_error && (
              <small className="warning">
                Latest sync error: {trakt.poll.last_error}
              </small>
            )}
            <button
              className="primary"
              disabled={syncBusy || trakt.sync.running || !trakt.sync.can_sync}
              onClick={() => void syncNow()}
            >
              {syncBusy || trakt.sync.running
                ? "Syncing…"
                : trakt.sync.retry_allowed
                  ? "Retry sync"
                  : "Sync now"}
            </button>
            {(syncBusy || trakt.sync.running) && (
              <SyncProgress phase={trakt.poll.phase} />
            )}
            {trakt.sync.last_result && (
              <div className="sync-summary">
                <p>
                  Last completed:{" "}
                  {formatDate(trakt.sync.last_result.completed_at)}
                </p>
                <p>
                  {trakt.sync.last_result.history_changes} history change(s),{" "}
                  {trakt.sync.last_result.rating_changes} rating change(s),{" "}
                  {trakt.sync.last_result.pending_ratings_completed} pending
                  rating(s) sent.
                </p>
                {trakt.sync.last_result.pending_ratings_remaining > 0 && (
                  <p>
                    {trakt.sync.last_result.pending_ratings_remaining} rating(s)
                    still awaiting sync.
                  </p>
                )}
              </div>
            )}
            <p>Next scheduled run: {formatDate(trakt.sync.next_run)}</p>
            {trakt.sync.last_error && (
              <small className="warning">
                Latest full-sync error: {trakt.sync.last_error}
              </small>
            )}
          </div>
        )}
        {traktConnected && !traktEditorOpen && (
          <button
            className="secondary credential-toggle"
            onClick={() => setTraktEditorOpen(true)}
          >
            Edit Trakt configuration
          </button>
        )}
        {showTraktEditor && (
          <>
            <div className="override-note">
              Saved credentials are encrypted and write-only. Environment
              overrides, when present, lock those fields.
            </div>
            <div className="credential-fields">
              <label>
                Client ID
                <input
                  disabled={setup.trakt.client_id_overridden}
                  value={clientID}
                  onChange={(e) => setClientID(e.target.value)}
                  autoComplete="off"
                  placeholder={
                    setup.trakt.client_id_overridden
                      ? "Locked by environment"
                      : setup.trakt.configured
                        ? "Configured — enter to replace"
                        : "Paste Trakt client ID"
                  }
                />
              </label>
              <label>
                Client secret
                <input
                  disabled={setup.trakt.client_secret_overridden}
                  type="password"
                  value={clientSecret}
                  onChange={(e) => setClientSecret(e.target.value)}
                  autoComplete="new-password"
                  placeholder={
                    setup.trakt.client_secret_overridden
                      ? "Locked by environment"
                      : setup.trakt.configured
                        ? "Configured — enter to replace"
                        : "Paste Trakt client secret"
                  }
                />
              </label>
            <div className="settings-actions">
              <button
                className="secondary"
                disabled={
                  integrationBusy ||
                  (!setup.trakt.configured &&
                    ((!setup.trakt.client_id_overridden && !clientID) ||
                      (!setup.trakt.client_secret_overridden && !clientSecret)))
                }
                onClick={() => void saveTraktCredentials()}
              >
                Save Trakt credentials
              </button>
              {traktConnected && (
                <button
                  className="text-button"
                  onClick={() => {
                    setClientID("");
                    setClientSecret("");
                    setTraktEditorOpen(false);
                  }}
                >
                  Cancel editing
                </button>
              )}
            </div>
          </div>
          </>
        )}
      </div>
      <div className="settings-card">
        <p className="eyebrow">PREFERENCES</p>
        <h2>Local behavior</h2>
        <p className="mobile-version">Running version: <strong>{updateStatus?.running_version || buildVersion}</strong></p>
        <label>
          Timezone
          <TimezoneSelector
            value={settings.timezone}
            onChange={(timezone) => setSettings({ ...settings, timezone })}
          />
        </label>
        <label>
          Trakt polling interval (minutes)
          <input
            type="number"
            min="1"
            max="1440"
            value={settings.trakt_poll_minutes}
            onChange={(e) =>
              setSettings({
                ...settings,
                trakt_poll_minutes: Number(e.target.value),
              })
            }
          />
        </label>
        <div className="toggle-row">
          <div>
            <strong>Update checks</strong>
            <small>Periodically check GitHub Releases. WatchWeaver never updates or restarts itself.</small>
          </div>
          <button
            role="switch"
            aria-label="Update checks"
            aria-checked={settings.update_checks_enabled}
            className={`toggle ${settings.update_checks_enabled ? "on" : ""}`}
            onClick={() => setSettings({ ...settings, update_checks_enabled: !settings.update_checks_enabled })}
          ><span /></button>
        </div>
        <div className="toggle-row">
          <div>
            <strong>Prompt for ratings</strong>
            <small>Ask for a rating after eligible movie watches, season completions, and caught-up episodes.</small>
          </div>
          <button role="switch" aria-label="Prompt for ratings" aria-checked={settings.prompt_ratings_enabled} className={`toggle ${settings.prompt_ratings_enabled ? "on" : ""}`} onClick={() => setSettings({...settings,prompt_ratings_enabled:!settings.prompt_ratings_enabled})}><span /></button>
        </div>
        <div className="toggle-row">
          <div>
            <strong>Prompt for optional reviews</strong>
            <small>Offer an optional written review for movies and completed seasons. Episodes remain user-initiated from History.</small>
          </div>
          <button role="switch" aria-label="Prompt for optional reviews" aria-checked={settings.prompt_reviews_enabled} className={`toggle ${settings.prompt_reviews_enabled ? "on" : ""}`} onClick={() => setSettings({...settings,prompt_reviews_enabled:!settings.prompt_reviews_enabled})}><span /></button>
        </div>
        <div className="toggle-row">
          <div>
            <strong>Movie prompts</strong>
            <small>Allow the selected prompt types for new movie watches.</small>
          </div>
          <button
            role="switch"
            aria-label="Movie prompts"
            aria-checked={settings.prompt_movies_enabled}
            className={`toggle ${settings.prompt_movies_enabled ? "on" : ""}`}
            onClick={() =>
              setSettings({
                ...settings,
                prompt_movies_enabled: !settings.prompt_movies_enabled,
              })
            }
          >
            <span />
          </button>
        </div>
        <div className="toggle-row">
          <div>
            <strong>TV prompts</strong>
            <small>Allow the selected prompt types for season completions and eligible caught-up episodes.</small>
          </div>
          <button
            role="switch"
            aria-label="TV prompts"
            aria-checked={settings.prompt_tv_enabled}
            className={`toggle ${settings.prompt_tv_enabled ? "on" : ""}`}
            onClick={() =>
              setSettings({
                ...settings,
                prompt_tv_enabled: !settings.prompt_tv_enabled,
              })
            }
          >
            <span />
          </button>
        </div>
        <div className="toggle-row">
          <div>
            <strong>Serializd reminders</strong>
            <small>Track transferable TV changes.</small>
          </div>
          <button
            role="switch"
            aria-label="Serializd reminders"
            aria-checked={settings.serializd_enabled}
            className={`toggle ${settings.serializd_enabled ? "on" : ""}`}
            onClick={() =>
              setSettings({
                ...settings,
                serializd_enabled: !settings.serializd_enabled,
              })
            }
          >
            <span />
          </button>
        </div>
        <div className="two-col">
          <label>
            Transferable-change threshold
            <input
              type="number"
              min="1"
              value={settings.serializd_reminder_changes}
              onChange={(e) =>
                setSettings({
                  ...settings,
                  serializd_reminder_changes: Number(e.target.value),
                })
              }
            />
            <small>Remind after this many TV changes since your last confirmed Serializd import.</small>
          </label>
          <label>
            Elapsed-day threshold
            <input
              type="number"
              min="1"
              value={settings.serializd_reminder_days}
              onChange={(e) =>
                setSettings({
                  ...settings,
                  serializd_reminder_days: Number(e.target.value),
                })
              }
            />
            <small>Remind after this many days since your last confirmed import.</small>
          </label>
        </div>
        <p className="threshold-summary">
          Remind me after {settings.serializd_reminder_changes} transferable change{settings.serializd_reminder_changes === 1 ? "" : "s"} or {settings.serializd_reminder_days} day{settings.serializd_reminder_days === 1 ? "" : "s"}, whichever comes first. Confirming an import resets both counters; it does not delete history.
        </p>
        <button className="primary" onClick={() => void save()}>
          Save preferences
        </button>
      </div>
      <div className="settings-card full">
        <div className="card-heading">
          <div>
            <p className="eyebrow">OPTIONAL ANNOUNCEMENTS</p>
            <h2>Discord</h2>
          </div>
          <span
            className={`state ${integrations.discord.enabled ? "confirmed" : ""}`}
          >
            {integrations.discord.status}
          </span>
        </div>
        <p>Outbound-only announcements through a write-only webhook.</p>
        <div className="discord-settings">
          <div className="toggle-row">
            <div>
              <strong>Enable Discord</strong>
              <small>Send outbound WatchWeaver announcements.</small>
            </div>
            <button
              role="switch"
              aria-label="Enable Discord announcements"
              aria-checked={discordEnabled}
              className={`toggle ${discordEnabled ? "on" : ""}`}
              onClick={() => setDiscordEnabled(!discordEnabled)}
            >
              <span />
            </button>
          </div>
          <label>
            Discord webhook URL
            <input
              disabled={setup.discord.webhook_overridden}
              type="password"
              value={webhook}
              onChange={(e) => setWebhook(e.target.value)}
              autoComplete="new-password"
              placeholder={
                setup.discord.webhook_overridden
                  ? "Locked by environment"
                  : setup.discord.configured
                    ? "Configured — enter to replace"
                    : "https://discord.com/api/webhooks/…"
              }
            />
          </label>
          <div className="settings-actions">
            <button
              className="primary"
              disabled={
                integrationBusy ||
                (discordEnabled && !webhook && !setup.discord.configured)
              }
              onClick={() => void saveDiscord()}
            >
              Save Discord
            </button>
            {setup.discord.configured && (
              <button
                className="secondary"
                disabled={integrationBusy}
                onClick={() => void testDiscord()}
              >
                Send test
              </button>
            )}
          </div>
        </div>
        <div className="override-note">
          Saved webhook URLs are encrypted and write-only. WatchWeaver reports
          configuration status without returning the stored value.
        </div>
      </div>
    </section>
  );
}

function UpdateCard({ status, onUpdate, onError }: { status?: UpdateStatus; onUpdate: (value: UpdateStatus) => void; onError: (value: string) => void }) {
  const [busy, setBusy] = useState(false);
  const check = async () => {
    setBusy(true);
    try { onUpdate(await request<UpdateStatus>("/api/update?force=1")); }
    catch (e) { onError((e as Error).message); }
    finally { setBusy(false); }
  };
  if (!status) return <article className="settings-card full"><Loading /></article>;
  const labels: Record<UpdateStatus["state"], string> = {
    up_to_date: "Up to date", beta_update_available: "Beta update available",
    stable_update_available: "Stable update available", unable: "Unable to check",
    disabled: "Update checks disabled", development: "Development build",
  };
  const available = status.state.endsWith("_update_available");
  return <article className="settings-card full update-card">
    <div className="card-heading"><div><p className="eyebrow">VERSION</p><h2>{labels[status.state]}</h2></div><span className={`state ${available || status.state === "unable" ? "pending" : "confirmed"}`}>{status.channel}</span></div>
    <p>Running <strong>{status.running_version}</strong>{status.latest_version && <> · Newest {status.latest_version}</>}</p>
    {status.checked_at && <p>Last checked: {formatDate(status.checked_at)}</p>}
    {available && status.release_url && <p><a href={status.release_url} target="_blank" rel="noreferrer">View release notes or changes ↗</a></p>}
    {available && <p>In Portainer, pull the matching WatchWeaver image and recreate the stack. Your data remains in the mounted <code>/data</code> volume.</p>}
    <button className="secondary" disabled={busy || !status.enabled || status.channel === "development"} onClick={() => void check()}>{busy ? "Checking…" : "Check for updates"}</button>
  </article>;
}

function Metric({
  value,
  label,
  tone,
}: {
  value: string | number;
  label: string;
  tone?: string;
}) {
  return (
    <div className={`metric ${tone || ""}`}>
      <strong>{value}</strong>
      <span>{label}</span>
    </div>
  );
}
function Empty({ title, body }: { title: string; body: string }) {
  return (
    <div className="empty">
      <span>✓</span>
      <h3>{title}</h3>
      <p>{body}</p>
    </div>
  );
}
function SyncProgress({ phase }: { phase?: string }) {
  return (
    <div className="sync-progress" role="status" aria-live="polite">
      <div>
        <strong>Syncing with Trakt…</strong>
        <span>{humanStatus(phase || "processing activity")}</span>
      </div>
      <div className="sync-progress-track" aria-hidden="true">
        <span />
      </div>
      <small>
        Large first imports can take a while. New prompts will appear here as
        processing finishes.
      </small>
    </div>
  );
}
function Loading() {
  return (
    <div className="loading" aria-label="Loading">
      <span />
      <span />
      <span />
    </div>
  );
}
function Pager({
  page,
  pages,
  setPage,
}: {
  page: number;
  pages: number;
  setPage: (n: number) => void;
}) {
  if (pages < 2) return null;
  return (
    <div className="pager">
      <button
        className="secondary"
        disabled={page <= 1}
        onClick={() => setPage(page - 1)}
      >
        ← Previous
      </button>
      <span>
        Page {page} of {pages}
      </span>
      <button
        className="secondary"
        disabled={page >= pages}
        onClick={() => setPage(page + 1)}
      >
        Next →
      </button>
    </div>
  );
}
export default App;
