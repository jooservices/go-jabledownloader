# go-jabledownloader

Download videos from Jable.TV — a single-binary Go CLI with Cloudflare
bypass, parallel HLS segment downloads, an interactive picker, self-update,
and optional OpenTelemetry export to the JOOservices OpenObserve platform.

## About v1.0.0

This line is a **rebuild** of the archived `jabledownloader` project
(`archives/JOOservices.2/jabledownloader`). No backward compatibility with
the archived binary or its `video.mp4` output layout is kept; downloads are
now named `<code>-<codec>.mp4`.

## Features

- `get` a single video by URL or code (e.g. `jur-827`)
- `search`, `latest`, `hot` with an interactive multi-select picker
- Parallel segment downloading with retry/backoff, ffmpeg concat
- `--dry-run` preview with size estimates
- Self-update from GitHub releases
- Optional OTLP logs/metrics/traces to OpenObserve (fail-open)

## Requirements

- Go 1.25 (build), or the prebuilt Docker image
- ffmpeg (runtime), Chrome/Chromium (scraping)

## Installation

```bash
make build                 # bin/jabledownloader
# or
make docker-build          # jooservices/go-jabledownloader:latest
```

## Quick start

```bash
jabledownloader get jur-827
jabledownloader search cute --dry-run
jabledownloader latest --count 5
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
