# WatchWeaver Data Model

## Status

This document defines the initial local data-model and migration strategy for WatchWeaver v0.1.

## Design principles

WatchWeaver uses a normalized relational model in SQLite. Core media entities are stored separately from watch events and integration-specific state.

The schema must preserve raw history while allowing workflow state to evolve independently.

## Core entities

WatchWeaver should model the following durable concepts:

- media items
  - movie
  - show
  - season
  - episode
- external identifiers
- watch events
- ratings
- reviews
- prompt/workflow tasks
- integration state
- application settings

## Media identity

Each media entity receives a WatchWeaver-owned internal identifier. External IDs are stored as mappings rather than primary keys.

Examples include:

- Trakt IDs
- TMDB IDs
- IMDb IDs
- future provider IDs

This avoids spreading service-specific columns across the core domain model.

## Watch events

Each viewing is stored as its own watch event.

A rewatch creates another event rather than updating or replacing the previous one.

Each event should preserve:

- internal event ID
- target media ID
- source integration
- upstream source/history event ID when available
- original watched timestamp
- normalized UTC timestamp
- import timestamp
- any reconciliation/deletion state needed later

### Deduplication

The strongest available upstream event identifier should be used for uniqueness. For Trakt, the Trakt history event ID should be used when available.

WatchWeaver must not deduplicate solely on `media + watched_at`, because distinct valid events must remain possible.

## Ratings

WatchWeaver stores one current rating per rating target.

Valid rating targets for v0.1 are:

- movie
- season
- episode

A rating is not attached to a specific watch event. Rewatches may generate a new prompt, but submitting a new rating updates the current rating for that media target.

Ratings are stored canonically as integer values from 1 through 10, corresponding to a 0.5–5 star user-facing scale.

Rating records should preserve source and synchronization metadata needed for Trakt reconciliation.

## Reviews

Reviews are first-class local data. They attach to the same logical media targets used by the rating/review workflow rather than to a destination service.

Destination synchronization/export state must be stored separately from review text.

## Prompt/workflow state

Prompt state must be modeled independently from ratings and watch events.

The schema should be able to represent states such as:

- pending
- completed
- snoozed
- skipped
- ignored

Exact prompt semantics are defined in the rating/review rules specification.

## Integration state

Integration-specific workflow state should be modeled generically rather than by adding columns such as `letterboxd_exported` or `serializd_synced` to core media tables.

Integration state must be able to support concepts such as:

- notification delivery state
- Letterboxd export state per movie watch/diary event
- Serializd synchronization checkpoints
- Trakt polling/checkpoint state
- future integrations

## Timestamps and timezone handling

All normalized timestamps should be stored as UTC instants.

When an upstream service provides an original timestamp, WatchWeaver should preserve that original value or enough information to reproduce it faithfully.

Timezone conversion is a presentation concern except where a destination format requires a local calendar date, such as diary-date generation.

## Migration strategy

The SQLite schema is managed through explicit numbered SQL migration files committed to the repository and embedded in the Go application.

Example:

```text
migrations/
  000001_initial.up.sql
  000002_add_feature.up.sql
```

The SQL migration files are the source of truth. WatchWeaver should not rely on ORM-generated schema migrations.

Migrations must run deterministically at startup before normal application work begins.

## SQLite durability

The database lives in the persistent WatchWeaver data directory rather than the container filesystem.

Backup and restore procedures must account for SQLite WAL mode and must not instruct users to blindly copy a live database file without a safe checkpoint/backup mechanism.

Detailed operational backup behavior is defined in the deployment specification.

## Reset and deletion behavior

Disconnecting or resetting an integration must not destroy local watch history, ratings, or reviews.

An integration reset may remove or reset:

- authentication state
- polling checkpoints
- destination-specific synchronization state
- destination-specific notification/export bookkeeping

A complete local-data deletion is a separate, explicit destructive operation.

## Initial schema shape

The concrete implementation may use table names similar to:

```text
media_items
external_ids
watch_events
ratings
reviews
prompt_tasks
integration_state
app_settings
```

The implementation may normalize seasons and episodes further where useful, but must preserve the relationships and ownership rules described here.

## Acceptance boundary

The v0.1 schema is sufficient when it can support all of the following without redesign:

1. Import Trakt movie and episode history.
2. Preserve multiple watches of the same media item.
3. Map Trakt/TMDB/IMDb identifiers to WatchWeaver media entities.
4. Store one current rating per movie, season, or episode.
5. Store local review text.
6. Track prompt workflow state independently from ratings.
7. Track Discord notification state.
8. Track Letterboxd export state per movie watch event.
9. Track Serializd synchronization checkpoints/counters.
10. Run forward schema migrations safely on startup.
