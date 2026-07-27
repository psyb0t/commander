# Changelog

All notable changes per release. Versions follow [semver](https://semver.org).

## v0.5.4 — 2026-07-27

Modernize the toolchain and CI.

- Go 1.26.
- `make lint` now runs `go fix -diff ./...` before `golangci-lint` (was `go tool modernize`, which is dropped along with its dependency).
- Added a coverage badge — `test-coverage` writes `coverage-percent.txt`, wired into the `badges` CI job and README.
- Logging (`log/slog`) and error wrapping (`github.com/psyb0t/ctxerrors`) were already in place; verified, no changes needed.

## v0.5.3 — 2026-07-27

Add README status badges.

- Added self-hosted version and license badges (rendered as SVGs on the `badges` branch by the `create-badges` CI job, no third-party render service). Wired a badges job into pipeline.yml.
