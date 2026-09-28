# jooservices/go-jabledownloader

[![CI](https://github.com/jooservices/go-jabledownloader/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/jooservices/go-jabledownloader/actions/workflows/ci.yml)
[![Coverage (develop)](https://codecov.io/gh/jooservices/go-jabledownloader/branch/develop/graph/badge.svg?token=KYCLSJVFPS)](https://codecov.io/gh/jooservices/go-jabledownloader/branch/develop)
[![Quality Gate (master)](https://sonarcloud.io/api/project_badges/measure?project=jooservices_go-jabledownloader&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=jooservices_go-jabledownloader)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/jooservices/go-jabledownloader/badge)](https://securityscorecards.dev/viewer/?uri=github.com/jooservices/go-jabledownloader)
[![Go Version](https://img.shields.io/badge/Go-1.26-blue.svg)](https://go.dev/)
[![GitHub Release](https://img.shields.io/github/v/release/jooservices/go-jabledownloader?display_name=tag)](https://github.com/jooservices/go-jabledownloader/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A single-binary Go CLI for downloading videos from multiple sites (Jable.TV,
EPORNER, ...). The site is auto-detected from the input — no `--site` flag.
Jable uses a Cloudflare-bypassing Chrome fetch and parallel HLS segment
downloads; EPORNER is server-rendered, so it downloads direct MP4 files over
plain HTTP with parallel Range chunks. Also includes an interactive picker,
self-update, and optional OpenTelemetry export to the JOOservices OpenObserve
platform.

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

- Multi-site with auto-detection: `download` resolves the provider from the URL
  (or Jable code); no `--site` flag
- EPORNER: direct MP4 download (240p–1080p, h264/av1) over parallel Range
  chunks with resume; no browser required
- `download` a single video by URL or code (e.g. `jur-827`); skips existing files unless `--force`
- `get` to inspect a video (source URLs, or `--json` full detail) without downloading
- Uniform site contract: every provider implements `List`, `Search`, and
  `Detail`; listing rows include site, code, title, URL, thumbnail, and duration
- `latest` aggregates the latest rows from every registered site; `search <keyword>`
  searches every registered site by the supplied keyword
- `site <name> list` and `site <name> search <keyword>` scope discovery to one
  provider; pass `--view` for a provider-specific listing such as Jable `hot`
- In an interactive terminal, discovery shows progress, prints the result rows,
  and opens the multi-select picker so selected rows can be downloaded through
  the normal download pipeline. Piped/JSON output remains display-only unless
  `--download` is passed explicitly.
- Parallel segment downloading with retry/backoff, cross-run resume, ffmpeg concat
- `--quality` to cap height (`best`, `240`, `360`, `480`, `720`, `1080`)
- `--subtitle` — subtitles via host `mlx_whisper`; English by default
  (Whisper translates directly), other languages via `--subtitle-lang` + `--translator`
- `--subtitle-mode soft|hard` — soft = separate track + `.<lang>.srt`; hard = burn-in
- `--dry-run` preview with size estimates
- Videos are saved to `<out>/<site>/<code>/`; customise with `--path-template`
  (see [Output layout](#output-layout))
- `config` to persist `output_dir` / `path_template` / `worker_count`
- Self-update from GitHub releases

## Requirements

- ffmpeg (HLS concat/remux; also audio extract + subtitle embed when `--subtitle`)
- Chrome/Chromium (Jable scraping only — bypasses Cloudflare). EPORNER and
  other server-rendered sites need no browser
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
to every [GitHub Release](https://github.com/jooservices/go-jabledownloader/releases).
macOS example (Linux archives contain the same `jabledownloader` binary;
Windows archives contain `jabledownloader.exe`):

```bash
tar -xzf jabledownloader_v4.4.0_darwin_arm64.tar.gz
sudo mv jabledownloader /usr/local/bin/
```

Or build from source (Go 1.26):

```bash
make build   # host binary into bin/jabledownloader
```

## Quick start

```bash
jabledownloader download jur-827
jabledownloader download https://en.jable.tv/videos/abf-382/ --subtitle
jabledownloader download abf-382 --subtitle --subtitle-mode hard
jabledownloader download https://www.eporner.com/video-1XrYk0gaMpV/daisy-f-x/ --quality 720
jabledownloader get jur-827
jabledownloader latest --count 5
jabledownloader search "cute" --site jable --count 5
jabledownloader site jable list --view hot --count 5
jabledownloader site eporner search "sample words" --count 5
```

Subtitles (`--subtitle`) run on the **host** after the MP4 is ready, as a
pipeline of replaceable steps:

1. **Extract audio** — `ffmpeg` (16 kHz mono WAV).
2. **Transcribe** — `mlx_whisper` (default model `mlx-community/whisper-medium`,
   spoken language `--spoken-language`, default `ja`).
3. **Translate** — with the default `--translator auto` and `--subtitle-lang en`,
   Whisper translates directly (`--task translate`). For any other language,
   Whisper transcribes and the named `--translator` translates.
4. **Apply** — **soft** mux (`mov_text` track; default) or **hard** burn-in
   (needs ffmpeg with libass). The sidecar `<video>.<lang>.srt` marks success;
   re-runs skip (hard subtitles are never burned twice).

```bash
jabledownloader download abf-382 --subtitle                       # English
jabledownloader download abf-382 --subtitle --subtitle-lang vi --translator <name>
```

### Adding a translator

Translators plug into a registry; the pipeline and the `--translator` flag
pick them up without other changes:

1. Create `internal/media/translate/<name>/` implementing `translate.Translator`
   (keep cue count and timings; honour `ctx`; return
   `translate.ErrUnsupportedPair` for pairs it cannot handle).
2. Call `translate.Register("<name>", factory)` in its `init`.
3. Blank-import the package in `cmd/jabledownloader/wiring.go`.
4. Run the shared suite in its tests: `contracttest.Run(t, factory)`.

### Output layout

Each video gets its own directory under `--out` (default `./videos`), built
from `--path-template` / `config set path_template` (default `{site}/{code}`):

| Template | Result |
| --- | --- |
| `{site}/{code}` (default) | `videos/jable/abc-123/abc-123-h264.mp4` |
| `{code}` | `videos/abc-123/…` (the pre-v5 layout) |
| `library/{site}-{code}` | `videos/library/jable-abc-123/…` |

The template must contain `{code}` and stay inside `--out`. Videos already in
the pre-v5 `<out>/<code>/` layout are still recognised as downloaded.

## CLI / commands

| Command | Purpose |
| --- | --- |
| `jabledownloader download <url\|code>` | Download a single video (site auto-detected from the input) |
| `jabledownloader download <url\|code> --name <file>` | Download with a custom output filename |
| `jabledownloader get <url\|code>` | Show video info (source URLs, or `--json` full detail) without downloading |
| `jabledownloader latest` | Display latest rows from every site (`--site`, `--page`, `--count`, `--json`) |
| `jabledownloader search <keyword>` | Search every site by keyword (`--site`, `--page`, `--count`, `--json`) |
| `jabledownloader site <name> list` | Display one site's listing (`--view`, `--page`, `--count`, `--json`) |
| `jabledownloader site <name> search <keyword>` | Search one site by keyword (`--page`, `--count`, `--json`) |
| `… --download` | Explicitly select and download discovered rows; all discovery commands are display-only otherwise |
| `jabledownloader update` | Self-update from GitHub releases (`--check`) |
| `jabledownloader config` | Show or set persisted settings |
| `jabledownloader completion <shell>` | Shell completion for bash/zsh/fish/powershell |

## Configuration

Persisted settings live in `~/.config/jabledownloader/config.json`
(`jabledownloader config` / `config set`).

| Env var | Purpose |
| --- | --- |
| `CHROME_PATH` | Chromium/Chrome binary for scraping (Docker sets `/usr/bin/chromium`) |
| `CHROME_NO_SANDBOX` | `1` forces Chrome's `--no-sandbox`; `0` forbids the automatic fallback. Unset: sandbox on, retried without it only if Chrome cannot start (as root, e.g. in Docker, chromedp disables it automatically) |
| `JABLE_CHROME_PROFILE` | Optional Chrome profile directory; keeps the Cloudflare clearance cookie between runs |
| `OBS_ENDPOINT` | OpenObserve URL; unset = telemetry disabled (default). Credentials require `https` unless the host is loopback |
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

- Exit codes: `0` success, `1` error, `2` partial batch or multi-site discovery
  failure, `130` interrupted (re-run the same command to resume)
- Output naming: `<code>-<codec>.mp4` (codec from master playlist or the MP4
  source, h264 fallback); `--subtitle` also writes `<code>-<codec>.<lang>.srt`
- `get --json` keeps the v4.3 JSON keys
- Layered packages (enforced by `internal/archtest`):

  ```
  cmd/jabledownloader   composition root: flags, wiring, exit codes
  internal/ui/cli       terminal UI: renders app events, picker, prompts
  internal/app          use-cases; talks to UIs only via Reporter/Prompter
  internal/site/*       sites resolve pages into domain values (jable, eporner)
  internal/engine/*     transport engines by source kind (hls, progressive)
  internal/media/*      subtitle pipeline: audio → asr → translate → subtitle
  internal/domain       shared values and events
  internal/platform/*   HTTP clients shared by sites and engines
  ```

  Adding a site means one package under `internal/site/` plus a blank import
  in `cmd`; engines are reused. Site test fixtures are real pages captured by
  `go test -tags fixtures ./internal/site/...`, never hand-written.
- The site is auto-detected from a video input; discovery may address all sites
  or one named provider. Jable additionally needs a browser, EPORNER does not.
  Jable views: `latest`, `hot`; EPORNER views: `latest`, `all`, `most-viewed`,
  `top-rated`.

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
make docker-test          # fmt + vet + lint + unit/coverage in the CI container
make test-browser         # real-Chrome browser tests (e2e tag), merges coverage
make docker-run           # run the image (ARGS="get jur-827")
make release              # cross-compiled archives into dist/
```

`make test` writes `coverage.out` and fails below **85% total statement
coverage**. The Playwright E2E suite uses a local Jable-shaped fixture: it
checks rendered DOM, then drives the production Chromium fetcher through
listing parsing and HLS-detail extraction. It never calls third-party sites.

## Community

- [Contributing guide](CONTRIBUTING.md) — setup, git workflow, commit convention, quality gates, PR rules
- [Security policy](SECURITY.md) — how to report vulnerabilities privately
- [Code of Conduct](CODE_OF_CONDUCT.md)
- [Support](SUPPORT.md)
- [Governance](GOVERNANCE.md)

## License

MIT — see [LICENSE](LICENSE).
