# WatchWeaver bot API v1

The optional bot lives in `thef4tdaddy/WatchWeaver-DiscordBot`, in a separate
container. This document describes the WatchWeaver side. No bot is embedded or
deployed by this change. Tracking: #11, #150, #151, #152, #153.

## Configuration and access

By default no bot listener or worker runs. Set:

- `WATCHWEAVER_BOT_LISTEN_ADDR`: dedicated interface address and port.
- `WATCHWEAVER_BOT_TOKEN_FILE`: mounted file containing a random service token
  of at least 32 characters. The file is read per request; replacing it revokes
  the old token immediately. Missing/unreadable/short tokens fail closed.
- `WATCHWEAVER_BOT_USER_IDS`: comma-separated Discord user snowflakes authorized
  to act as this instance's owner. Changes require restarting WW.
- `WATCHWEAVER_PUBLIC_URL`: HTTP(S) origin users can open in their browser. No
  credentials, query, fragment, or path prefix. This differs from the internal
  API address.
- `WATCHWEAVER_BOT_NOTIFICATIONS=true`: explicitly transfer task notification
  ownership to the bot worker. Existing Serializd webhook reminders remain enabled
  when a webhook is configured. Defaults to false. Do not enable before the bot
  consumer is available. Webhook-only installations remain the default.

Each request must contain `Authorization: Bearer <service-token>` and
`X-Discord-User-ID: <authorized-user>`. WW trusts the authenticated bot to assert
its verified Discord actor; the bot must validate the Discord interaction itself.
Never put tokens in URLs or logs. Provision secrets outside version control.

`compose.bot-api.yaml` provides a WW-side override with separate web and bot
interfaces. Inspect subnet availability before use. The web listener binds only
to the web network address; the bot listener only to the bot network address.
The bot must join only `watchweaver-bot-api` plus its own outbound-egress network,
never the WW web network. Publish no bot API host port. Do not give the bot host
networking, host gateway access, privileged mode, Docker socket, or WW data.
If the host publishes the web API to a network reachable by the bot, restrict
that host endpoint with firewall rules or an authenticated reverse proxy as well.
A second port alone is not an authorization boundary. Validate actual network
reachability in the target Docker/Portainer environment before enabling writes.
The override is not a deployment action and does not install the bot.

## Reads

All routes below are under `/api/bot/v1`:

- `GET /capabilities`: protocol version, currently supported features, public links.
- `GET /inbox`: `page`, `per_page`, `q`, `type`, `task_id`.
- `GET /history`: `page`, `per_page`, `q`, `type`.
- `GET /media`: paginated `q`/`type` search, including show/season/episode context.
- `GET /media/{id}` and `/tasks/{id}`: media, current rating/review when present,
  resource `revision`, and `media_revision` for task completion.
- `GET /status`, `/integrations`, `/letterboxd`, `/serializd`: status only.

No settings, configuration, authorization, backup, diagnostics, export file,
transfer, Letterboxd confirmation, or Serializd mark-synced route is mounted.
Unknown routes return JSON 404. Errors are `{ "error": "..." }`.

## Mutations

`POST /actions` requires `Idempotency-Key` (the Discord interaction ID, maximum
128 characters). Body:

```json
{"target":"task","id":42,"action":"complete","rating":8,"review":"Enjoyed it.","revision":1,"media_revision":3}
```

- Task actions: `complete`, `skip`, `snooze` (future RFC3339 `until`).
- Media actions: `rating` (integer 1–10), `review` (nonempty text, maximum 100000
  UTF-8 bytes), `delete-rating`, `delete-review`, `ignore`, `unignore`.
- Ignore/unignore applies to movies and shows; ignoring a show suppresses future
  descendant prompts and resolves current prompts. Unignore permits future prompts;
  it does not resurrect already ignored tasks.
- All bot mutations require the observed `revision`; task completion also requires
  `media_revision`. Reload after HTTP 409. Never retry with guessed versions.
- The bot must confirm deletion with the actor before submitting it.
- Result and change commit in one transaction. The same actor/key/payload replays
  the original success after restart; changing the payload under that key conflicts.
- Web rating/review and task changes use the same workflow service. Upstream
  changes increment resource revisions through database triggers too.

`POST /sync/trakt` with `Idempotency-Key` returns HTTP 202 and a durable job ID.
`GET /jobs/{id}` returns its state to the originating actor. The background worker
uses the existing serialized Trakt sync manager. Interrupted jobs resume after
restart; sync remains reconciling/idempotent, not exactly-once remote execution.

## Prompt delivery

When enabled, WW establishes a task-ID baseline and does not announce existing
history. New pending prompts less than one day old enter the durable outbox;
changes to previously delivered tasks enqueue card updates.

- `POST /notifications/claim`: at most ten jobs per minute with a random two-minute lease,
  task ID, revision, path and any previously delivered message ID.
- Fetch current task details before sending/updating a card. Recheck state;
  a claim is not a guarantee the task remains actionable.
- `POST /notifications/{id}/ack` with `lease` and `message_id` commits delivery.
  Acknowledgments are retryable while the lease remains owned. Expired leases
  return 409; failed sends can be retried after lease expiry.
- Old/obsolete jobs expire; newer revisions replace pending older versions.
- Delivery is at-least-once: a crash after Discord accepts a message but before
  acknowledgment can duplicate it. Reconcile using Discord message references
  where possible; the bot must respect rate limits and digest large batches.

The current outbox carries task cards and recommends digest presentation for batches of five or more. Letterboxd/Serializd pending summaries
remain available through read endpoints and exact workflow links; destination
workflow actions are never available through Discord.

## Deep links

`/inbox?type=movie&q=title`, `/history?page=2&q=title`, `/media/{id}`, `/tasks/{id}`,
`/letterboxd`, `/serializd`, `/status`, `/settings/trakt`, `/settings/jellyfin`,
`/settings/discord`, `/settings/preferences`. Existing `/movies` and `/tv` aliases
remain supported. Direct refresh uses the existing SPA fallback. Completed task
links remain readable and missing resources provide an inbox recovery link.

## Release verification

Run backend tests (including bot authorization, forbidden routes, stale writes,
replays, outbox leases), frontend tests, typecheck, lint and production build.
Before release also test the Docker network boundary and published images with the
separate bot. This branch alone does not establish end-to-end Discord delivery.
