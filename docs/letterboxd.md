# Letterboxd Integration

## Status

This document defines the supported Letterboxd workflow for WatchWeaver v0.1.

## v0.1 boundary

WatchWeaver does not depend on Letterboxd API access and does not reverse-engineer private endpoints.

The supported v0.1 integration is generation of Letterboxd-compatible CSV files that the user imports manually through Letterboxd's official importer.

## Source data

Letterboxd export eligibility is derived from WatchWeaver's local movie watch events, movie identity mappings, current movie rating, and current movie review.

TV activity is not exported through this workflow.

## Film matching

Preferred matching order:

1. TMDB movie ID
2. IMDb ID
3. Title + year fallback

TMDB is preferred because Letterboxd officially supports `tmdbID` and uses TMDB as its underlying film metadata source.

## Diary rows

Each representable movie watch event becomes a Letterboxd diary row with a `WatchedDate` derived from the watch timestamp after conversion into the user's configured timezone.

Letterboxd accepts calendar dates rather than timestamps, so time-of-day is not exported.

The `Rewatch` value is true when a prior chronological WatchWeaver watch event exists for the same movie.

## Same-day rewatch limitation

Letterboxd combines multiple rows for the same film and the same `WatchedDate` into one diary entry.

WatchWeaver must therefore detect when multiple local watch events for the same movie map to the same calendar date and surface that as an export limitation.

The CSV may include only one representable row for that film/date combination rather than pretending separate same-day watches can be preserved automatically.

The underlying WatchWeaver history remains unchanged.

## Ratings

WatchWeaver stores one current rating per movie.

For a full-history export, the current movie rating is attached only to the most recent representable diary row for that movie rather than duplicated across every historical watch row.

WatchWeaver exports ratings on Letterboxd's 0.5–5 scale using the exact canonical mapping from the internal 1–10 rating.

If a movie has a current rating but no representable watched date, WatchWeaver may emit a rating-only row for the film.

## Reviews

WatchWeaver stores review text as first-class local data.

For Letterboxd, the current movie review is attached to the most recent representable diary row for that movie so it does not get duplicated across historical diary entries.

If a current review exists without a representable watch date, WatchWeaver may emit a review-only film row.

## Changes after an import

Letterboxd's importer updates an existing diary entry when the same film is imported again with the same `WatchedDate`.

Therefore, if WatchWeaver's current movie rating or review changes after a previously confirmed Letterboxd import, WatchWeaver marks the most recent representable diary row for that movie as needing re-export.

Only the relevant latest diary entry needs the current rating/review update; old historical diary rows are not rewritten merely because the one current movie rating changed.

## Export lifecycle

WatchWeaver distinguishes file generation from confirmed import.

At minimum, export state supports:

- `pending`: eligible local activity/change not yet included in a generated batch
- `generated`: included in a CSV batch but not yet confirmed imported by the user
- `confirmed`: user has explicitly marked the batch as imported into Letterboxd

Generating a file does not automatically mark its rows confirmed.

If a generated batch is never confirmed, its activity remains eligible for regeneration so a lost/downloaded-but-not-imported file does not silently lose synchronization state.

## Initial export and later exports

The first Letterboxd export may include all representable local movie history.

Subsequent exports are delta-oriented and include:

- new movie watch events not yet confirmed imported
- new/changed current ratings needing export
- new/changed current reviews needing export
- previously generated but unconfirmed activity as appropriate

## File format

Generated files must be UTF-8 comma-delimited CSV compatible with Letterboxd's documented import format.

WatchWeaver should prefer these columns where applicable:

- `tmdbID`
- `imdbID`
- `Title`
- `Year`
- `Rating`
- `WatchedDate`
- `Rewatch`
- `Review`

Only one rating column should be emitted to avoid precedence ambiguity.

CSV escaping must follow Letterboxd's documented importer expectations.

## File-size limit

Letterboxd limits import files to 1 MB.

WatchWeaver automatically chunks exports into multiple files below that limit, repeating the header row in each part.

A logical export batch may therefore consist of multiple CSV files but should be confirmed as one user-visible batch unless partial-import tracking is explicitly implemented later.

## Manual confirmation workflow

The intended v0.1 flow is:

1. WatchWeaver shows pending Letterboxd activity.
2. User generates/downloads one or more CSV files.
3. User imports them using Letterboxd's official import page and verifies the preview.
4. User returns to WatchWeaver and marks the batch imported.
5. WatchWeaver records the confirmation checkpoint/state.

WatchWeaver must not claim a Letterboxd import succeeded merely because a CSV file was generated.

## Deletions and corrections

Because v0.1 has no supported Letterboxd write API, WatchWeaver cannot reliably remove an already-imported Letterboxd diary entry.

Local corrections should be reflected in future export state where the official importer can safely update an existing diary entry, such as rating/review changes for the same film/date.

Cases requiring deletion or separation of same-day duplicate diary entries must be surfaced as manual Letterboxd actions rather than hidden.

## v0.1 acceptance boundary

The Letterboxd integration is sufficient for v0.1 when it can:

1. Identify eligible local movie watch events.
2. Convert watch timestamps into configured-local `WatchedDate` values.
3. Mark rewatches from chronological local history.
4. Prefer TMDB IDs and fall back safely when needed.
5. Export exact half-star ratings from WatchWeaver's canonical 1–10 rating.
6. Attach the one current rating/review only to the most recent relevant diary row.
7. Detect and surface same-film/same-date history that Letterboxd cannot represent distinctly.
8. Chunk CSV batches below Letterboxd's 1 MB file limit.
9. Track pending, generated, and user-confirmed import state.
10. Re-export the relevant latest diary row when the current rating or review changes after confirmation.
11. Never require private Letterboxd APIs or credentials for v0.1.
