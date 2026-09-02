# Deployment and Configuration

## Status

This document defines the self-hosted deployment and configuration contract for WatchWeaver v0.1.

## Deployment model

WatchWeaver v0.1 ships as a single containerized application suitable for a NAS, home server, mini PC, or general Docker host.

The container contains:

- the Go backend
- the compiled React/TypeScript frontend as static assets served by Go
- embedded numbered SQL migrations

SQLite is stored outside the ephemeral container filesystem in a persistent `/data` volume.

Docker Compose is the primary documented installation path, but WatchWeaver must not assume a specific NAS vendor or container-management UI.

## Container image

Official project images should be published to GitHub Container Registry (GHCR).

Initial image path:

```text
ghcr.io/thef4tdaddy/watchweaver
```

Supported release architectures for v0.1:

- `linux/amd64`
- `linux/arm64`

Release tags include an immutable version tag and commit-SHA tag. WatchWeaver publishes two moving channels:

- Git tags such as `v0.1.0-beta.1` update the container tag `beta`.
- Stable Git tags such as `v0.1.0` update the container tag `latest` and the stable minor tag such as `0.1`.

Beta releases never update `latest`. Production deployments should prefer an immutable version tag for predictable upgrades.

## Network behavior

The application serves its web UI and API from one HTTP listener using the same origin.

Default container port:

```text
8080
```

The listen address and externally published host port may be configured without changing application behavior.

v0.1 is supported only on a trusted home/LAN, through a private VPN, or behind an existing authenticated reverse proxy. Network access control is part of the required deployment boundary.

Do not publish the WatchWeaver listener directly to the public internet. Direct public exposure is unsupported because any client that can reach the application can access private viewing data and administrative controls.

Application-native multi-user authentication is outside the v0.1 foundation unless added through a later design issue.

## Persistent volume layout

All durable or user-generated instance data lives beneath `/data`.

Recommended logical layout:

```text
/data/
  watchweaver.db
  backups/
  exports/
```

SQLite auxiliary WAL/SHM files may exist alongside the database as required by SQLite.

Generated Letterboxd CSV files may be produced in `/data/exports` or streamed directly to the user, but any files retained by the application must remain within persistent storage and outside the image filesystem.

Backups belong under `/data/backups` by default, with the option for administrators to copy/mount them elsewhere.

## Configuration categories

WatchWeaver separates bootstrap/deployment configuration from normal application preferences.

### Deployment/bootstrap configuration

Environment variables are used for values that must exist before the application/database/UI is usable or that are best controlled by the container administrator.

Examples include:

- listen address/port
- data directory
- log level
- bootstrap integration credentials/secrets where appropriate
- reverse-proxy/base-URL related settings where needed

For the bootstrap HTTP server, v0.1 defines:

- `WATCHWEAVER_LISTEN_ADDR` with default `:8080`

### UI-managed application settings

Normal runtime preferences should live in the WatchWeaver database and be editable through the web UI where practical.

Examples include:

- Trakt polling interval within supported bounds
- rating/review prompt preferences
- Serializd reminder thresholds
- timezone
- Discord notification enablement/preferences where configuration does not expose secrets
- integration workflow preferences

## Configuration precedence

Avoid exposing the same setting through multiple independent mechanisms whenever possible.

When a setting must exist in more than one layer, precedence is:

1. explicit deployment/environment override
2. persisted UI/database setting
3. built-in default

Environment overrides should be reserved for settings administrators reasonably expect to control at deployment time. The UI should indicate when a value is externally overridden and therefore not editable/effective from the UI.

## Secrets

Secrets are never baked into the image or committed to the repository.

Environment variables, mounted secret files, or Docker secrets may provide bootstrap secrets.

OAuth tokens that must be refreshed and maintained by WatchWeaver may be stored in the local database after authorization, with appropriate care to avoid exposing them through ordinary API responses or logs.

Examples/documentation must use fake values only.

Normal API/UI responses must not return full stored credentials/tokens after configuration. The UI may show status such as `configured`, `connected`, or masked metadata instead.

## Database initialization and migrations

On startup, WatchWeaver opens the SQLite database in `/data` and applies all pending embedded numbered SQL migrations before declaring the application ready.

Migrations are:

- ordered
- transactional where SQLite permits
- idempotently tracked by migration version
- the source of truth for schema evolution

If a migration fails, readiness remains false and the application must fail clearly rather than partially serving against an unknown schema state.

Upgrades must preserve the existing database and apply only the missing migrations.

Downgrading to an older application version is not guaranteed unless explicitly documented for that release. Users should restore a pre-upgrade backup when a schema downgrade is required.

## SQLite operation

Use SQLite WAL mode for normal application operation where supported by the mounted filesystem.

The database path, WAL file, and SHM file must all reside on the same persistent filesystem.

WatchWeaver should avoid recommending raw file-copy backups of a live WAL database as the primary backup method.

## Backups

WatchWeaver should provide a safe application-level backup operation using an SQLite-supported consistent backup mechanism, such as the SQLite backup API or `VACUUM INTO`, rather than requiring users to stop the container and manually copy database/WAL files.

A backup contains at minimum the SQLite database in a consistent state.

Retained generated exports are reproducible and do not need to be part of the database backup contract, though administrators may back up the entire `/data` directory if desired.

Before significant schema upgrades, WatchWeaver should create or strongly support creation of a pre-migration backup.

v0.1 restoration may be documented as an administrative operation: stop WatchWeaver, replace the database with a known-good consistent backup, then restart on a compatible application version.

Create a consistent online backup with the application command:

```bash
docker compose exec watchweaver watchweaver backup
```

The default filename is UTC timestamped beneath `/data/backups`. An explicit destination beneath the persistent volume can be supplied as the second argument. Existing files are never overwritten.

### Restore procedure

1. Stop the service with `docker compose down`.
2. Retain the current database until the backup has been verified.
3. Inside the `watchweaver-data` volume, copy the selected consistent backup to `/data/watchweaver.db` and remove stale `watchweaver.db-wal` and `watchweaver.db-shm` sidecars.
4. Start the same application version that created the backup, or a documented compatible newer version, with `docker compose up -d`.
5. Wait for `/readyz` and verify history and settings in the dashboard.

Do not replace or copy the live database file while WatchWeaver is running. Downgrading an already migrated database is unsupported; restore a pre-upgrade backup instead.

## Health and readiness

Expose lightweight unauthenticated operational endpoints suitable for container health checks:

```text
GET /healthz
GET /readyz
```

`/healthz` answers whether the application process is alive and able to serve HTTP.

`/readyz` answers whether WatchWeaver is ready to handle normal requests. Readiness requires at minimum:

- database opened successfully
- migrations completed successfully
- core application initialization completed

Optional integrations such as Discord, Letterboxd, or Serializd must not make core readiness fail merely because they are disabled or temporarily unavailable.

A temporary Trakt network outage after successful application startup likewise should be surfaced as integration status rather than making the entire container unhealthy.

Health endpoints must not expose secrets, viewing history, ratings, reviews, or other private instance data.

## Graceful shutdown

On SIGTERM/SIGINT, WatchWeaver should:

1. stop accepting/scheduling new background work
2. cancel polling/retry loops
3. allow in-flight database transactions or bounded HTTP requests to finish where practical
4. checkpoint/close SQLite cleanly
5. stop optional integration clients
6. exit within the container runtime's normal termination window

Shutdown must not intentionally drop locally accepted rating/review changes merely because an outbound integration is unavailable; durable pending state should survive restart.

## Logging

Logs go to stdout/stderr for normal Docker collection.

Default logging should include useful operational information such as:

- application version
- startup/shutdown
- migration versions applied
- integration connection/status transitions
- Trakt poll success/failure summaries
- export/sync workflow summaries
- retry/error information

Logs must not include:

- access/refresh tokens
- Discord webhook URLs/tokens
- full OAuth authorization responses
- review text by default
- generated CSV contents
- full viewing-history dumps
- other secrets

Instance-specific media titles/IDs should only be logged when useful for diagnosing a specific operation and should not appear at noisy/default levels unnecessarily.

A configurable log level may expose additional diagnostic detail without changing the no-secrets rule.

## Docker Compose contract

The repository should include a sanitized Compose example that demonstrates:

- official GHCR image
- persistent `/data` volume
- port mapping
- timezone/environment configuration
- restart policy
- optional `.env` usage
- health check

The example must not contain real host paths, usernames, IDs, tokens, or service credentials.

## Timezone

WatchWeaver stores canonical timestamps in UTC but requires a configured display/export timezone for diary dates and user-facing schedules.

The default may come from an explicit WatchWeaver timezone setting or container `TZ` environment value. The effective timezone should be visible in the web UI.

## Upgrades

Normal upgrade flow:

1. create/verify a current backup
2. pull the desired WatchWeaver image version
3. recreate/restart the container using the same `/data` volume
4. WatchWeaver applies pending migrations before readiness
5. verify `/readyz` and integration status

Container replacement must never require re-authorizing or reconfiguring the instance solely because the application filesystem was recreated; all such durable state belongs in `/data` or externally supplied configuration.

## v0.1 acceptance boundary

Deployment/configuration is sufficient for v0.1 when:

1. WatchWeaver can run as one Docker container with one persistent `/data` volume.
2. The Go process serves both API and compiled web UI on the same origin.
3. Sanitized Docker Compose instructions work without assuming a specific NAS vendor.
4. `linux/amd64` and `linux/arm64` images are published to GHCR.
5. Deployment/bootstrap settings and UI-managed preferences have a clear ownership/precedence model.
6. Secrets are externalized or safely persisted and never returned/logged in full.
7. Startup applies numbered SQLite migrations before readiness.
8. `/healthz` and `/readyz` support container health checks without leaking private data.
9. SIGTERM/SIGINT causes bounded graceful shutdown and clean database closure.
10. A safe consistent SQLite backup/restore procedure is documented and implementable.
11. Application logs provide useful operational state without exposing tokens, review text, CSV contents, or bulk history.
12. Recreating/upgrading the container with the same `/data` volume preserves the configured WatchWeaver instance.
