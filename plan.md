# go-jabledownloader — Plan

Execution increments for the rebuild. Each increment ends with a green
`make ci` and a PR into `develop`.

## Increment 1 — Skeleton (done)

- `go.mod` (module `github.com/jooservices/go-jabledownloader`, Go 1.25)
- Dockerfile (build + runtime with ffmpeg/chromium), Makefile,
  `.golangci.yml`, CI workflow, `.gitignore`, `.env.example`
- Docs: `knowledge.md`, `implementation.md`, `plan.md`, `README.md`,
  `AGENTS.md`, `CHANGELOG.md`, `CONTRIBUTING.md`, `SECURITY.md`, `LICENSE`

## Increment 2 — Pure engine (done)

- `internal/hls`: parser, resolver (variant + codec), estimate, concat,
  downloader with options pattern + progress callback
- `internal/format`, `internal/config`
- Tests: parser/resolver/estimate/config/format

## Increment 3 — Site adapter (done)

- `internal/scraper`: `Fetcher` interface, chromedp browser, `Client`
  (`FetchVideoInfo`, `LatestVideos`, `HotVideos`, `SearchVideos`),
  `ResolveInput`
- Fixtures in `testdata/` + parse tests

## Increment 4 — Use-cases + CLI (done)

- `internal/app`: `Service` (`RunGet`, `RunMulti`), context setup, plan,
  `FindExistingVideo`, `PlanError`, progress display
- `cmd/jabledownloader`: root + get/search/latest/hot/update/completion,
  exit codes 0/1/2, `--force`/`--verbose`/`--quiet`
- `internal/ui`: writer/theme/progress/picker (counter, resume notice)

## Increment 5 — Telemetry (done)

- `internal/telemetry`: OTLP exporters, fail-open, noop when unset
- Spans/counters/histograms wired in `app`
- Verified against the local `jooservices/openobserve` instance

## Increment 6 — Release v4.0.0 (in progress)

- GitHub repo under `jooservices` (master + develop, branch protection)
- `make release` cross-compiles the six platform archives consumed by the
  `update` command
- Tag `v4.0.0` from `master`; CHANGELOG and README updated
