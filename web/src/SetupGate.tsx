import { useEffect, useState } from "react";
import App from "./App";
import { request } from "./api";
import "./setup.css";

type Setup = {
  complete: boolean;
  encrypted_storage: boolean;
  trakt: {
    configured: boolean;
    authorization_status: string;
    client_id_overridden: boolean;
    client_secret_overridden: boolean;
  };
  discord: {
    configured: boolean;
    enabled: boolean;
    webhook_overridden: boolean;
  };
};
type Authorization = {
  status: string;
  user_code?: string;
  verification_url?: string;
  poll_after_seconds?: number;
};

function friendlySetupError(message: string) {
  const lower = message.toLowerCase();
  if (lower.includes("expired")) return "The Trakt device code expired. Start authorization again to get a new code.";
  if (lower.includes("401") || lower.includes("unauthorized") || lower.includes("invalid client")) return "Trakt rejected these credentials. Check the Client ID and Secret in your Trakt API application, then save them again.";
  if (lower.includes("revoked")) return "Trakt authorization was revoked. Start authorization again and approve the new device code.";
  return message;
}

async function copyText(value: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const field = document.createElement("textarea");
  field.value = value;
  field.style.position = "fixed";
  field.style.opacity = "0";
  document.body.appendChild(field);
  field.select();
  const copied = document.execCommand("copy");
  field.remove();
  if (!copied) throw new Error("Copy is unavailable in this browser. Select and copy the redirect URI manually.");
}

export default function SetupGate() {
  const [setup, setSetup] = useState<Setup>();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState("");
  const refresh = () =>
    request<Setup>("/api/setup")
      .then((value) => {
        setError("");
        setSetup(value);
        if (!value.complete) setOpen(true);
      })
      .catch((e) => setError(e.message));
  useEffect(() => {
    void refresh();
  }, []);
  if (!setup)
    return (
      <div className="setup-loading">
        {error ? (
          <>
            <p>{error}</p>
            <button onClick={() => void refresh()}>Retry</button>
          </>
        ) : (
          <div className="setup-splash">
            <img src="/brand/watchweaver-splash.png" alt="WatchWeaver" />
            <span>Starting WatchWeaver…</span>
          </div>
        )}
      </div>
    );
  if (!setup.complete)
    return (
      <SetupDialog
        setup={setup}
        required
        onClose={() => void refresh()}
        onChanged={refresh}
      />
    );
  return (
    <>
      <App />
      <button className="configure-button" onClick={() => setOpen(true)}>
        Configure integrations
      </button>
      {open && (
        <SetupDialog
          setup={setup}
          required={!setup.complete}
          onClose={() => {
            setOpen(false);
            void refresh();
          }}
          onChanged={refresh}
        />
      )}{" "}
      {error && <div className="setup-toast">{error}</div>}
    </>
  );
}

function SetupDialog({
  setup,
  required,
  onClose,
  onChanged,
}: {
  setup: Setup;
  required: boolean;
  onClose: () => void;
  onChanged: () => void;
}) {
  const [clientID, setClientID] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [webhook, setWebhook] = useState("");
  const [discordEnabled, setDiscordEnabled] = useState(setup.discord.enabled);
  const [auth, setAuth] = useState<Authorization>({
    status: setup.trakt.authorization_status,
  });
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setMessage("");
    try {
      await fn();
      onChanged();
    } catch (e) {
      setMessage(friendlySetupError((e as Error).message));
    } finally {
      setBusy(false);
    }
  };
  const saveTrakt = () =>
    run(async () => {
      await request("/api/integrations/trakt/config", {
        method: "PUT",
        body: JSON.stringify({
          client_id: clientID,
          client_secret: clientSecret,
        }),
      });
      setClientID("");
      setClientSecret("");
      setAuth(
        await request<Authorization>("/api/integrations/trakt/authorize", {
          method: "POST",
        }),
      );
    });
  const beginAuth = () =>
    run(async () =>
      setAuth(
        await request<Authorization>("/api/integrations/trakt/authorize", {
          method: "POST",
        }),
      ),
    );
  const pollAuth = () =>
    run(async () =>
      setAuth(
        await request<Authorization>("/api/integrations/trakt/authorize/poll", {
          method: "POST",
        }),
      ),
    );
  const saveDiscord = () =>
    run(async () => {
      await request("/api/integrations/discord/config", {
        method: "PUT",
        body: JSON.stringify({ webhook_url: webhook, enabled: discordEnabled }),
      });
      setWebhook("");
      setMessage("Discord settings saved.");
    });
  const testDiscord = () =>
    run(async () => {
      await request("/api/integrations/discord/test", { method: "POST" });
      setMessage("Test announcement sent.");
    });
  const missingClientID =
    !clientID && !setup.trakt.configured && !setup.trakt.client_id_overridden;
  const missingClientSecret =
    !clientSecret &&
    !setup.trakt.configured &&
    !setup.trakt.client_secret_overridden;
  useEffect(() => {
    if (auth.status !== "authorization_pending") return;
    const timer = setInterval(
      () => {
        void request<Authorization>("/api/integrations/trakt/authorize/poll", {
          method: "POST",
        })
          .then((value) => {
            setAuth(value);
            if (value.status === "connected") onChanged();
          })
          .catch((e) => setMessage(e.message));
      },
      Math.max(1, auth.poll_after_seconds ?? 5) * 1000,
    );
    return () => clearInterval(timer);
  }, [auth.status, auth.poll_after_seconds, onChanged]);
  return (
    <div
      className="setup-backdrop"
      role="dialog"
      aria-modal="true"
      aria-label="WatchWeaver setup"
    >
      <div className="setup-dialog">
        <div className="setup-heading">
          <div>
            <span>PRIVATE LAN / VPN ONLY</span>
            <h1>
              {required ? "Set up WatchWeaver" : "Configure integrations"}
            </h1>
            <p>
              Secrets are encrypted with a generated key stored in your
              persistent data volume. Saved values are never returned to this
              browser.
            </p>
          </div>
          {!required && (
            <button onClick={onClose} aria-label="Close">
              ×
            </button>
          )}
        </div>
        {message && <div className="setup-message">{message}</div>}
        <section>
          <h2>1. Trakt</h2>
          <p>Trakt is an optional integration for people with Trakt VIP or an existing valid API application, and is the only automated watch-history source in this release. WatchWeaver itself does not require a subscription.</p>
          <details className="trakt-guide">
            <summary>How to get your Trakt Client ID and Secret</summary>
            <ol>
              <li>Open <a href="https://trakt.tv/oauth/applications" target="_blank" rel="noreferrer">Trakt API applications ↗</a> and create an application.</li>
              <li>Use name <code>WatchWeaver</code>.</li>
              <li>Use website <code>https://github.com/thef4tdaddy/watchweaver</code>.</li>
              <li>Use redirect URI <code>urn:ietf:wg:oauth:2.0:oob</code> <button className="copy-button" onClick={() => void copyText("urn:ietf:wg:oauth:2.0:oob").then(() => setMessage("Redirect URI copied.")).catch((e) => setMessage((e as Error).message))}>Copy</button></li>
              <li>Use description <code>Private self-hosted media tracker</code> and leave JavaScript origins blank.</li>
              <li>Save the application, then copy its Client ID and Client Secret below.</li>
              <li>Choose Save and connect, open Trakt activation, enter the displayed code, and return here.</li>
            </ol>
            <p>Your Client Secret is sensitive. WatchWeaver encrypts it in persistent storage and never returns it to the browser.</p>
          </details>
          <div className="setup-fields">
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
            <button
              disabled={busy || missingClientID || missingClientSecret}
              onClick={() => void saveTrakt()}
            >
              {setup.trakt.configured ? "Save changes" : "Save and connect"}
            </button>
          </div>
          {(setup.trakt.client_id_overridden ||
            setup.trakt.client_secret_overridden) && (
            <div className="setup-locked">
              Environment-managed fields are locked; other credentials remain
              UI-managed.
            </div>
          )}
          {setup.trakt.configured &&
            auth.status !== "authorization_pending" &&
            auth.status !== "connected" && (
              <button disabled={busy} onClick={() => void beginAuth()}>
                Start Trakt authorization
              </button>
            )}
          {auth.status === "authorization_pending" && (
            <div className="device-code">
              <p>
                Open{" "}
                <a
                  href={auth.verification_url}
                  target="_blank"
                  rel="noreferrer"
                >
                  Trakt activation ↗
                </a>{" "}
                and enter:
              </p>
              <strong>{auth.user_code}</strong>
              <button disabled={busy} onClick={() => void pollAuth()}>
                I authorized it
              </button>
            </div>
          )}
          {auth.status === "connected" && (
            <div className="setup-success">✓ Trakt connected</div>
          )}
        </section>
        <section>
          <h2>
            2. Discord <small>optional</small>
          </h2>
          <label className="setup-check">
            <input
              type="checkbox"
              checked={discordEnabled}
              onChange={(e) => setDiscordEnabled(e.target.checked)}
            />{" "}
            Enable outbound announcements
          </label>
          {setup.discord.webhook_overridden ? (
            <div className="setup-locked">
              Webhook is locked by a server environment override.
            </div>
          ) : (
            <label>
              Webhook URL
              <input
                type="password"
                value={webhook}
                onChange={(e) => setWebhook(e.target.value)}
                autoComplete="new-password"
                placeholder={
                  setup.discord.configured
                    ? "Configured — enter to replace"
                    : "https://discord.com/api/webhooks/…"
                }
              />
            </label>
          )}
          <div className="setup-actions">
            <button
              disabled={
                busy ||
                (discordEnabled && !webhook && !setup.discord.configured)
              }
              onClick={() => void saveDiscord()}
            >
              Save Discord
            </button>
            {setup.discord.configured && (
              <button
                className="secondary"
                disabled={busy}
                onClick={() => void testDiscord()}
              >
                Send test
              </button>
            )}
          </div>
        </section>
        <div className="setup-footer">
          <span>No login: restrict access to your trusted LAN or VPN.</span>
          <button
            disabled={required && auth.status !== "connected"}
            onClick={onClose}
          >
            {required ? "Finish setup" : "Done"}
          </button>
        </div>
      </div>
    </div>
  );
}
