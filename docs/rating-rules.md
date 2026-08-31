# Rating and Review Rules

## Status

This document defines deterministic rating/review task eligibility for WatchWeaver v0.1.

## Metadata boundary

Trakt is the only required TV release/progress metadata source for v0.1.

WatchWeaver should not require TMDB, TVmaze, or another secondary TV metadata API for the initial release. Additional metadata providers may be added later if they materially improve release-state accuracy or features.

When Trakt metadata is insufficient or uncertain, WatchWeaver should prefer not to create a questionable prompt and should reevaluate when metadata changes.

## General principles

- Watch events and rating tasks are separate concepts.
- WatchWeaver stores one current rating per movie, season, or episode.
- A new watch or rewatch may create a new prompt even when the target already has a current rating.
- Submitting a rating updates the current rating rather than creating a permanent rating per watch event.
- Reviews are optional additions to rating interactions, not separate obligations.
- Historical events imported during the initial Trakt baseline synchronization do not generate prompts.

## Movies

Every newly detected completed movie watch after the initial synchronization boundary is eligible for a movie rating task.

This includes rewatches.

If the movie already has a rating, the prompt should expose that current rating and allow the user to keep or change it rather than presenting the movie as unrated.

## Television: season completion

For backlog, completed, or binge viewing, WatchWeaver does not prompt after every episode.

A season becomes eligible for a season rating task when all normal episodes belonging to the season are released and have been watched.

Season completion is derived from episode inventory and watch state, not from a broad show-status flag or a finale marker.

Season 0/specials do not count toward normal season completion.

## Television: caught-up episode rule

An individual episode may become eligible for an episode rating task when all of the following are true:

1. The episode was newly watched after the initial synchronization boundary.
2. The user has watched all normal episodes released so far for the relevant current progression.
3. The newly watched episode is the newest released normal episode.
4. Another normal episode is expected in the future.
5. The season is not already complete.

This is a caught-up rule, not an `airing` show-status rule.

It supports weekly releases and mid-season breaks without generating episode prompts while a user is simply working through a backlog.

## Full-season releases

When an entire season is available at once, WatchWeaver treats viewing as backlog/binge behavior.

Intermediate episode watches do not generate episode rating tasks. Completing the season generates the season rating task.

## Specials / season 0

Season 0 and specials are tracked as watch events but do not generate rating/review prompts by default in v0.1.

They do not block normal-season completion.

## Split seasons and mid-season breaks

If all currently released episodes have been watched and another normal episode is known to be expected later, the newest watched episode can satisfy the caught-up episode rule.

The season itself is not considered complete until its normal episode inventory is released and watched.

## Cancelled or ended shows

Cancellation or show-level status does not independently determine season completion.

If the known normal episode inventory for a season has been released and watched, the season may be complete regardless of whether the show was cancelled or ended conventionally.

## Out-of-order viewing and skipped episodes

Watching episodes out of order is supported.

A season is not complete while a normal episode in its released season inventory remains unwatched.

WatchWeaver does not assume an unwatched normal episode was intentionally skipped. A future explicit completion override may be added later if needed.

## Multiple watches in one ingestion batch

Eligibility should be evaluated against the settled state after a polling/import batch rather than blindly creating a task after each imported row.

If later events in the same batch make an intermediate task obsolete, the obsolete task should not be emitted.

This prevents notification spam when multiple episodes from a binge session arrive together.

## Rewatches

A new movie rewatch is eligible for a new movie rating task.

The task displays the existing current rating when one exists and allows the user to keep or change it.

TV rating storage remains one current rating per season or episode. More sophisticated season/episode rewatch-cycle prompting may be added later; v0.1 must not manufacture duplicate TV prompts merely because historical watch counts increased ambiguously.

## Already-rated content

Discovering/importing an existing rating by itself does not create a rating task.

A genuinely new eligible watch after the baseline boundary may create a task even when the target is already rated.

## Reviews

Reviews are optional.

Completing a rating task does not require review text. Review entry may be offered alongside or after rating submission, but WatchWeaver does not create separate recurring reminders solely because a rating lacks a review.

## Prompt states

A rating/review task supports at least these semantic outcomes:

- `pending`: actionable now
- `snoozed`: hidden until a future timestamp, then actionable again
- `completed`: rating interaction completed
- `skipped`: this specific task is permanently dismissed
- `ignored`: prompting is disabled for the applicable media scope

Exact UI controls and snooze-duration choices are defined by the web/Discord interaction specifications.

At minimum, v0.1 should support never-prompt behavior at the movie or show level.

## Metadata uncertainty

WatchWeaver should err toward silence when release metadata is incomplete, delayed, or contradictory.

It may reevaluate eligibility when metadata is refreshed. It must avoid creating a season-completion or caught-up task based on an episode inventory it cannot reasonably establish.

## Deterministic decision outline

```text
new movie watch
  -> movie rating task

new TV watch/batch
  -> did settled watch state complete the normal season?
       yes -> season rating task
       no  -> is the newest newly-watched episode also the newest released episode,
              are all released normal episodes caught up,
              and is another normal episode expected later?
                yes -> episode rating task
                no  -> no task
```

Season 0/specials bypass the default rating-task path.

## v0.1 acceptance boundary

Given local watch state plus Trakt release/progress metadata, the rule engine must deterministically answer whether to create:

- no task
- a movie rating task
- a season rating task
- an episode rating task

The same settled input state must produce the same result, and batch ingestion must not generate obsolete intermediate prompts.
