# go-jabledownloader

Download videos from Jable.TV — a single-binary Go CLI with Cloudflare
bypass, parallel HLS segment downloads, an interactive picker, self-update,
and optional OpenTelemetry export to the JOOservices OpenObserve platform.

## About v4.0.0

This line is a **rebuild** of the archived `jabledownloader` project
(`archives/JOOservices.2/jabledownloader`). **No backward compatibility** is
kept with the archived binary, its config, or its output layout.

## Highlights vs the previous line

- Correct ownership and structure: module
  `github.com/jooservices/go-jabledownloader`, Go 1.25, layered
  `internal/` packages with a pure HLS engine
- Output naming `<code>-<codec>.mp4` (e.g. `start-166-h264.mp4`) instead of
  `video.mp4`; codec resolved from the master playlist
- Download progress UI: animated segment bar, ffmpeg progress with ETA and
  speed, resume reporting; newline snapshots when piped
- Optional, fail-open OpenTelemetry export to the JOOservices OpenObserve
  platform (`OBS_*` env vars, off by default)
- CLI UX: `--force`, `--verbose`, `--quiet`, `--no-color`, grouped help,
  picker with selection counter, exit codes 0/1/2
- Repository hygiene: no committed binaries or downloads; Docker-based
  dev/test/CI; golangci-lint standard; full docs
  (`knowledge.md` / `implementation.md` / `plan.md`)
- **Breaking**: archived config files and `video.mp4` outputs are ignored;
  self-update now talks to `jooservices/go-jabledownloader` releases

## Features

- `get` a single video by URL or code (e.g. `jur-827`)
- `search`, `latest`, `hot` with an interactive multi-select picker
- Parallel segment downloading with retry/backoff, resume, ffmpeg concat
- `--dry-run` preview with size estimates
- Self-update from GitHub releases
- Optional OTLP logs/metrics/traces to OpenObserve (fail-open)

## Requirements

- Prebuilt binary from a release archive, or Go 1.25 to build
- ffmpeg (runtime), Chrome/Chromium (scraping)

## Installation

```bash
# From a release archive (jabledownloader_v4.0.0_<goos>_<goarch>.tar.gz):
tar -xzf jabledownloader_v4.0.0_darwin_arm64.tar.gz
sudo mv jabledownloader /usr/local/bin/

# Or build locally / via Docker:
make build                 # bin/jabledownloader
make docker-build          # jooservices/go-jabledownloader:latest
```

## Quick start

```bash
jabledownloader get jur-827
jabledownloader search cute --dry-run
jabledownloader latest --count 5
jabledownloader update
```

## Design contract

See `knowledge.md` (design authority) and `implementation.md` (execution
contract). Key invariants: layered `internal/` packages, pure HLS engine,
fixture-driven scraper tests, `<code>-<codec>.mp4` output naming.

## Observability (optional)

Copy `.env.example` to `.env`, set `OBS_*` to the JOOservices OpenObserve
instance (repo `jooservices/openobserve`), and export them:

```bash
export OBS_ENDPOINT=http://localhost:5080 OBS_ORG=jooservices \
       OBS_STREAM=jabledownloader OBS_USER=... OBS_PASSWORD=...
```

Without `OBS_ENDPOINT` the CLI runs with telemetry disabled.

## Releases

`make release` cross-compiles the six platform archives
(`jabledownloader_vX.Y.Z_{darwin,linux,windows}_{amd64,arm64}.tar.gz`) plus
checksums into `dist/` — the exact assets the `update` command consumes.

## Development

Docker-only loop:

```bash
make ci              # fmt + vet + lint + test
make cover           # coverage report
make docker-run      # ARGS="get jur-827"
```

## Branch model & CI

`master`/`develop`, PR required, CI green before merge — see the workspace
root `AGENTS.md`. CI runs the same containers as local development.

## License

MIT — see `LICENSE`.
