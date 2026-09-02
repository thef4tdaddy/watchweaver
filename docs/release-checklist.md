# v0.1 Release Checklist

Run this checklist from a clean checkout before creating a release tag. It complements the automated unit, integration, race, frontend, and container jobs; it does not introduce new product scope.

## Automated gate

- [ ] `go test -race ./...` passes. These suites cover migration/restart safety, baseline silence, paginated and overlapping Trakt imports, source-event deduplication, distinct rewatches, deterministic binge/weekly-TV prompt rules, one-current-rating behavior, outbound retry durability, Letterboxd timezone/duplicate/regeneration state, Serializd overdue checkpoints, Discord deduplication/backoff, API secret redaction, health/readiness, graceful shutdown, and consistent backup/restore.
- [ ] In `web/`, `npm ci`, `npm run test`, `npm run typecheck`, `npm run build`, and `npm run lint` pass.
- [ ] `scripts/check-release-privacy.sh` passes against tracked files.
- [ ] The Container workflow builds the production image, reaches `/readyz`, creates a live backup, recreates the service with the same volume, restores the backup, and queries the restored API.
- [ ] The release workflow’s build succeeds for `linux/amd64` and `linux/arm64` without publishing from an untrusted branch.

## Reproducible acceptance walk-through

- [ ] Start with a new named volume and fake/test integration endpoints. Confirm `/healthz` is live before or during initialization and `/readyz` succeeds only after migrations.
- [ ] Complete Trakt device authorization. Confirm the initial history and rating baseline creates no old inbox tasks or Discord announcements.
- [ ] Add a new movie watch and a settled full-season or weekly-TV fixture. Confirm each eligible prompt appears once after overlapping polls and remains after restart.
- [ ] Submit an exact half-star rating and review in the web inbox. Force a Trakt failure, restart, then recover it; confirm the local value never disappears and the pending write is eventually acknowledged once.
- [ ] Add a movie rewatch. Confirm both watch events remain in History while only one current rating and review exist.
- [ ] Generate a Letterboxd batch around the configured timezone’s date boundary. Restart before confirmation, download the same generated batch, inspect same-day duplicate warnings, then explicitly mark it imported.
- [ ] Reach both Serializd reminder thresholds independently. Restart while overdue, open the official importer, and confirm the checkpoint changes only after “Mark synced.”
- [ ] Disable Discord, Serializd reminders, and Trakt credentials independently and verify local history, inbox, Letterboxd, settings, and health remain usable. Re-enable a failing Discord endpoint and confirm conservative retry without backlog flooding.
- [ ] Stop the container with SIGTERM and confirm it exits within the Compose grace period. Recreate it with the same `/data` volume and verify authorization, history, ratings, reviews, export state, and checkpoints persist.

## Privacy and release artifacts

- [ ] Confirm the deployment is reachable only from the intended LAN/VPN or through an authenticated reverse proxy; the application port is not directly exposed to the public internet.
- [ ] Review the PR diff and image history. No real credentials, tokens, webhook URLs, user/home/NAS paths, IDs, viewing data, databases, exports, or logs are present.
- [ ] Confirm normal API responses and rendered settings contain only public authorization instructions and integration status—not device codes, access/refresh tokens, client secrets, or webhook URLs.
- [ ] Create a fresh application backup and retain the pre-upgrade image tag. Do not tag the release while a known data-loss, duplicate-event, rating-snapshot, secret-leak, or notification-flood defect remains.
