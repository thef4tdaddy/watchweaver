# WatchWeaver Architecture

## Status

This document defines the initial architecture for WatchWeaver v0.1. Detailed behavior for ratings, persistence, Trakt reconciliation, Discord interactions, Letterboxd exports, Serializd reminders, and deployment is specified separately.

## Product shape

WatchWeaver is a self-hosted application that receives watch activity from Trakt, persists its own workflow state, and routes that activity into rating, review, export, and synchronization workflows for other services.

### High-level flow

```text
Jellyfin / other scrobblers
          |
          v
        Trakt
          |
          v
    WatchWeaver
      |   |   |
      |   |   +--> Discord
      |   +------> Letterboxd CSV
      +----------> Serializd sync workflow
```

Jellyfin and other media servers are upstream of Trakt. WatchWeaver does not require direct Jellyfin credentials for v0.1.

## Application architecture

WatchWeaver v0.1 is one deployable application:

- Go backend
- SQLite database
- React + TypeScript frontend built with Vite
- frontend assets served by the Go process
- Discord bot hosted inside the same Go application when enabled
- Docker as the primary deployment method

The v0.1 target is one container and one long-running Go process. This keeps installation and operation simple for NAS and home-server environments while still allowing internal packages to remain cleanly separated.

## Internal boundaries

External services must be isolated behind integration-specific packages/adapters. The rest of the application should operate on WatchWeaver domain concepts rather than service-specific transport details.

Initial package boundaries are expected to include concepts equivalent to:

```text
internal/
  trakt/
  discord/
  letterboxd/
  serializd/
  media/
  ratings/
  rules/
  database/
```

These names are organizational guidance, not a requirement that every package exist immediately.

## Data ownership

Trakt is an upstream integration, not WatchWeaver's database.

### Trakt owns

- the external watch activity received from Jellyfin and other scrobblers
- Trakt ratings
- Trakt-specific account and media identifiers

### WatchWeaver owns

- imported watch-event records
- local media mappings
- prompt state
- locally stored ratings and reviews
- snooze, skip, and ignore state
- Discord notification state
- Letterboxd export state
- Serializd synchronization state
- application configuration
- decisions produced by the rating/review rules engine

Once a watch event has been imported successfully, WatchWeaver must retain enough local state to continue processing that event during temporary Trakt outages.

## Media identity

WatchWeaver uses its own internal identifiers for domain entities. Third-party identifiers are mappings, not database primary keys.

A media entity may have mappings such as:

- Trakt ID
- TMDB ID
- IMDb ID
- additional provider IDs in the future

TMDB is the preferred cross-service identity where available because it is useful for interchange with services such as Letterboxd and for TV metadata. Trakt IDs remain authoritative when communicating with Trakt.

The detailed normalized schema and uniqueness constraints are defined in the data-model specification.

## Watch events

A watch is modeled as an event, not merely a `watched = true` property on a media item.

Multiple watches of the same movie or episode must remain distinct and retain their original timestamps. This supports:

- rewatches
- diary-style Letterboxd exports
- accurate historical activity
- corrections and reconciliation
- TV completion calculations

Derived state such as `season completed` is computed from the underlying watch events rather than replacing them.

## Ratings

WatchWeaver presents ratings to users as a five-star scale with half-star increments:

```text
0.5, 1.0, 1.5, 2.0, 2.5, 3.0, 3.5, 4.0, 4.5, 5.0
```

Internally, ratings are stored canonically as integers from 1 through 10.

This provides exact, lossless mapping between the WatchWeaver UI and integrations that use a 1-10 scale:

```text
WatchWeaver UI: 3.5 / 5
Internal value: 7
Trakt:          7 / 10
Letterboxd:     3.5 / 5
```

WatchWeaver stores rating state locally even when a supported rating is also synchronized to Trakt. Detailed reconciliation and conflict rules belong to the Trakt integration specification.

## Reviews

Reviews are first-class WatchWeaver data and are not owned by a destination service.

A locally stored review may later be exported or surfaced through one or more integrations. Destination-specific synchronization state must remain separate from the review text itself.

## Web UI and Discord

### Web UI

The web interface is the complete administrative and workflow surface for WatchWeaver. It should eventually support configuration, history, corrections, pending work, exports, synchronization status, and integration health.

### Discord

Discord is an optional convenience interface for notifications and lightweight interactions such as rating, reviewing, snoozing, skipping, status checks, and synchronization reminders.

WatchWeaver must remain fully operable without Discord enabled. Discord must not be required for administration or recovery.

## Optional integrations

Integrations should be independently optional where practical.

For v0.1, Trakt is the required ingestion integration. Discord, Letterboxd, and Serializd workflows may be enabled independently.

Examples of valid deployments should eventually include:

```text
Trakt + WatchWeaver + Letterboxd
Trakt + WatchWeaver + Discord
Trakt + WatchWeaver + Discord + Letterboxd + Serializd
```

## v0.1 acceptance boundary

The architecture is sufficient for v0.1 when WatchWeaver can support this end-to-end path:

1. Run WatchWeaver through its supported self-hosted deployment.
2. Connect a Trakt account.
3. Detect a new watch that has reached Trakt.
4. Persist that watch locally exactly once.
5. Preserve the original watch timestamp and media identity mappings.
6. Evaluate whether the activity creates a rating/review task.
7. Accept a rating through a supported WatchWeaver interface.
8. Store that rating locally.
9. Synchronize supported ratings to Trakt.
10. Make eligible movie activity available to the Letterboxd export workflow.
11. Make eligible TV activity contribute to Serializd synchronization status.
12. Expose important state through the web UI.

Discord is part of the intended v0.1 feature set but is not required for the core application to function.

## Deferred specifications

This architecture intentionally does not define the following details:

- exact movie, season, and episode prompting rules
- SQLite tables, indexes, and migration tooling
- Trakt OAuth, polling, pagination, and reconciliation rules
- Discord buttons, modals, commands, and notification wording
- Letterboxd export batching and confirmation semantics
- Serializd reminder thresholds and checkpoint semantics
- configuration precedence, secret storage, and deployment details

Those concerns are specified in their dedicated design work before implementation tickets are created.
