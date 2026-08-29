# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [4.0.0] - 2026-08-29

### Changed

- **Full rebuild** of the previous `jabledownloader` CLI (versions up to
  v3.x) as `github.com/jooservices/go-jabledownloader` (Go 1.25). No
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

[4.0.0]: https://github.com/jooservices/go-jabledownloader/releases/tag/v4.0.0
