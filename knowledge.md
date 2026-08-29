# go-jabledownloader — Knowledge Base

> Approved planning baseline for the rebuild of the archived `jabledownloader`
> CLI. Documentation snapshot: 2026-08-29. First release line: **v4.0.0**.

## 1. Product decision and verified evidence

- Repository: `go-jabledownloader`; module: `github.com/jooservices/go-jabledownloader`.
- Product: a CLI that downloads videos from Jable.TV (HLS). Not an SDK, not a
  service.
- Language baseline: Go 1.25 (workspace standard, see `go-flickr`).
- Version line: v4.0.0 is the first release of the rebuild. The archived
  binary line is superseded without a compatibility promise; release asset
  contract: `jabledownloader_vX.Y.Z_{goos}_{goarch}.tar.gz`.
- The previous `jabledownloader` CLI (versions up to v3.x, module
  `github.com/vietvu/jabledownloader`, Go 1.26.5) is the behavioral
  reference. Its HLS engine, chromedp scraper, picker and self-update logic
  work; the rebuild ports them into the workspace structure.
- Verified archive defects that the rebuild fixes:
  - Wrong module owner (`vietvu` → `jooservices`).
  - `flickrdownloader` copy-paste leftovers in `pkg/update`.
  - Committed 12 MB binaries, `videos/` content, `.DS_Store` files.
  - `pkg/scraper` mutable global `defaultBrowser`.
  - `pkg/hls` imported `pkg/ui` (layer violation).
  - Duplicated duration formatters and chromedp flag sets (DRY).
  - Dead code: `FetchNewReleaseVideos` / `FetchCategoryVideos` /
    `FetchModelVideos` had no CLI command (YAGNI — dropped).

## 2. Architecture decisions

| Decision | Choice | Reason |
| --- | --- | --- |
| Layout | `cmd/jabledownloader` + `internal/{app,config,format,hls,scraper,telemetry,ui,update}` | `internal/` keeps private API private (workspace convention); `cmd/` holds thin cobra wiring |
| Layering | cmd → app → {scraper, hls, ui}; hls is pure (no ui/config/telemetry imports) | DIP: engines depend on nothing renderable |
| Scraper | `Fetcher` interface; Chrome impl via chromedp; fixture impl in tests | Testable without Chrome; no globals |
| HLS progress | `hls.Event` callback (`ProgressFunc`) | hls stays pure; ui renders |
| Telemetry | optional OTLP/HTTP to JOOservices OpenObserve; fail-open; noop when `OBS_ENDPOINT` unset | ops insight without coupling; OBS down never blocks downloads |
| File naming | `<code>-<codec>.mp4` (e.g. `jur-827-h264.mp4`); codec from master playlist `CODECS` | predictable output names |
| CLI framework | cobra | de-facto standard; archive already used it |
| HTTP headers | `http.RoundTripper` middleware | one place for UA/Referer (DRY) |
| Update | GitHub releases, `jabledownloader_vX_{goos}_{goarch}.tar.gz` assets | proven archive flow, re-identified |

## 3. Observability contract

- Sink: `github.com/jooservices/openobserve` (OpenObserve, docker compose).
- Endpoint: `{OBS_ENDPOINT}/api/{OBS_ORG}`; stream per project via
  `stream-name` header; basic auth with the dedicated ingestion user.
- Signals: spans (`run.get`, `crawl.fetch_video_info`, `video.download`),
  counters (`crawl.request.total`, `hls.videos`, `run.videos`), histograms
  (`crawl.fetch_video_info.duration_ms`, `hls.video.duration_ms`), log events
  per phase. Per-segment successes are metrics only (volume control).

## 4. Non-goals

- No playlist editor, no multi-site support, no GUI.
- No telemetry SDK of our own; the standard OTel Go SDK is used directly.
