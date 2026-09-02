# WatchWeaver JSON API

The v0.1 API is served under `/api` on the same origin as the web application.
Errors use `{"error":"message"}`. Unknown API routes return JSON `404`
responses and never fall through to the SPA.

## Workflow

- `GET /api/inbox?page=1&per_page=50` lists pending and snoozed tasks.
- `POST /api/tasks/{id}/complete` accepts `rating`, `review`, or both and saves
  the supplied values in the same transaction that completes the task.
- `POST /api/tasks/{id}/skip` skips an unresolved task.
- `POST /api/tasks/{id}/snooze` accepts `{"until":"<future RFC3339 time>"}`.
- `GET /api/history?page=1&per_page=50` lists distinct watch events newest first.

Pagination is ordered deterministically and `per_page` may be 1 through 100.

## Ratings and reviews

- `GET|PUT|DELETE /api/media/{id}/rating`
- `GET|PUT|DELETE /api/media/{id}/review`

A rating `PUT` accepts exactly one of `{"rating":7}` or `{"stars":3.5}`.
Canonical ratings are integers from 1 through 10. Stars must be an exact
half-star increment from 0.5 through 5.0 and are converted losslessly by
multiplying by two. Arbitrary floating-point values are rejected.

A review `PUT` accepts `{"body":"..."}`. Ratings and reviews are current-only;
updating either replaces its current value without changing watch history.

## Configuration and integrations

- `GET|PUT /api/settings` reads or replaces the v0.1 preferences: IANA
  `timezone`, `serializd_enabled`, `serializd_reminder_changes`, and
  `serializd_reminder_days`. Defaults are UTC, disabled, 20 changes, and 14 days.
- `GET /api/integrations` returns public authorization and history-poll status
  only. Credentials, access tokens, refresh tokens, and device codes are never
  returned.
- `POST /api/integrations/trakt/authorize` starts Trakt device authorization.
- `POST /api/integrations/trakt/authorize/poll` advances pending authorization.

The public Trakt authorization response includes only its state and, while
pending, the user code and verification URL needed by the user.

## Letterboxd exports

- `GET /api/letterboxd` returns pending row/event and duplicate-warning counts.
- `GET /api/letterboxd/batches` lists generated and confirmed batches.
- `POST /api/letterboxd/batches` generates a logical batch in one or more CSV files.
- `GET /api/letterboxd/batches/{id}` returns batch state, warnings, and file metadata.
- `GET /api/letterboxd/batches/{id}/files/{part}` downloads a CSV part.
- `POST /api/letterboxd/batches/{id}/confirm` records explicit user confirmation.

Generating or downloading CSV never confirms an import. Generated batches stay
regeneratable until the user confirms one. CSV dates use the configured timezone,
and each file remains below Letterboxd's 1 MB limit.

## Serializd reminders

- `GET /api/serializd` returns the confirmed checkpoint, pending transferable
  episode activity, due reasons, unsupported/manual TV data, and official importer link.
- `POST /api/serializd/mark-synced` is the only action that advances the checkpoint.

Due state uses the configured change-count OR elapsed-days thresholds and remains
false when Serializd is disabled or when no transferable changes are pending.

## Discord announcements

Discord is an optional, outbound-only integration configured with the
`DISCORD_WEBHOOK_URL` environment variable. Integration status exposes only a
configured/disabled boolean state; the webhook URL is never returned. Delivery
state and conservative retry metadata are persisted for prompt summaries and
Serializd due-transition announcements. Discord provides no interactive controls.
