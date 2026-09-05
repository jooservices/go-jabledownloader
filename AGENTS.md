# go-jabledownloader

This file adds project-only rules.

- Module path: `github.com/jooservices/go-jabledownloader`; Go 1.26 baseline
- Layering: `cmd` → `internal/app` → `{scraper,hls,ui,subtitle}`. `internal/hls`
  is a pure engine — never import `ui`, `config`, `telemetry`, or `subtitle`
  there; progress leaves via `ProgressFunc`. Prefer stdlib + `golang.org/x/sync`
  only in `hls`
- `internal/scraper` depends on a `Fetcher` interface; no global browser
- `internal/subtitle` is host-only (mlx_whisper + ffmpeg); default mode soft,
  default model `mlx-community/whisper-medium`, default spoken language `ja`
- Tests stay network-free and credentials-free; scraper tests use
  `internal/scraper/testdata/` fixtures. No test launches Chrome
- Output naming contract: `<code>-<codec>.mp4` (codec from master playlist
  `CODECS`, h264 fallback). Optional `--subtitle` also writes
  `<code>-<codec>.en.srt` and embeds English subs (`--subtitle-mode soft|hard`)
- Exit codes: `0` success, `1` error, `2` partial batch failure (`PlanError`)
- Subtitle embedding is host-only (Apple Silicon `mlx_whisper`); never required
  in Docker/CI. Agents must not install packages — document host install commands
  for the user instead
- Release assets: `jabledownloader_vX.Y.Z_{goos}_{goarch}.tar.gz` (built by
  `make release`); tags from `master`
- Telemetry is optional and fail-open: `OBS_*` env vars activate it; OBS being
  down must never break a download
- Prefer Docker for lint/CI (`tools/ci/docker-compose`); host Go is OK when it
  matches `go 1.26`. GitHub Actions runs on `ubuntu-latest`
- Branch model: `develop` for integration, `master` for production, tags from
  `master`
