# go-jabledownloader

[![Release](https://img.shields.io/badge/version-4.0.0-blue.svg)](CHANGELOG.md)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Download videos from Jable.TV — a single-binary Go CLI with Cloudflare
bypass, parallel HLS segment downloads, an interactive picker, self-update,
and optional OpenTelemetry export to the JOOservices OpenObserve platform.

## About v4.0.0

This line is a complete rebuild of the archived `jabledownloader` project
(`archives/JOOservices.2/jabledownloader`): new module path
`github.com/jooservices/go-jabledownloader`, new architecture, new output
layout.

> [!WARNING]
> **`v4.0.0` is a complete rebuild and is NOT backward compatible.**
> Archived config files and `video.mp4` outputs are ignored, and self-update
> talks to `jooservices/go-jabledownloader` releases. See the
> [changelog](CHANGELOG.md).

## Highlights vs the previous line

- Layered `internal/` architecture with a pure HLS engine (no UI/config
  dependencies in the download core)
- Output naming `<code>-<codec>.mp4` (e.g. `start-166-h264.mp4`); codec
  resolved from the master playlist
- Download progress UI: animated segment bar, ffmpeg progress with ETA and
  speed, resume reporting; newline snapshots when piped
- Optional, fail-open OpenTelemetry export to the JOOservices OpenObserve
  platform (`OBS_*` env vars, off by default)
- CLI UX: `--force`, `--verbose`, `--quiet`, `--no-color`, grouped help,
  picker with selection counter, exit codes 0/1/2
- Repository hygiene: no committed binaries or downloads; Docker-based
  dev/test/CI; golangci-lint standard; full docs
  (`knowledge.md` / `implementation.md` / `plan.md`)

## Features

- `get` a single video by URL or code (e.g. `jur-827`)
- `search`, `latest`, `hot` with an interactive multi-select picker
- Parallel segment downloading with retry/backoff, resume, ffmpeg concat
- `--dry-run` preview with size estimates
- Self-update from GitHub releases

## Requirements

- ffmpeg (runtime, for concat/fallback)
- Chrome/Chromium (scraping — bypasses Cloudflare)
- Go 1.25 only when building from source; prebuilt archives need none
- Docker is optional: required for development/CI, not for running the
  released binary

## Quick start

```bash
# Prebuilt release archive — no Docker needed:
tar -xzf jabledownloader_v4.0.0_darwin_arm64.tar.gz
sudo mv jabledownloader /usr/local/bin/

jabledownloader get jur-827
jabledownloader search cute --dry-run
jabledownloader latest --count 5
```

## CLI / commands

| Command | Purpose |
| --- | --- |
| `jabledownloader get <url\|code>` | Download a single video |
| `jabledownloader search <query>` | Search and download |
| `jabledownloader latest` | Download the latest videos (`--count`) |
| `jabledownloader hot` | Download the trending videos (`--count`) |
| `jabledownloader update` | Self-update from GitHub releases (`--check`) |
| `jabledownloader completion <shell>` | Shell completion for bash/zsh/fish/powershell |

## Typical workflow

1. `jabledownloader search cute` — pick videos in the interactive list
2. Review the plan (durations + size estimates), confirm
3. Progress bars show segments/ETA/speed; interrupted runs **resume** on the
   next invocation
4. Output lands in `./videos/<code>/<code>-<codec>.mp4`
5. Batch failures exit with code `2`; `--force` re-downloads existing files

## Docker commands

Optional — for running the image instead of a host binary, and mandatory
for development:

```bash
make build          # host binary into bin/
make docker-build   # jooservices/go-jabledownloader:latest
make docker-run     # ARGS="get jur-827"
make release        # cross-compiled archives into dist/
make ci             # fmt + vet + lint + test (container)
```

## Configuration

| Env var | Purpose |
| --- | --- |
| `OBS_ENDPOINT` | OpenObserve URL; unset = telemetry disabled (default) |
| `OBS_ORG` | OBS organization (default `jooservices`) |
| `OBS_STREAM` | OBS stream (default `jabledownloader`) |
| `OBS_USER` | OBS ingestion user email |
| `OBS_PASSWORD` | OBS ingestion user password |

Env names only — values never live in this repository.

## Observability (optional)

Telemetry is off by default and not bundled: the CLI works fully without
any OBS component. OBS is a separate project — `jooservices/openobserve` —
that exposes a docker-compose OpenObserve instance.

1. **Start OBS** (see its README): `make up && make status`, then
   `make smoke` to bootstrap the org + stream
2. **Create an ingestion user** in the `jooservices` org (IAM → Users) —
   never use root credentials
3. **Export the `OBS_*` variables** from the table above, then run any
   command
4. **Inspect** at `http://localhost:5080` (switch org to `jooservices`):
   stream `jabledownloader` for logs/traces; metric streams
   `crawl_request_total`, `hls.videos`, duration histograms

Fail-open: an unreachable OBS never affects downloads.

## Design contract

See `knowledge.md` (design authority) and `implementation.md` (execution
contract). Key invariants: layered `internal/` packages, pure HLS engine,
fixture-driven scraper tests, `<code>-<codec>.mp4` output naming.

## Documentation

- [CHANGELOG.md](CHANGELOG.md)
- [CONTRIBUTING.md](CONTRIBUTING.md)
- [SECURITY.md](SECURITY.md)
- [knowledge.md](knowledge.md) / [implementation.md](implementation.md) /
  [plan.md](plan.md)

## Development

Docker-only loop; CI runs the same containers:

```bash
make ci              # fmt + vet + lint + test
make cover           # coverage report
```

## Branch model & CI

`master`/`develop`, PR required, CI green before merge — see the workspace
root `AGENTS.md`. Workflows: `.github/workflows/`.

## Community

- Issues: https://github.com/jooservices/go-jabledownloader/issues
- Security: [SECURITY.md](SECURITY.md)
- Contributing: [CONTRIBUTING.md](CONTRIBUTING.md)

## License

[MIT](LICENSE)
