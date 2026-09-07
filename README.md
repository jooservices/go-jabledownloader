# jooservices/go-jabledownloader

[![CI](https://github.com/jooservices/go-jabledownloader/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/jooservices/go-jabledownloader/actions/workflows/ci.yml)
[![Coverage (develop)](https://codecov.io/gh/jooservices/go-jabledownloader/branch/develop/graph/badge.svg?token=KYCLSJVFPS)](https://codecov.io/gh/jooservices/go-jabledownloader/branch/develop)
[![Quality Gate (master)](https://sonarcloud.io/api/project_badges/measure?project=jooservices_go-jabledownloader&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=jooservices_go-jabledownloader)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/jooservices/go-jabledownloader/badge)](https://securityscorecards.dev/viewer/?uri=github.com/jooservices/go-jabledownloader)
[![Go Version](https://img.shields.io/badge/Go-1.26-blue.svg)](https://go.dev/)
[![GitHub Release](https://img.shields.io/github/v/release/jooservices/go-jabledownloader?display_name=tag)](https://github.com/jooservices/go-jabledownloader/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A single-binary Go CLI for downloading videos from Jable.TV: Cloudflare
bypass, parallel HLS segment downloads, an interactive picker, self-update,
and optional OpenTelemetry export to the JOOservices OpenObserve platform.

> [!WARNING]
> **`v4.0.0` is a complete rebuild of the previous `jabledownloader` CLI (up to v3.x) and is NOT backward compatible.**
> New module path `github.com/jooservices/go-jabledownloader`, new architecture, new output layout.
> Archived config files and `video.mp4` outputs are ignored; self-update talks to `jooservices/go-jabledownloader` releases.
> See the [changelog](CHANGELOG.md).

## Upgrade highlights

- v4 runs as `jabledownloader` under the new `go-jabledownloader` module; rewrite call sites and archived settings for the v4 API.
- Output naming is now `<code>-<codec>.mp4` (e.g. `start-166-h264.mp4`); the codec is resolved from the master playlist.
- Parallel segment downloads with retry/backoff, cross-run resume, and ffmpeg concat.
- Optional English soft/hard subtitles via host `mlx_whisper` (`--subtitle`).
- Optional, fail-open OpenTelemetry export to JOOservices OpenObserve (`OBS_*` env vars, off by default).

## Features

- `get` a single video by URL or code (e.g. `jur-827`); skips existing files unless `--force`
- `search`, `latest`, `hot` with an interactive multi-select picker and `--count`
- Parallel segment downloading with retry/backoff, cross-run resume, ffmpeg concat
- `--quality` to cap height (`best`, `360`, `480`, `720`, `1080`)
- `--subtitle` — English subtitles via host `mlx_whisper` (`--task translate`)
- `--subtitle-mode soft|hard` — soft = separate track + `.en.srt`; hard = burn-in
- `--dry-run` preview with size estimates
- `config` to persist `output_dir` / `worker_count`
- Self-update from GitHub releases

## Requirements

- ffmpeg (runtime, for concat/fallback; also audio extract + subtitle embed when `--subtitle`)
- Chrome/Chromium (scraping — bypasses Cloudflare)
- **Optional (host, `--subtitle` only):** [`mlx-whisper`](https://pypi.org/project/mlx-whisper/) on PATH
  (Apple Silicon). Install yourself — agents must not install packages:
  ```bash
  uv tool install mlx-whisper
  # or: pipx install mlx-whisper
  ```
  Default model: `mlx-community/whisper-medium` (override with `--whisper-model`).
  Do not use `whisper-large-v3-turbo` for English translate — on MLX it often
  keeps Japanese. `--subtitle-mode hard` also needs an ffmpeg build with **libass** (`subtitles`
  filter). Confirm with `ffmpeg -filters | grep subtitles`. Soft mode works
  without libass.
- Go 1.26 only when building from source; prebuilt archives need none
- Docker is preferred for lint/CI; host Go is OK when it matches `go 1.26`.
  Running the released binary does not require Docker. **Subtitle generation is a
  host feature** (mlx_whisper / Metal) — do not rely on the project Docker image for it.

## Installation

Prebuilt archives for macOS, Linux, and Windows (`amd64`/`arm64`) are attached
to every [GitHub Release](https://github.com/jooservices/go-jabledownloader/releases):

```bash
tar -xzf jabledownloader_v4.2.0_darwin_arm64.tar.gz
sudo mv jabledownloader /usr/local/bin/
```

Or build from source (Go 1.26):

```bash
make build   # host binary into bin/jabledownloader
```

## Quick start

```bash
jabledownloader get jur-827
jabledownloader get https://en.jable.tv/videos/abf-382/ --subtitle
jabledownloader get abf-382 --subtitle --subtitle-mode hard
jabledownloader search cute --dry-run
jabledownloader latest --count 5
```

English subtitles (`--subtitle`) run on the **host** after the MP4 is ready:
`ffmpeg` extracts audio → `mlx_whisper --task translate` (default model
`mlx-community/whisper-medium`, spoken language `ja`) → `.en.srt`, then either
**soft** mux (`mov_text`, language `eng`; default) or **hard** burn-in (pixels;
needs ffmpeg with libass).

## CLI / commands

| Command | Purpose |
| --- | --- |
| `jabledownloader get <url\|code>` | Download a single video |
| `jabledownloader search <query>` | Search and download (`--count`) |
| `jabledownloader latest` | Download the latest videos (`--count`) |
| `jabledownloader hot` | Download the trending videos (`--count`) |
| `jabledownloader update` | Self-update from GitHub releases (`--check`) |
| `jabledownloader config` | Show or set persisted settings |
| `jabledownloader completion <shell>` | Shell completion for bash/zsh/fish/powershell |

## Configuration

Persisted settings live in `~/.config/jabledownloader/config.json`
(`jabledownloader config` / `config set`).

| Env var | Purpose |
| --- | --- |
| `CHROME_PATH` | Chromium/Chrome binary for scraping (Docker sets `/usr/bin/chromium`) |
| `OBS_ENDPOINT` | OpenObserve URL; unset = telemetry disabled (default) |
| `OBS_ORG` | OBS organization (default `jooservices`) |
| `OBS_STREAM` | OBS stream (default `jabledownloader`) |
| `OBS_USER` | OBS ingestion user email |
| `OBS_PASSWORD` | OBS ingestion user password |

Env names only — values never live in this repository.

## Observability (optional)

Telemetry is off by default and not bundled: the CLI works fully without any
OBS component. OBS is a separate project — `jooservices/openobserve` — that
exposes a docker-compose OpenObserve instance. To enable it, start OBS, create
an ingestion user in the `jooservices` org (never root), export the `OBS_*`
variables above, and inspect at `http://localhost:5080` (stream
`jabledownloader`). Fail-open: an unreachable OBS never affects downloads.

## Design notes

Project rules live in [AGENTS.md](AGENTS.md). Key user-facing invariants:

- Exit codes: `0` success, `1` error, `2` partial batch failure
- Output naming: `<code>-<codec>.mp4` (codec from master playlist, h264 fallback);
  `--subtitle` also writes `<code>-<codec>.en.srt`
- Layered `internal/` packages; the HLS engine is pure (no UI/config/telemetry
  dependencies); scraper tests use fixture data and never launch Chrome

## Documentation

- [Changelog](CHANGELOG.md) — version history and upgrade notes
- [Contributing guide](CONTRIBUTING.md) — setup, git workflow, commit convention, quality gates, PR rules
- [Development workflows](WORKFLOWS.md) — branches, CI, releases, and repository automation
- [Security policy](SECURITY.md) — private vulnerability reporting

## Development

Go tooling runs in Docker so the toolchain matches CI (`tools/ci/docker-compose`,
Go 1.26 image); host Go is OK when it matches `go 1.26`.

```bash
tools/install-git-hooks   # once after clone (commit-msg, pre-commit, pre-push)
make docker-test          # fmt + vet + lint + test in the CI container
make docker-run           # run the image (ARGS="get jur-827")
make release              # cross-compiled archives into dist/
```

## Community

- [Contributing guide](CONTRIBUTING.md) — setup, git workflow, commit convention, quality gates, PR rules
- [Security policy](SECURITY.md) — how to report vulnerabilities privately
- [Code of Conduct](CODE_OF_CONDUCT.md)
- [Support](SUPPORT.md)
- [Governance](GOVERNANCE.md)

## License

MIT — see [LICENSE](LICENSE).
