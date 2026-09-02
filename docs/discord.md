# Discord Integration

## Status

This document defines the Discord scope for WatchWeaver v0.1 and the boundary for later interactive bot functionality.

## v0.1 role

Discord is an optional outbound notification surface only.

WatchWeaver v0.1 does not accept ratings, reviews, commands, buttons, select-menu actions, modals, snoozes, skips, or configuration changes through Discord.

All user control remains in the WatchWeaver web interface.

This intentionally keeps the first release simple while preserving a path to an interactive bot later.

## Delivery mechanism

Because v0.1 is announcement-only, implementation should use the simplest supported Discord delivery mechanism that satisfies reliable channel notifications.

An interactive Discord bot library is not required for the v0.1 milestone solely for future functionality. Bot-based interactions can be introduced when the interactive Discord roadmap work begins.

## Notifications

Discord may announce actionable WatchWeaver state such as:

- a movie is ready to rate/review
- a season is ready to rate/review
- an eligible caught-up episode is ready to rate/review
- Serializd synchronization is due
- important integration/synchronization failures where user attention is useful

Notifications should direct the user back to the WatchWeaver web interface for actions.

## Prompt notifications

A rating/review notification should identify the media target and why it is actionable without requiring the user to interact with Discord.

Examples conceptually include:

```text
WatchWeaver: Dune: Part Two is ready to rate.
Open WatchWeaver to rate or review it.
```

```text
WatchWeaver: Season 2 of Example Show is ready to rate.
Open WatchWeaver to rate or review it.
```

The exact presentation may evolve during UI implementation, but Discord must not become the source of truth for prompt state.

## Notification fatigue

WatchWeaver should avoid replaying a large historical notification backlog after Discord is configured or recovers from an outage.

Initial Trakt baseline history never generates Discord rating notifications.

For normal new activity, individual actionable notifications are acceptable. If a large number of tasks become eligible together, WatchWeaver may emit a summary notification instead of flooding the configured channel.

The durable task queue remains in WatchWeaver regardless of notification delivery.

## Failure behavior

Discord failure must never block Trakt ingestion, rating/review workflows, exports, synchronization bookkeeping, or the web UI.

Notification delivery state may be recorded locally for observability and duplicate suppression.

A failed Discord notification may be retried conservatively, but WatchWeaver must not create a notification storm after reconnecting.

## Configuration and privacy

Discord is independently optional. If Discord configuration is absent or disabled, the subsystem does not run and the rest of WatchWeaver behaves normally.

Discord credentials, webhook URLs, channel identifiers, guild identifiers, and message identifiers must never be hard-coded into the repository. The normal webhook setup path is the write-only web wizard; values are encrypted at rest and never returned by status APIs. The wizard accepts only official `discord.com` or `discordapp.com` HTTPS webhook URLs. Environment configuration is an optional locked administrator override.

Secrets must not be logged. Identifiers should only be logged when operationally useful and should not appear in public example configuration.

## Deferred interactive bot roadmap

A later WatchWeaver version may promote Discord from announcement-only to an interactive control surface.

Potential future capabilities include:

- rate movies/seasons/episodes from Discord
- enter/edit reviews using Discord modals
- snooze or skip prompts
- ignore a movie/show
- inspect the unrated queue
- inspect integration status
- trigger supported synchronization actions
- acknowledge Serializd synchronization

Those features require a separate design pass covering bot library choice, Discord permissions/intents, component UX, authorization, command behavior, and interaction-state handling.

They are explicitly outside the v0.1 acceptance boundary.

## v0.1 acceptance boundary

Discord support is sufficient for v0.1 when:

1. Discord can be enabled or disabled independently.
2. WatchWeaver can send configured outbound notifications for newly actionable rating/review tasks.
3. WatchWeaver can send a Serializd sync-due notification when that workflow requires attention.
4. Notifications direct the user to the web application for action.
5. Discord outages do not block any core WatchWeaver workflow.
6. Restart/recovery does not flood Discord with historical notifications.
7. No Discord secrets or instance-specific identifiers are committed to the public repository.
