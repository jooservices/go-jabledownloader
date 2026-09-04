# Governance

## Project model

`jooservices/go-jabledownloader` is an owner-driven project (benevolent-leader
style). JOOservices maintains the CLI; the project owner holds final decision
authority.

## Roles

| Role | Holder | Responsibility |
| --- | --- | --- |
| Owner / lead maintainer | Viet Vu (JOOservices) | Roadmap, architecture decisions, release approval, access control, final arbitration |
| Maintainers | Appointed by the owner | Review PRs, keep CI green, uphold the quality gates |
| Contributors | Everyone else | Propose changes via issues and PRs following [CONTRIBUTING.md](CONTRIBUTING.md) |

## Decision making

- Day-to-day changes merge through PR review against [CONTRIBUTING.md](CONTRIBUTING.md)
- API / CLI design changes and scope additions are decided by the owner after
  discussion in an issue or PR
- **Releases require explicit owner approval** — no tag or GitHub Release
  without it
- Breaking changes are acceptable only in major versions; this line starts at
  `v4.0.0` as a full rebuild with no backward compatibility with v3.x

## Quality authority

The repository quality gates (gofmt, go vet, golangci-lint, race tests,
coverage floor, required Docker CI chain) may only be relaxed by the owner.

## Conduct enforcement

Code of Conduct reports go to [admin@jooservices.com](mailto:admin@jooservices.com)
and are handled by the maintainers per the [Code of Conduct](CODE_OF_CONDUCT.md).

## Changes to this document

Amendments require owner approval via PR.
