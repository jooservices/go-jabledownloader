# go-jabledownloader — Plan

Execution increments for the rebuild. Each increment ends with a green
`make ci` and a PR into `develop`.

## Increment 1 — Skeleton (done in this workspace snapshot)

- `go.mod` (module `github.com/jooservices/go-jabledownloader`, Go 1.25)
- Dockerfile (build + runtime with ffmpeg/chromium), Makefile,
  `.golangci.yml`, CI workflow, `.gitignore`, `.env.example`
- Docs: `knowledge.md`, `implementation.md`, `plan.md`, `README.md`,
  `AGENTS.md`, `CHANGELOG.md`, `CONTRIBUTING.md`, `SECURITY.md`, `LICENSE`

## Increment 2 — Pure engine

- `internal/hls`: parser, resolver (variant + codec), estimate, concat,
  downloader with options pattern + progress callback
- `internal/format`, `internal/config`
- Tests: parser/resolver/estimate/config/format

## Increment 3 — Site adapter

- `internal/scraper`: `Fetcher` interface, chromedp browser, `Client`
  (`FetchVideoInfo`, `LatestVideos`, `HotVideos`, `SearchVideos`),
  `ResolveInput`
- Fixtures in `testdata/` + parse tests

## Increment 4 — Use-cases + CLI

- `internal/app`: `Service` (`RunGet`, `RunMulti`), context setup, plan,
  `FindExistingVideo`, `PlanError`
- `cmd/jabledownloader`: root + get/search/latest/hot/update/completion,
  exit codes 0/1/2
- `internal/ui`: writer/theme/progress/picker

## Increment 5 — Telemetry

- `internal/telemetry`: OTLP exporters, fail-open, noop when unset
- Wire spans/counters/histograms in `app`
- `.env.example` documents `OBS_*` variables
- Verify against the local `jooservices/openobserve` instance

## Increment 6 — Release readiness

- GitHub repo under `jooservices` (master + develop, branch protection)
- goreleaser or equivalent release assets `jabledownloader_vX.Y.Z_{goos}_{goarch}.tar.gz`
- Tag `v1.0.0` from `master`; CHANGELOG updated
