# GitHub Actions workflow flow

This document describes the workflows currently defined in
`.github/workflows/`. Jobs run on the self-hosted Linux X64 runner pool
(`runner1`); Go commands run through the repository Docker Compose setup
(`tools/ci/docker-compose`). The pull-request gate and the post-merge pass are
split into two workflows; branch protection on `master`/`develop` requires the
pull-request checks before merge.

Codecov and SonarQube are **not** provisioned for this repository yet.

## Overall event flow

```mermaid
flowchart TD
    native[GitHub Secret Scanning and Push Protection] --> Alerts[GitHub security alerts or blocked push]

    pr[PR to master or develop] --> CI[CI — full quality gate]
    pr --> CodeQL[CodeQL]
    pr --> Commitlint[Commitlint]
    pr --> Semantic[Semantic PR Title]
    pr --> PathLabel[PR Labeler]
    pr --> Audit{Changed files under .github?}
    Audit -->|yes| WorkflowAudit[Workflow audit]

    push[Push to master or develop] --> PostMerge[CI post-merge]
    push --> CodeQL
    push --> Audit

    master[Push to master] --> Scorecard[OpenSSF Scorecard]

    tag[Push tag v*.*.*] --> Release[Release]

    weekly[Weekly schedules] --> CodeQL
    weekly --> LinkCheck[Link check]
    weekly --> Scorecard
    weekly --> WorkflowAudit

    daily[Daily schedule] --> Stale[Stale]

    manual[workflow_dispatch] --> LinkCheck
    manual --> Scorecard
    manual --> Stale
    manual --> WorkflowAudit
```

## Pull-request gate (`ci.yml`)

**Trigger:** pull requests targeting `master` or `develop`.
Concurrent runs for the same pull request cancel older in-progress runs.

```mermaid
flowchart TD
    PR[Pull request] --> V[Validate]
    V --> L[Lint]
    L --> S[Security matrix x3 — fail-fast]
    L --> T[Test]
    S --> C[Coverage]
    T --> C
    C --> Gate[CI aggregation]

    S --- S1[Dependencies: govulncheck + OSV + Dependency Review]
    S --- S2[Secrets: Gitleaks OSS CLI in pinned Docker image]
    S --- S3[SAST: Semgrep OSS golang]
```

Every Go job builds the CI image, restores Go module caches under `.cache`
(keyed on `go.sum`), then runs its tool. The security matrix legs share the
job definition and select their tool via the matrix name. The leaf `CI` job
aggregates Validate → Lint → Security → Test → Coverage for a single
branch-protection context when needed.

## Post-merge pass (`ci-post-merge.yml`)

**Trigger:** pushes to `master` or `develop` (i.e., right after a merge).

```text
Validate → Test → Coverage
```

A light sanity pass only: linting and security scanning already gated the
pull request, so the post-merge run verifies the freshly created merge commit.

## Release flow (`release.yml`)

**Trigger:** push of a tag matching `v*.*.*`. Runs are not cancelled.

The workflow fails if the tag is not reachable from `origin/master`. Do not
tag until the maintainer approves the release. Artifacts are cross-compiled
archives under `dist/`.

## Other workflows

| Workflow | Trigger | Flow / result |
| --- | --- | --- |
| `codeql.yml` | Push/PR on `master` or `develop`; weekly | CodeQL for GitHub Actions YAML |
| `commitlint.yml` | PR opened/edited/synchronized/reopened | Every PR commit vs `.github/commitlint.config.mjs` |
| `semantic-pr.yml` | PR opened/edited/synchronized | PR title type + uppercase subject start |
| `pr-labeler.yml` | PR opened/synchronized/reopened | Labels from `.github/labeler.yml` |
| `link-check.yml` | Weekly; manual | Lychee Markdown link check |
| `scorecard.yml` | Push to `master`; weekly; manual | OpenSSF Scorecard → SARIF |
| `stale.yml` | Daily; manual | Stale issues/PRs |
| `workflow-audit.yml` | `.github/**` changes; weekly; manual | Actionlint + Zizmor |

## Required status checks

Branch protection on `master` and `develop` requires (exact names):

- `Validate`
- `Lint`
- `Security (Dependencies)`
- `Security (Secrets)`
- `Security (SAST)`
- `Test`
- `Coverage`
- `Validate commit messages`
- `Validate PR Title`

## Runtime truth

Documented runners and Docker paths must match the YAML. Prefer changing the
workflow files first, then update this document in the same PR.
