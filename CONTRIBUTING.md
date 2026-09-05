# Contributing

Follow JOOservices identity, Conventional Commits, and the `master`/`develop`
branch model with required PRs and green CI. Project rules: [AGENTS.md](AGENTS.md).

## Setup

```bash
tools/install-git-hooks   # commit-msg / pre-commit / pre-push
```

Never use `--no-verify`. Host `gitleaks` is optional locally; CI still scans
with the pinned OSS image and `.gitleaks.toml`.

## Workflow

1. Branch from latest `develop`: `feature/<name>`, `fix/<name>`, …
2. Prefer Docker for lint/CI (`make ci` / `make docker-test`). Host Go is OK
   when it matches `go 1.26` (see [AGENTS.md](AGENTS.md)).
3. Open a PR into `develop` using the repository PR template.
4. Required CI must pass before merge.

## Quality gates

```bash
make ci    # gofmt check, go vet, golangci-lint, go test -race -cover
```

Prefer running `make ci` inside Docker so the toolchain matches CI. Rules from
[AGENTS.md](AGENTS.md) apply: pure `internal/hls`, fixture-based scraper tests,
justified dependencies, documented exported API.
