# Serializd Integration

## Status

This document defines the supported Serializd workflow for WatchWeaver v0.1.

## v0.1 boundary

WatchWeaver does not reverse-engineer or depend on an undocumented Serializd write API.

The supported v0.1 workflow uses Serializd's official Trakt importer as the synchronization mechanism and WatchWeaver as the reminder/checkpoint layer around that manual import.

## What Serializd currently imports from Trakt

Serializd's official Trakt importer documents support for:

- watched shows
- episode history
- show ratings
- episode ratings
- watchlist

It explicitly does not import text reviews or custom lists.

Season ratings are not listed among imported data. WatchWeaver therefore must not claim that its season ratings are synchronized to Serializd in v0.1.

## Source of truth and data flow

Trakt remains the authoritative TV history/rating bridge used by the Serializd importer.

Conceptually:

```text
Jellyfin / scrobblers
        -> Trakt
        -> WatchWeaver local state
        -> Serializd Trakt importer (manual user action)
```

WatchWeaver tracks the amount of relevant TV activity that has accumulated since the user last confirmed a Serializd import.

## Sync checkpoint

WatchWeaver stores a durable Serializd confirmation checkpoint.

At minimum this includes:

- timestamp of the last user-confirmed Serializd import
- high-water mark / equivalent durable local reference needed to count later relevant activity
- reminder state so the same overdue condition does not spam notifications

The checkpoint advances only when the user explicitly chooses `Mark synced` after completing the Serializd Trakt import.

Opening the Serializd page or viewing the reminder does not advance the checkpoint.

## Relevant activity

The pending Serializd change count should include TV changes that the current official Trakt importer can reasonably transfer, such as:

- newly watched episodes
- new or changed episode ratings
- new or changed show ratings, if WatchWeaver supports them

Completed-season events may be displayed as useful summary information but should not be double-counted in addition to the episode watches that caused them.

Season ratings, reviews, and other data the importer does not document as supported are excluded from the transferable-change count and should be surfaced separately as unsupported/manual state where useful.

## Default reminder thresholds

WatchWeaver v0.1 uses configurable defaults of:

- 20 transferable TV changes since the last confirmed import, OR
- 14 days since the last confirmed import when at least one transferable change is pending

The thresholds use OR semantics.

No reminder is due merely because 14 days have elapsed if there has been no transferable TV activity since the last confirmation.

These are product defaults, not hard-coded architectural constants, and should be configurable in the web UI.

## Reminder state

WatchWeaver can answer at any time:

- last confirmed Serializd sync time
- number of transferable changes since that checkpoint
- age of the oldest/newest pending activity as useful
- whether the configured activity threshold is reached
- whether the configured elapsed-time threshold is reached
- whether synchronization is currently due
- whether unsupported local TV data exists that the Trakt importer will not carry over

## Web workflow

The intended v0.1 flow is:

1. WatchWeaver accumulates relevant TV activity.
2. The dashboard shows pending Serializd activity and due status.
3. When due, WatchWeaver offers a link to Serializd's official Trakt import page.
4. The user runs the import on Serializd.
5. The user returns to WatchWeaver and chooses `Mark synced`.
6. WatchWeaver records the new confirmation checkpoint and resets the pending reminder state.

WatchWeaver must never infer successful Serializd synchronization merely from opening a link or elapsed time.

## Discord behavior

If Discord announcements are enabled, WatchWeaver may send one sync-due notification when the Serializd workflow transitions from not-due to due.

The v0.1 Discord notification is informational only and directs the user back to the WatchWeaver web interface / Serializd import page.

It must not notify on every watched episode and should not repeatedly announce the same unchanged overdue state.

After a user confirms synchronization, a future reminder requires newly accumulated qualifying activity to become due again.

## Import semantics and limitations

Serializd documents that its Trakt importer adds missing data without overwriting existing Serializd data. Existing watched/rated data may therefore be skipped by Serializd rather than replaced.

WatchWeaver should present its reminder/checkpoint as "time to run the Serializd importer" rather than promising exact two-way synchronization.

WatchWeaver v0.1 cannot automatically verify which individual records Serializd accepted.

## Reviews and season ratings

Reviews remain stored locally in WatchWeaver and may require manual entry into Serializd in v0.1.

Season ratings likewise must not be marked Serializd-synchronized through the Trakt importer unless Serializd later documents support for them.

Future roadmap work may investigate supported Serializd APIs/import mechanisms or other safe workflows.

## Disabling the integration

Serializd is independently optional.

If disabled:

- no Serializd due calculation needs to be surfaced to the user
- no Discord Serializd reminders are sent
- existing local history/ratings/reviews remain untouched
- the stored checkpoint may be retained so re-enabling can resume intelligently

## v0.1 acceptance boundary

The Serializd integration is sufficient for v0.1 when WatchWeaver can:

1. Store a durable user-confirmed Serializd sync checkpoint.
2. Count transferable TV activity since that checkpoint without double-counting season-completion summaries.
3. Treat new/changed supported ratings as pending transferable changes where applicable.
4. Determine due state using configurable defaults of 20 changes OR 14 days with at least one pending change.
5. Link the user to Serializd's official Trakt importer.
6. Provide an explicit `Mark synced` action.
7. Send at most one Discord announcement per unchanged due state when Discord is enabled.
8. Clearly distinguish unsupported data such as reviews and season ratings from data the importer documents as transferable.
9. Never claim automatic verification of successful Serializd import.
10. Never require undocumented/private Serializd APIs.
