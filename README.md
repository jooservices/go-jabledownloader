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

- ffmpeg (runtime, for concat/fallback)
- Chrome/Chromium (scraping — bypasses Cloudflare)
- Go 1.25 only when building from source; prebuilt archives need none
- Docker is **optional**: required for development/CI, not for running the
  released binary

## Installation

```bash
# 1. Prebuilt release archive — no Docker needed:
tar -xzf jabledownloader_v4.0.0_darwin_arm64.tar.gz
sudo mv jabledownloader /usr/local/bin/

# 2. Build from source (inside the build container):
make build                 # bin/jabledownloader

# 3. Or run the Docker image instead of a host binary:
make docker-build          # jooservices/go-jabledownloader:latest
docker run --rm -it --shm-size=1g -v $PWD/videos:/data \
    jooservices/go-jabledownloader:latest get jur-827
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

## Observability — optional, with the JOOservices OpenObserve

Telemetry is **off by default and not bundled**: jabledownloader works fully
without any OBS component, and `OBS_*` env vars are the only link between
the two projects. Fail-open: an unreachable OBS never affects downloads.

### Step 1 — start OpenObserve (one time)

OBS lives in its own repository, `jooservices/openobserve`:

```bash
git clone git@github.com:jooservices/openobserve.git
cd openobserve
cp .env.example .env        # set OBS_ROOT_EMAIL / OBS_ROOT_PASSWORD
make up && make status      # UI on http://localhost:5080
make smoke                  # bootstrap org jooservices + jabledownloader stream
```

### Step 2 — create the ingestion user (one time)

jabledownloader must not use root credentials. Create a producer user in the
`jooservices` org (UI: IAM → Users, or API
`POST /api/jooservices/users`), e.g. `telemetry@jooservices.com`.

### Step 3 — point jabledownloader at it

```bash
export OBS_ENDPOINT=http://localhost:5080
export OBS_ORG=jooservices
export OBS_STREAM=jabledownloader
export OBS_USER=telemetry@jooservices.com
export OBS_PASSWORD=...          # from the producer user

jabledownloader get jur-827
```

### Step 4 — inspect the data

Open `http://localhost:5080`, log in, switch the org dropdown to
**jooservices**, then open:

- Stream `jabledownloader` (logs) — crawl/parse/download events with
  `code`, `title`, `video_id`, `trace_id`
- Stream `jabledownloader` (traces) — `run.get` → `crawl.fetch_video_info`
  → `video.download` spans
- Metric streams — `crawl_request_total{status}`, `hls.videos{outcome}`,
  `crawl_fetch_video_info_duration_ms`, `hls.video.duration_ms`

## Releases

`make release` cross-compiles the six platform archives
(`jabledownloader_vX.Y.Z_{darwin,linux,windows}_{amd64,arm64}.tar.gz`) plus
checksums into `dist/` — the exact assets the `update` command consumes.

## Development

Docker-only loop (development and CI run in containers):

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
