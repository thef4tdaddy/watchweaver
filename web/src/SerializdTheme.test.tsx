import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";

let due = false;

function json(body: unknown, status = 200) {
  return Promise.resolve(
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    }),
  );
}

beforeEach(() => {
  due = false;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const path = String(input);
      if (path === "/api/integrations")
        return json({
          trakt: {
            authorization: { status: "connected" },
            poll: { phase: "idle", consecutive_failures: 0 },
            sync: { running: false, can_sync: true, retry_allowed: false },
          },
          letterboxd: { enabled: true, status: "available" },
          serializd: { enabled: true, status: "enabled" },
          discord: { enabled: false, status: "disabled" },
        });
      if (path === "/api/inbox")
        return json({
          page: 1,
          per_page: 50,
          total: 0,
          total_pages: 1,
          items: [],
        });
      if (path === "/api/serializd")
        return json({
          enabled: true,
          pending_changes: due ? 21 : 5,
          pending_episode_watches: due ? 20 : 5,
          pending_rating_changes: due ? 1 : 0,
          tracked_episode_watches: 42,
          count_threshold_reached: due,
          elapsed_threshold_reached: false,
          due,
          unsupported_season_ratings: 0,
          unsupported_tv_reviews: 0,
          reminder_changes: 20,
          reminder_days: 14,
          import_url: "https://serializd.example/import",
        });
      return json({ error: "not found" }, 404);
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("Serializd checkpoint theme states", () => {
  it("renders the normal checkpoint with themed actions", async () => {
    render(<App />);
    fireEvent.click(screen.getByRole("button", { name: /Television/ }));

    const heading = await screen.findByText("Television is in rhythm");
    const panel = heading.closest(".hero-panel");
    expect(panel).toHaveClass("tv");
    expect(panel).not.toHaveClass("due");
    expect(screen.getByRole("link", { name: /Open importer/ })).toHaveClass(
      "primary",
    );
    expect(screen.getByRole("button", { name: "Mark synced" })).toHaveClass(
      "secondary",
    );
  });

  it("renders the due checkpoint with the warning-state theme hook", async () => {
    due = true;
    render(<App />);
    fireEvent.click(screen.getByRole("button", { name: /Television/ }));

    const heading = await screen.findByText("Your Trakt import is due");
    expect(heading.closest(".hero-panel")).toHaveClass("tv", "due");
    expect(screen.getByText("21 transferable changes since your last confirmation."))
      .toBeInTheDocument();
  });
});
