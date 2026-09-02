import { useCallback, useEffect, useState } from "react";
import "./App.css";
import {
  APIError,
  type Batch,
  type HistoryItem,
  type Integrations,
  type LetterboxdStatus,
  type OperationalStatus,
  type Page,
  type Rating,
  type SerializdStatus,
  type Settings,
  type Task,
  request,
} from "./api";

type View = "inbox" | "history" | "movies" | "tv" | "status" | "settings";
const nav: [View, string, string][] = [
  ["inbox", "Inbox", "⌁"],
  ["history", "History", "◷"],
  ["movies", "Movies", "◇"],
  ["tv", "Television", "▱"],
  ["status", "Status", "!"],
  ["settings", "Settings", "⚙"],
];
const stars = Array.from({ length: 10 }, (_, i) => i + 1);

function App() {
  const [view, setView] = useState<View>("inbox");
  const [integrations, setIntegrations] = useState<Integrations>();
  const [error, setError] = useState("");
  const refreshIntegrations = useCallback(
    () =>
      request<Integrations>("/api/integrations")
        .then(setIntegrations)
        .catch((e) => setError(e.message)),
    [],
  );
  useEffect(() => {
    void refreshIntegrations();
  }, [refreshIntegrations]);
  useEffect(() => {
    const timer = window.setInterval(() => void refreshIntegrations(), 3000);
    return () => window.clearInterval(timer);
  }, [refreshIntegrations]);
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
            >
              <span>{icon}</span>
              {label}
              {id === "inbox" && <i>•</i>}
            </button>
          ))}
        </nav>
        <div className="sidebar-foot">
          <StatusDot
            ok={integrations?.trakt.authorization.status === "connected"}
            label={`Trakt · ${humanStatus(integrations?.trakt.authorization.status)}`}
          />
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
          <button
            className="icon-button"
            onClick={() => location.reload()}
            aria-label="Refresh"
          >
            ↻
          </button>
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
          />
        )}{" "}
        {view === "history" && <History onError={setError} />}{" "}
        {view === "movies" && <Movies onError={setError} />}{" "}
        {view === "tv" && <Television onError={setError} />}{" "}
        {view === "status" && (
          <StatusView
            onNavigate={setView}
            onError={setError}
            refreshIntegrations={refreshIntegrations}
          />
        )}{" "}
        {view === "settings" && (
          <SettingsView
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
  onNavigate,
  onError,
  refreshIntegrations,
}: {
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
  const entries = [
    ...Object.entries(status.components),
    ["backup", status.backup] as const,
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
      <div className="settings-grid">
        {entries.map(([name, component]) => (
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
              {component.action === "diagnostics" && (
                <a className="primary" href="/api/diagnostics" download>
                  Download diagnostics
                </a>
              )}
            </div>
          </article>
        ))}
      </div>
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
function humanStatus(value?: string) {
  return (value || "not configured").replaceAll("_", " ");
}
function formatDate(value?: string) {
  if (!value) return "Never";
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(value));
}
function mediaLabel(media: Task["media"]) {
  if (media.type === "episode")
    return `${media.show_title} · S${media.season_number} E${media.episode_number}`;
  return [media.title, media.year].filter(Boolean).join(" · ");
}

function Inbox({ onError, syncRunning, syncPhase }: { onError: (value: string) => void; syncRunning: boolean; syncPhase?: string }) {
  const [page, setPage] = useState<Page<Task>>();
  const [ratings, setRatings] = useState<Record<number, number>>({});
  const [drafts, setDrafts] = useState<
    Record<number, { rating?: number; review: string }>
  >({});
  const [busy, setBusy] = useState<number>();
  const load = useCallback(async () => {
    try {
      const data = await request<Page<Task>>("/api/inbox");
      setPage(data);
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
  }, [onError]);
  // oxlint-disable react/set-state-in-effect -- the effect starts an external API synchronization.
  useEffect(() => {
    void load();
  }, [load]);
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
        <button className="secondary" onClick={() => void load()}>
          Refresh
        </button>
      </div>
      {syncRunning && <SyncProgress phase={syncPhase} />}
      {page.items.length === 0 && !syncRunning ? (
        <Empty
          title="You’re all caught up"
          body="New rating and review prompts will appear here after Trakt activity is processed."
        />
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
                  {existing && (
                    <div className="current-note">
                      Current rating: <strong>{existing / 2} ★</strong> ·
                      Submitting changes the current rating; it does not create
                      a historical snapshot.
                    </div>
                  )}
                  <div
                    className="rating-row"
                    aria-label={`Rating for ${task.media.title}`}
                  >
                    {stars.map((value) => (
                      <button
                        key={value}
                        title={`${value / 2} stars`}
                        className={(draft.rating || 0) >= value ? "filled" : ""}
                        onClick={() =>
                          setDrafts((d) => ({
                            ...d,
                            [task.id]: { ...draft, rating: value },
                          }))
                        }
                      >
                        {value % 2 === 0 ? "★" : "◐"}
                      </button>
                    ))}
                    <b>
                      {draft.rating
                        ? `${draft.rating / 2} / 5`
                        : "Choose rating"}
                    </b>
                  </div>
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
                  <div className="actions">
                    <button
                      className="primary"
                      disabled={
                        busy === task.id ||
                        (!draft.rating && !draft.review.trim())
                      }
                      onClick={() =>
                        void act(task.id, "complete", {
                          ...(draft.rating ? { rating: draft.rating } : {}),
                          ...(draft.review.trim()
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
              </div>
            </article>
          ))}
        </div>
      )}
      <Pager page={data.page} pages={data.total_pages} setPage={setPageNo} />
    </section>
  );
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
  const mark = async () => {
    setMarking(true);
    try {
      await request("/api/serializd/mark-synced", { method: "POST" });
      await load();
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
            {marking ? "Saving…" : "Mark synced"}
          </button>
        </div>
      </div>
      <div className="metric-grid">
        <Metric value={status.tracked_episode_watches} label="Episodes tracked" />
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
          label="Last confirmed"
        />
      </div>
      {!status.last_confirmed_at && status.tracked_episode_watches > 0 && (
        <div className="notice">
          <strong>Imported history is ready</strong>
          <p>
            Existing Trakt history is your starting baseline, so it is tracked
            without being counted as new. After running the Serializd importer,
            choose Mark synced to start counting changes from today.
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
    </section>
  );
}

function SettingsView({
  integrations,
  refreshIntegrations,
  onError,
}: {
  integrations?: Integrations;
  refreshIntegrations: () => void;
  onError: (v: string) => void;
}) {
  const [settings, setSettings] = useState<Settings>();
  const [authBusy, setAuthBusy] = useState(false);
  const [syncBusy, setSyncBusy] = useState(false);
  useEffect(() => {
    request<Settings>("/api/settings")
      .then(setSettings)
      .catch((e) => onError(e.message));
  }, [onError]);
  const save = async () => {
    if (!settings) return;
    try {
      setSettings(
        await request<Settings>("/api/settings", {
          method: "PUT",
          body: JSON.stringify(settings),
        }),
      );
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
  if (!settings || !integrations) return <Loading />;
  const trakt = integrations.trakt;
  return (
    <section className="settings-grid">
      <div className="settings-card">
        <div className="card-heading">
          <div>
            <p className="eyebrow">REQUIRED SOURCE</p>
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
        <div className="override-note">
          Manage write-only Trakt credentials with “Configure integrations.”
          Environment overrides, when present, lock those fields.
        </div>
      </div>
      <div className="settings-card">
        <p className="eyebrow">PREFERENCES</p>
        <h2>Local behavior</h2>
        <label>
          Timezone
          <input
            value={settings.timezone}
            onChange={(e) =>
              setSettings({ ...settings, timezone: e.target.value })
            }
          />
          <small>Use an IANA timezone such as America/Chicago.</small>
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
            <strong>Movie prompts</strong>
            <small>Create rating/review tasks for new movie watches.</small>
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
            <small>Create supported season and episode rating tasks.</small>
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
            Change threshold
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
          </label>
          <label>
            Day threshold
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
          </label>
        </div>
        <button className="primary" onClick={() => void save()}>
          Save preferences
        </button>
      </div>
      <div className="settings-card full">
        <p className="eyebrow">OPTIONAL SERVICES</p>
        <h2>Integration availability</h2>
        <div className="integration-row">
          <div>
            <strong>Letterboxd</strong>
            <small>Official CSV export workflow</small>
          </div>
          <span className="state confirmed">Available</span>
        </div>
        <div className="integration-row">
          <div>
            <strong>Discord announcements</strong>
            <small>Outbound-only; managed through encrypted setup</small>
          </div>
          <span
            className={`state ${integrations.discord.enabled ? "confirmed" : ""}`}
          >
            {integrations.discord.status}
          </span>
        </div>
        <div className="override-note">
          Saved webhook URLs are encrypted and write-only. WatchWeaver reports
          configuration status without returning the stored value.
        </div>
      </div>
    </section>
  );
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
