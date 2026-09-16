# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [4.3.0] - 2026-09-16

### Added

- Multi-site support with auto-detection: the provider is resolved from the
  input (URL host or Jable code); no `--site` flag
- EPORNER site adapter (`internal/site/eporner`): server-rendered video pages
  expose direct MP4 sources (240p–1080p, h264/av1); downloads need no browser
- `internal/site` provider abstraction: `Site` interface, `VideoInfo` with a
  source list, `Fetcher` + plain `HTTPFetcher`, and an auto-detect registry
- `internal/direct` engine: progressive MP4 downloads with parallel Range
  chunks, retry/backoff, cross-run resume, and shared progress events
- `--quality` now also accepts `240`

### Changed

- `internal/scraper` moved to `internal/site/jable` and now implements the
  shared `Site` contract
- `internal/app` picks the download strategy from the source kind (HLS vs
  direct) instead of hard-coding the HLS engine
- The Jable browser starts lazily on first use, so EPORNER `get` never needs
  Chrome

## [4.2.0] - 2026-09-05

### Added

- `--subtitle` embeds English subtitles after download via host `mlx_whisper`
  (`--task translate`) and ffmpeg
- `--subtitle-mode soft|hard` — soft (default) = separate `mov_text` track +
  `.en.srt`; hard = burn-in (requires ffmpeg with libass / `subtitles` filter)
- `--whisper-model` (default `mlx-community/whisper-medium`)
- `--spoken-language` (default `ja`; empty for auto-detect)
- Richer download progress: multi-line bar with segments, downloaded / estimated
  size, bytes left, current + average speed, elapsed, and ETA
- `internal/subtitle` package: audio extract, Whisper translate, soft mux / hard
  burn, English-output validation

### Changed

- Existing-video detection prefers the primary `<code>-<codec>.mp4` and skips
  derived names such as `*.hard.mp4`
- Default Whisper model is `whisper-medium` (not `large-v3-turbo`); turbo often
  ignores `--task translate` and keeps Japanese on Apple MLX

### Fixed

- mlx_whisper SRT naming: `--output-name` must not end in `.en` (Path.with_suffix
  would strip it); normalize to `<code>-<codec>.en.srt`
- Reject mostly-Japanese subtitle files so a failed translate cannot be muxed
  as English
- Whisper output uses an isolated temp dir so a pre-existing `.en.srt` cannot be
  treated as this run's translate result
- Skip re-embed when `.en.srt` already exists (avoids double hard-burn)
- Preserve source video permission bits on the subtitled MP4 and `.en.srt`
- Progress TTY overwrite uses `lines-1` cursor-up; `stop` waits for the renderer
  before clearing the block

## [4.1.0] - 2026-09-05

### Added

- `search --count` / `-n` (parity with `latest` / `hot`)
- `--quality` (`best`, `360`, `480`, `720`, `1080`) to cap master-playlist height
- `config` / `config get` / `config set` for `output_dir` and `worker_count`
- Docker `make docker-run` uses `-it`, `--shm-size=2g`, and `CHROME_PATH`

### Changed

- Listing scrapes no longer wait for player `hlsUrl` (~15s); video pages still do
- Interrupted segment downloads keep `.segments` for true resume across runs
- Empty segment placeholders are re-downloaded instead of treated as complete
- Search queries are path-escaped (spaces and special characters)
- `get` skips an existing `<code>-*.mp4` unless `--force` is set
- Chrome honors `CHROME_PATH` when launching chromedp

### Fixed

- CLI errors print to stderr (exits were previously silent with `SilenceErrors`)

### Removed

- Rebuild planning docs (`knowledge.md`, `implementation.md`, `plan.md`)
  after v4.0.0 shipped; durable rules live in `AGENTS.md` and the README.

## [4.0.0] - 2026-08-29

### Changed

- **Full rebuild** of the previous `jabledownloader` CLI (versions up to
  v3.x) as `github.com/jooservices/go-jabledownloader` (Go 1.26). No
  backward compatibility with the old binary, its config, or its
  `video.mp4` output layout is kept.

### Added

- Layered architecture: `cmd/jabledownloader` + `internal/{app,config,format,hls,scraper,telemetry,ui,update}`
- HLS engine with master-playlist variant + codec resolution
- `<code>-<codec>.mp4` output naming (e.g. `start-166-h264.mp4`)
- Resume support: segments already on disk are skipped and reported
- Live progress UI: animated segment bar, ffmpeg progress, ETA and speed,
  newline snapshots for non-TTY output
- Optional OpenTelemetry export (logs, metrics, traces) to the JOOservices
  OpenObserve platform, fail-open and off by default
- CLI UX: `--force`, `--verbose`, `--quiet`, `--no-color`, grouped help,
  picker with selected counter, distinct exit codes (0 ok, 1 error,
  2 partial failure)
- Self-update from GitHub releases (assets
  `jabledownloader_vX.Y.Z_{goos}_{goarch}.tar.gz`)
- Docker build/test/CI tooling, golangci-lint standard, repository docs
  (`knowledge.md`, `implementation.md`, `plan.md`, README, AGENTS,
  CONTRIBUTING, SECURITY)

### Fixed

- Removed committed binaries, downloaded content and `.DS_Store` artifacts
- Removed `flickrdownloader` leftovers from the self-update package
- Removed dead scraper endpoints with no CLI command

[4.2.0]: https://github.com/jooservices/go-jabledownloader/releases/tag/v4.2.0
[4.1.0]: https://github.com/jooservices/go-jabledownloader/releases/tag/v4.1.0
[4.0.0]: https://github.com/jooservices/go-jabledownloader/releases/tag/v4.0.0
