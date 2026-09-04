import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import type { Task } from "./api";

const integrations = {
  trakt: {
    authorization: { status: "connected" },
    poll: { phase: "polling", consecutive_failures: 0 },
    sync: { running: false, can_sync: true, retry_allowed: false },
  },
  letterboxd: { enabled: true, status: "available" },
  serializd: { enabled: true, status: "enabled" },
  discord: { enabled: false, status: "disabled" },
};
let currentIntegrations = integrations;
const settings = {
  timezone: "UTC",
  trakt_poll_minutes: 5,
  prompt_movies_enabled: true,
  prompt_tv_enabled: true,
  serializd_enabled: true,
  serializd_reminder_changes: 20,
  serializd_reminder_days: 14,
  update_checks_enabled: true,
};
const task: Task = {
  id: 4,
  type: "rating_review",
  state: "pending",
  created_at: "2026-09-02T00:00:00Z",
  media: {
    id: 9,
    type: "movie",
    title: "The Example",
    year: 2026,
    external_ids: { trakt: "9" },
  },
};
let activeTask = task;
function json(body: unknown, status = 200) {
  return Promise.resolve(
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    }),
  );
}

beforeEach(() => {
  activeTask = task;
  currentIntegrations = integrations;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === "/api/integrations") return json(currentIntegrations);
      if (path === "/api/setup") return json({ complete: true, encrypted_storage: true, trakt: { configured: true, authorization_status: "connected", client_id_overridden: false, client_secret_overridden: false }, discord: { configured: true, enabled: false, webhook_overridden: false } });
      if (path === "/api/update" || path === "/api/update?force=1") return json({ state: "beta_update_available", running_version: "0.1.0-beta.1", latest_version: "0.1.0-beta.2", release_url: "https://example/releases/2", channel: "beta", checked_at: "2026-09-02T12:00:00Z", enabled: true });
      if (path === "/api/inbox")
        return json({
          page: 1,
          per_page: 50,
          total: 1,
          total_pages: 1,
          items: [activeTask],
        });
      if (path.startsWith("/api/history"))
        return json({ page: 1, per_page: 20, total: 1, total_pages: 1, items: [{ id: 1, source: "trakt", watched_at: "2026-09-01T12:00:00Z", source_watched_at: "2026-09-01T12:00:00Z", media: task.media }] });
      if (path === "/api/media/9/rating")
        return json({ media_id: 9, rating: 8, stars: 4 });
      if (path === "/api/media/9/review" && !init?.method)
        return json({ error: "not found" }, 404);
      if (path === "/api/media/9/review" && init?.method === "PUT")
        return json({ media_id: 9, body: "Excellent." });
      if (path === "/api/settings") return json(settings);
      if (path === "/api/serializd")
        return json({
          enabled: true,
          pending_changes: 2,
          pending_episode_watches: 2,
          pending_rating_changes: 0,
          tracked_episode_watches: 42,
          count_threshold_reached: false,
          elapsed_threshold_reached: false,
          due: false,
          unsupported_season_ratings: 0,
          unsupported_tv_reviews: 0,
          reminder_changes: 20,
          reminder_days: 14,
          import_url: "https://serializd.example/import",
        });
      if (path === "/api/letterboxd")
        return json({
          pending_rows: 1,
          pending_events: 1,
          duplicate_warnings: 0,
          generated_batches: 0,
        });
      if (path === "/api/letterboxd/batches") return json({ items: [] });
      if (path === "/api/serializd/mark-synced" && init?.method === "POST")
        return json({
          enabled: true,
          pending_changes: 0,
          pending_episode_watches: 0,
          pending_rating_changes: 0,
          tracked_episode_watches: 42,
          count_threshold_reached: false,
          elapsed_threshold_reached: false,
          due: false,
          unsupported_season_ratings: 0,
          unsupported_tv_reviews: 0,
          reminder_changes: 20,
          reminder_days: 14,
          import_url: "https://serializd.example/import",
          last_confirmed_at: "2026-09-02T12:00:00Z",
        });
      if (path === "/api/integrations/trakt/sync" && init?.method === "POST")
        return json({ running: false });
      if (path === "/api/status")
        return json({
          overall: "needs_attention",
          checked_at: "2026-09-02T12:00:00Z",
          components: {
            trakt: {
              state: "working",
              label: "Trakt",
              detail: "Connected and ready to synchronize.",
              action: "sync",
            },
            discord: {
              state: "disabled",
              label: "Discord",
              detail: "Optional announcements are disabled.",
              action: "configure",
            },
            letterboxd: {
              state: "working",
              label: "Letterboxd",
              detail: "No movie exports are waiting.",
              action: "open",
            },
            serializd: {
              state: "working",
              label: "Serializd",
              detail: "Reminder thresholds have not been reached.",
              action: "open",
            },
            database: {
              state: "working",
              label: "Database",
              detail: "Persistent storage is available.",
              action: "diagnostics",
            },
          },
          backup: {
            state: "needs_attention",
            label: "Backups",
            detail: "No application backup was found.",
            action: "instructions",
          },
        });
      if (path.startsWith("/api/tasks/") && init?.method === "POST")
        return json({ state: "completed" });
      return json({ error: "not found" }, 404);
    }),
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("WatchWeaver dashboard", () => {
  it("renders the actionable inbox and existing current rating", async () => {
    render(<App />);
    expect(document.querySelector(".brand-mark")).toHaveAttribute(
      "src",
      "/brand/watchweaver-icon.png",
    );
    expect(
      screen.getByRole("heading", { level: 1, name: "Inbox" }),
    ).toBeInTheDocument();
    expect(await screen.findByText("The Example")).toBeInTheDocument();
    expect(screen.getByText(/Current rating:/)).toHaveTextContent("4 ★");
    expect(
      screen.getByText(/does not create a historical snapshot/i),
    ).toBeInTheDocument();
  });
  it("does not show the ambiguous local refresh button", async () => {
    render(<App />);
    await screen.findByText("The Example");
    expect(screen.queryByRole("button", { name: /Refresh Inbox data/ })).not.toBeInTheDocument();
  });
  it("navigates to Serializd status and settings without rendering secrets", async () => {
    render(<App />);
    await screen.findByText("The Example");
    fireEvent.click(screen.getByRole("button", { name: /Television/ }));
    expect(
      await screen.findByText("Television is in rhythm"),
    ).toBeInTheDocument();
    expect(screen.getByText("42")).toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: "I completed the Serializd import",
      }),
    ).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: /Settings/ }));
    expect(
      await screen.findByText("Integration availability"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/webhook-secret|access_token|refresh_token/i),
    ).not.toBeInTheDocument();
  });
  it("uses aligned local icons and explains Trakt API access on Status", async () => {
    render(<App />);
    await screen.findByText("The Example");
    const navigation = screen.getByRole("navigation", {
      name: "Main navigation",
    });
    expect(navigation.querySelectorAll(".nav-icon")).toHaveLength(6);
    expect(
      screen.getByRole("button", { name: "Inbox" }).querySelector(".inbox"),
    ).toBeInTheDocument();
    expect(
      screen
        .getByRole("button", { name: "Settings" })
        .querySelector(".settings"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Status" }));
    expect(
      await screen.findByText("Private network required"),
    ).toBeInTheDocument();
    expect(screen.getAllByText("Private network required")).toHaveLength(1);
    expect(
      screen.getByText(
        "Trakt VIP is currently required for new API applications.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/This is a Trakt policy, not a WatchWeaver subscription/i),
    ).toBeInTheDocument();
  });
  it("submits exact canonical rating values", async () => {
    render(<App />);
    await screen.findByText("The Example");
    fireEvent.click(screen.getByTitle("4.5 stars"));
    fireEvent.click(screen.getByRole("button", { name: "Save & complete" }));
    await waitFor(() =>
      expect(fetch).toHaveBeenCalledWith(
        "/api/tasks/4/complete",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({ rating: 9 }),
        }),
      ),
    );
  });
  it("keeps episode prompts rating-only while directing reviews to History", async () => {
    activeTask = {
      ...task,
      id: 5,
      media: {
        ...task.media,
        id: 10,
        type: "episode",
        title: "A Standout Episode",
        show_title: "Example Show",
        season_number: 1,
        episode_number: 4,
      },
    };
    render(<App />);
    await screen.findByText("A Standout Episode");
    expect(screen.queryByLabelText("Review for A Standout Episode")).not.toBeInTheDocument();
    expect(screen.getByText(/Add an optional episode review from History/i)).toBeInTheDocument();
    fireEvent.click(screen.getByTitle("4.5 stars"));
    fireEvent.click(screen.getByRole("button", { name: "Save & complete" }));
    await waitFor(() =>
      expect(fetch).toHaveBeenCalledWith(
        "/api/tasks/5/complete",
        expect.objectContaining({
          body: JSON.stringify({ rating: 9 }),
        }),
      ),
    );
  });
  it("rates and reviews a movie directly from History", async () => {
    render(<App />);
    await screen.findByText("The Example");
    fireEvent.click(screen.getByRole("button", { name: /History/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Rate or review" }));
    expect(await screen.findByText(/included in your next Letterboxd CSV/i)).toBeInTheDocument();
    fireEvent.click(screen.getByTitle("4.5 stars"));
    fireEvent.change(screen.getByLabelText("Review for This movie"), { target: { value: "Excellent." } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledWith("/api/media/9/rating", expect.objectContaining({ method: "PUT", body: JSON.stringify({ rating: 9 }) })));
    expect(fetch).toHaveBeenCalledWith("/api/media/9/review", expect.objectContaining({ method: "PUT", body: JSON.stringify({ body: "Excellent." }) }));
  });
  it("runs Trakt synchronization from settings", async () => {
    render(<App />);
    await screen.findByText("The Example");
    fireEvent.click(screen.getByRole("button", { name: /Settings/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Sync now" }));
    await waitFor(() =>
      expect(fetch).toHaveBeenCalledWith(
        "/api/integrations/trakt/sync",
        expect.objectContaining({ method: "POST" }),
      ),
    );
  });
  it("manages Discord configuration from settings", async () => {
    render(<App />);
    await screen.findByText("The Example");
    fireEvent.click(screen.getByRole("button", { name: /Settings/ }));
    fireEvent.click(await screen.findByRole("switch", { name: "Enable Discord announcements" }));
    fireEvent.change(screen.getByLabelText("Discord webhook URL"), { target: { value: "https://discord.com/api/webhooks/example" } });
    fireEvent.click(screen.getByRole("button", { name: "Save Discord" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledWith("/api/integrations/discord/config", expect.objectContaining({ method: "PUT", body: JSON.stringify({ webhook_url: "https://discord.com/api/webhooks/example", enabled: true }) })));
  });
  it("immediately shows the confirmed Serializd checkpoint", async () => {
    render(<App />);
    await screen.findByText("The Example");
    fireEvent.click(screen.getByRole("button", { name: /Television/ }));
    fireEvent.click(
      await screen.findByRole("button", {
        name: "I completed the Serializd import",
      }),
    );
    await waitFor(() =>
      expect(screen.queryByText("Never")).not.toBeInTheDocument(),
    );
    expect(screen.getByText(/Sep 2, 2026/)).toBeInTheDocument();
  });
  it("shows operational status and runs its recovery action", async () => {
    render(<App />);
    await screen.findByText("The Example");
    fireEvent.click(screen.getByRole("button", { name: /Status/ }));
    expect(
      await screen.findByText("Some items need attention"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("No application backup was found."),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Sync now" }));
    await waitFor(() =>
      expect(fetch).toHaveBeenCalledWith(
        "/api/integrations/trakt/sync",
        expect.objectContaining({ method: "POST" }),
      ),
    );
    expect(
      screen.getByRole("link", { name: "Download diagnostics" }),
    ).toHaveAttribute("href", "/api/diagnostics");
  });
  it("shows scheduled polling and refreshes the inbox when it completes", async () => {
    let inboxCalls = 0;
    const originalFetch = fetch as ReturnType<typeof vi.fn>;
    originalFetch.mockImplementation((input: RequestInfo | URL) => {
      const path = String(input);
      if (path === "/api/inbox") { inboxCalls++; return json({ page: 1, per_page: 50, total: 0, total_pages: 0, items: [] }); }
      if (path === "/api/integrations") return json(currentIntegrations);
      if (path === "/api/update") return json({ state: "development", running_version: "dev", channel: "development", enabled: true });
      return json({ error: "not found" }, 404);
    });
    currentIntegrations = { ...integrations, trakt: { ...integrations.trakt, sync: { ...integrations.trakt.sync, running: true } } };
    render(<App />);
    expect(await screen.findByText(/Syncing with Trakt/i)).toBeInTheDocument();
    expect(screen.queryByText("No rating prompts waiting")).not.toBeInTheDocument();
    currentIntegrations = integrations;
    await new Promise((resolve) => window.setTimeout(resolve, 3100));
    await waitFor(() => expect(screen.getByText("No rating prompts waiting")).toBeInTheDocument());
    expect(inboxCalls).toBeGreaterThan(1);
  }, 5000);
  it("treats scheduled polling as idle rather than an active sync", async () => {
    render(<App />);
    expect(await screen.findByText("The Example")).toBeInTheDocument();
    expect(screen.queryByText(/Syncing with Trakt/i)).not.toBeInTheDocument();
  });
  it("shows the running version and update guidance", async () => {
    render(<App />);
    expect(await screen.findByText("Version 0.1.0-beta.1")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Status" }));
    expect(await screen.findByText("Beta update available")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /release notes or changes/ })).toHaveAttribute("href", "https://example/releases/2");
    fireEvent.click(screen.getByRole("button", { name: "Check for updates" }));
    await waitFor(() => expect(fetch).toHaveBeenCalledWith("/api/update?force=1", expect.anything()));
  });
});
