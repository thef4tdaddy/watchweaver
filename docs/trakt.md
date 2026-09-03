# Trakt Integration

## Status

This document defines Trakt authentication, history ingestion, rating synchronization, and reconciliation behavior for WatchWeaver v0.1.

## Role of Trakt

Trakt is an optional integration for users who have Trakt VIP or an existing valid API application, and is the only automated upstream ingestion integration for v0.1. Jellyfin and other scrobblers remain upstream of Trakt; WatchWeaver does not require Jellyfin credentials.

Trakt is not WatchWeaver's database. Once activity is imported, WatchWeaver owns its local watch events and workflow state.

## Authentication

WatchWeaver uses Trakt's OAuth device authorization flow so a headless NAS/server can be connected without requiring a browser on the host.

The web UI presents the authorization instructions/code and reports connection status.

Client credentials and tokens must remain outside source control. Persisted credentials are treated as secrets and must not be exposed through normal API/UI responses or logs.

### Creating a Trakt API application

Creating a new personal Trakt API application currently requires Trakt VIP. An existing valid application can still be used. This requirement is Trakt's policy; WatchWeaver does not charge for or provide a subscription.

Create an application at [Trakt API applications](https://trakt.tv/oauth/applications) with these values:

- Name: `WatchWeaver`
- Website: `https://github.com/thef4tdaddy/watchweaver`
- Redirect URI: `urn:ietf:wg:oauth:2.0:oob`
- Description: `Private self-hosted media tracker`
- JavaScript origins: leave blank

Save the application, copy its Client ID and Client Secret into WatchWeaver's first-run wizard, and choose **Save and connect**. Open the Trakt activation page, enter the device code shown by WatchWeaver, approve access, and return to WatchWeaver. The Client Secret is write-only after saving.

Trakt remains the supported automatic history source for the current release. A direct Jellyfin integration is planned separately so a future release can offer an alternative path.

## Initial synchronization

On first successful connection, WatchWeaver imports the complete Trakt history available to the account for supported v0.1 media types, plus existing supported ratings.

Historical activity imported during this initial synchronization establishes the local baseline.

The baseline import must not generate historical rating/review prompts. Normal prompt generation begins for activity detected after the initial synchronization boundary.

This provides a complete local history without flooding a newly installed instance with years of old tasks.

## History ingestion

WatchWeaver imports movie and episode watch-history events.

For each event it preserves:

- the Trakt history/event ID when available
- media identity and external IDs
- the original watched timestamp
- normalized UTC time
- enough source metadata to reconcile the event later

Every distinct Trakt history event becomes a distinct WatchWeaver watch event. Rewatches are never collapsed into a single watched flag.

## Incremental polling

The default polling interval is five minutes.

Polling should be configurable without changing the fundamental synchronization model.

Incremental synchronization uses durable local checkpoints plus a small overlapping look-back window. The overlap is intentional: idempotent event ingestion is preferred over risking a missed event at a checkpoint boundary.

Trakt history IDs provide the preferred deduplication key. Reprocessing an already-imported history event must not create another local event.

A process restart must therefore be safe at any point in the polling/import cycle.

## Ratings

During initial synchronization, WatchWeaver imports existing Trakt ratings for supported rating targets.

WatchWeaver stores one current rating per movie, season, or episode using its canonical integer 1–10 representation.

When a user submits or changes a supported rating in WatchWeaver, WatchWeaver writes the corresponding rating to Trakt.

When a rating is changed directly in Trakt and later observed by WatchWeaver, the newer rating wins. Reconciliation therefore follows a last-modified-wins model using the best available source modification timestamps.

WatchWeaver should retain synchronization metadata so it can distinguish acknowledged outbound changes from genuinely newer remote changes.

## Failure and retry behavior

Temporary Trakt failures must not prevent WatchWeaver from operating on already-imported local data.

Inbound synchronization failures:

- do not advance the durable checkpoint past unprocessed data
- are retried later
- do not duplicate already-imported events

Outbound rating failures:

- retain the local rating
- record pending/failed synchronization state
- retry later using bounded backoff

WatchWeaver must respect Trakt rate-limit information and retry guidance. It must not aggressively retry requests during an outage or rate-limit condition.

## History reconciliation and deletion

A Trakt history event disappearing upstream must not cause WatchWeaver to silently destroy the corresponding local history.

When practical, reconciliation may mark an imported event as removed/deleted upstream while retaining the local record and provenance.

Destructive local deletion requires an explicit local-data action rather than merely observing that remote history changed.

## Temporary disconnection

If Trakt becomes unavailable or authorization expires:

- existing local history remains available
- existing WatchWeaver workflow state remains available
- pending outbound work remains durable
- synchronization resumes after connectivity/authentication is restored

Disconnecting Trakt does not erase local WatchWeaver data.

## First-run boundary

The initial synchronization must establish a durable completion/baseline boundary before normal new-activity prompt generation begins.

If first-run synchronization is interrupted, WatchWeaver resumes it idempotently rather than treating partially imported historical events as newly watched activity.

## v0.1 acceptance boundary

The Trakt integration is sufficient for the first functional milestone when it can:

1. Authenticate a self-hosted instance using device authorization.
2. Import complete existing movie/episode history without generating historical prompt spam.
3. Import existing supported ratings.
4. Poll for new history on an approximately five-minute default cadence.
5. Persist each new Trakt history event exactly once.
6. Preserve rewatches and original watched timestamps.
7. Make newly imported events available to the rating rules engine.
8. Write WatchWeaver rating changes to Trakt.
9. Import genuinely newer Trakt rating changes using last-modified-wins reconciliation.
10. Recover safely from process restarts, temporary Trakt outages, and rate limiting.
