# Contributing

Follow the JOOservices workspace policy (`AGENTS.md` at the workspace root):
git identity, `gh` as `soulevilx`, `master`/`develop` branch model with
required PRs and green CI, Conventional Commits.

## Workflow

1. Branch from latest `develop`: `feature/<name>`, `fix/<name>`, …
2. Develop inside Docker only (`make docker-run`, `make ci`).
3. Open a PR into `develop` using the repository PR template.
4. Required CI must pass before merge.

## Quality gates (all inside Docker)

```bash
make ci    # gofmt check, go vet, golangci-lint, go test -race -cover
```

Rules from `implementation.md` apply: pure `internal/hls`, fixture-based
scraper tests, justified dependencies, documented exported API.
