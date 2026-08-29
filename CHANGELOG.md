# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Rebuild of the archived `jabledownloader` CLI as
  `github.com/jooservices/go-jabledownloader` (Go 1.25)
- Layered structure: `cmd/jabledownloader` + `internal/{app,config,format,hls,scraper,telemetry,ui,update}`
- HLS engine with master-playlist codec resolution and `<code>-<codec>.mp4` output naming
- Optional OTLP logs/metrics/traces export to the JOOservices OpenObserve instance
- Docker-based build/test/CI tooling and repository docs

[Unreleased]: https://github.com/jooservices/go-jabledownloader/compare/develop...HEAD
