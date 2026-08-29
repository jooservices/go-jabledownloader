# go-jabledownloader agent rules

This repository is part of the JOOservices workspace. The canonical policy is
the workspace root `AGENTS.md` at `/Users/vietvu/Sites/JOOservices/AGENTS.md`.

Project-specific rules:

- Module path: `github.com/jooservices/go-jabledownloader`; Go 1.26 baseline.
- `knowledge.md` is the design authority; `implementation.md` is the execution
  contract; `plan.md` sequences the increments.
- Layering: `cmd` → `internal/app` → `{scraper,hls,ui}`. `internal/hls` is a
  pure engine — never import `ui`, `config`, or `telemetry` there.
- Tests stay network-free and credentials-free; scraper tests use
  `internal/scraper/testdata/` fixtures. No test launches Chrome.
- Output naming contract: `<code>-<codec>.mp4`.
- Telemetry is optional and fail-open: `OBS_*` env vars activate it; OBS being
  down must never break a download.
- Never weaken identity, GitHub-account (`soulevilx`), branch-model
  (`master`/`develop`, PR required) or quality rules.
