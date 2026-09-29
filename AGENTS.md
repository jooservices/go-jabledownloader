# go-jabledownloader

This file adds project-only rules.

- Module path: `github.com/jooservices/go-jabledownloader`; Go 1.26 baseline
- Layering (enforced by `internal/archtest`): `cmd` (composition root) →
  `internal/ui/cli` → `internal/app` (use-cases; UI only via `Reporter` /
  `Prompter`) → contracts in `internal/site`, `internal/engine`, `internal/media`
  → `internal/domain`. Concrete sites, engines, and translators register
  themselves and are blank-imported only in `cmd`
- Sites resolve only; downloading belongs to transport engines chosen by
  `Source.Kind` (`engine/hls`, `engine/progressive`). CDN headers travel on
  `Source.Headers`; never hard-code a site URL or referer in an engine
- Engines are pure: no `ui`, `config`, `telemetry`, `site`, or `media` imports;
  progress leaves via `domain.EventSink`. Prefer stdlib + `golang.org/x/sync`
- Sites depend on a `site.Fetcher`; no global browser. Jable uses one shared
  headless Chrome (tab per page); keep automation signals off
- `internal/media` is host-only (mlx_whisper + ffmpeg): audio → asr →
  translate → subtitle. Default mode soft, model `mlx-community/whisper-medium`,
  spoken language `ja`, target `en`. New translators register in
  `internal/media/translate` and must pass `contracttest.Run`
- Tests stay network-free and credentials-free. No unit test launches Chrome
  (browser tests use the `e2e` build tag and run via `make test-browser`). Site fixtures are real pages
  captured with `go test -tags fixtures ./internal/site/...` and recorded in
  `testdata/manifest.json`; never hand-write fixture HTML — derive negative
  cases from real pages in test code
- Output contract: `<out>/<path_template>/<code>-<codec>.mp4`, template default
  `{site}/{code}` (must contain `{code}`, stays inside `<out>`); the pre-v5
  `<out>/<code>/` layout still counts as downloaded. `--subtitle` writes
  `<code>-<codec>.<lang>.srt`. `get --json` keeps the v4.3 keys
- Exit codes: `0` success, `1` error, `2` partial batch or discovery failure
  (`PlanError`, `DiscoveryError`), `130` interrupted (Ctrl-C; re-run resumes)
- Subtitle embedding is host-only (Apple Silicon `mlx_whisper`); never required
  in Docker/CI. Agents must not install packages — document host install commands
  for the user instead
- Release assets: `jabledownloader_vX.Y.Z_{goos}_{goarch}.tar.gz` (built by
  `make release`); tags from `master`
- Telemetry is optional and fail-open: `OBS_*` env vars activate it; OBS being
  down must never break a download. Never attach titles, URLs, or local paths;
  credentials require https (or loopback)
- Prefer Docker for lint/CI (`tools/ci/docker-compose`); host Go is OK when it
  matches `go 1.26`. GitHub Actions runs on `ubuntu-latest`
- Branch model: `develop` for integration, `master` for production, tags from
  `master`
