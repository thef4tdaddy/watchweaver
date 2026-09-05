# Contributing to WatchWeaver

Thanks for your interest in WatchWeaver.

WatchWeaver is currently in early development, so design decisions and interfaces may change quickly.

## Before making a large change

Please open or join a GitHub issue before starting a substantial feature, integration, schema change, or architectural rewrite. This helps avoid duplicated work and gives the project a place to agree on behavior before implementation.

Small bug fixes, documentation corrections, tests, and narrowly scoped improvements do not need extensive advance discussion.

## Privacy and test data

Never commit or attach real credentials or private instance data. This includes:

- Trakt client secrets or access tokens
- Discord bot tokens or webhook URLs
- Private Discord server/channel identifiers when they are not necessary
- WatchWeaver databases
- Personal watch-history exports
- Generated Letterboxd CSV files containing real account activity
- Logs containing credentials or personal viewing data
- NAS hostnames, private IP addresses, or private filesystem layouts when they are not required to reproduce a problem

Use sanitized fixtures and placeholder values in examples and tests.

## Development expectations

As the implementation is established, this document will be expanded with the supported Go and Node versions, formatting commands, tests, linting, and local Docker workflow.

Until then:

1. Keep changes focused.
2. Include tests when changing behavior once a test framework exists.
3. Update documentation when changing user-visible behavior or configuration.
4. Do not introduce an unofficial/private third-party API as a hard dependency of the core workflow without prior discussion.

### Local CI-equivalent checks

Run from repository root:

```bash
go vet ./...
go test -race -covermode=atomic -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

CI enforces a repository-wide coverage floor (currently `64.3%`) using the total value from `go tool cover -func=coverage.out`. Increase the floor intentionally in `.github/workflows/go-ci.yml` when coverage improves.

The race detector is enabled in CI on `ubuntu-latest`, where Go race builds are supported.

## Pull requests

Pull requests should explain what changed, why it changed, how it was tested, and any new configuration or migration requirements.

Normal `feat/*`, `fix/*`, documentation, and dependency pull requests target `develop`. A green merge to `develop` publishes the multi-architecture `beta` container plus an immutable `sha-…` tag. Pull requests build and test containers but never publish them.

Stable promotion uses an explicit `develop` → `main` pull request after beta testing. Stable `vX.Y.Z` tags are created from `main`; only those tags publish `latest`. Optional numbered prereleases such as `v0.2.0-beta.1` are cut from `develop`, while ordinary develop builds use only `beta` and their SHA tag.

Emergency `hotfix/*` branches start from `main`, merge to `main`, and must then be merged back into `develop`. Direct and force pushes to either long-lived branch are prohibited. Use squash merges so history remains linear.

## License

By contributing to WatchWeaver, you agree that your contribution may be distributed under the project's PolyForm Noncommercial License 1.0.0.
