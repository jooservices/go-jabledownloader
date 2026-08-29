# go-jabledownloader — Implementation Contract

The design authority is `knowledge.md`. This file is the execution contract
for code in this repository.

## Structure

```text
cmd/jabledownloader/   thin cobra commands; no business logic
internal/app/          use-cases (Service), context setup, plan types
internal/config/       config file load/save + defaults
internal/format/       Bytes/Duration formatting (no deps)
internal/hls/          pure HLS engine: parser, resolver, downloader, concat
internal/scraper/      Jable.TV adapter: Fetcher interface, chromedp impl, goquery parsing
internal/telemetry/    optional OTLP → OpenObserve (fail-open, noop by default)
internal/update/       self-update from GitHub releases
internal/ui/           Writer abstraction, progress, picker, theme
```

## Rules

- `internal/hls` imports only stdlib + `golang.org/x/sync`; never `ui`,
  `config`, `telemetry`. Progress leaves hls via the `ProgressFunc` callback.
- `internal/scraper` depends on a `Fetcher` interface, never on a global
  browser instance.
- All exported API has godoc comments. Errors wrap with `%w`; sentinel
  errors defined where they originate.
- Dependencies are justified in `go.mod` ordering: direct first, then
  indirect. No dependency may be added without a reason in the PR.
- Tests are network-free and credentials-free. Scraping tests use
  `internal/scraper/testdata/*.html` fixtures. No test may launch Chrome.
- Output naming: `{code}-{codec}.mp4`; codec resolved from the master
  playlist `CODECS` attribute (h264 fallback).

## Commands (run inside Docker, never on the host)

```bash
make ci            # fmt-check + vet + lint + test
make test          # go test -race -cover ./...
make build         # binary into bin/
make docker-build  # runtime image
make docker-run    # run the image (ARGS="get jur-827")
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | success (including cancelled runs) |
| 1 | error |
| 2 | partial batch failure (some videos failed) |
