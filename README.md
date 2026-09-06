# jooservices/go-jabledownloader

[![CI](https://github.com/jooservices/go-jabledownloader/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/jooservices/go-jabledownloader/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/jooservices/go-jabledownloader/graph/badge.svg?token=KYCLSJVFPS)](https://codecov.io/gh/jooservices/go-jabledownloader)
[![Quality gate status](https://sonarcloud.io/api/project_badges/measure?project=jooservices_go-jabledownloader&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=jooservices_go-jabledownloader)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/jooservices/go-jabledownloader/badge)](https://securityscorecards.dev/viewer/?uri=github.com/jooservices/go-jabledownloader)
[![Go Version](https://img.shields.io/badge/Go-1.26-blue.svg)](https://go.dev/)
[![Release](https://img.shields.io/badge/version-4.2.0-blue.svg)](CHANGELOG.md)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Download videos from Jable.TV — a single-binary Go CLI with Cloudflare
bypass, parallel HLS segment downloads, an interactive picker, self-update,
and optional OpenTelemetry export to the JOOservices OpenObserve platform.

## About v4.0.0

This line is a complete rebuild of the previous `jabledownloader` CLI
(versions up to v3.x): new module path
`github.com/jooservices/go-jabledownloader`, new architecture, new output
layout.

> [!WARNING]
> **`v4.0.0` is a complete rebuild and is NOT backward compatible.**
> Archived config files and `video.mp4` outputs are ignored, and self-update
> talks to `jooservices/go-jabledownloader` releases. See the
> [changelog](CHANGELOG.md).

## Highlights vs the previous line

- Layered `internal/` architecture with a pure HLS engine (no UI/config
  dependencies in the download core)
- Output naming `<code>-<codec>.mp4` (e.g. `start-166-h264.mp4`); codec
  resolved from the master playlist
- Download progress UI: multi-line segment bar with size / left, current + avg
  speed, ETA; ffmpeg progress; resume reporting; newline snapshots when piped
- Optional English soft/hard subtitles via host `mlx_whisper` (`--subtitle`)
- Optional, fail-open OpenTelemetry export to the JOOservices OpenObserve
  platform (`OBS_*` env vars, off by default)
- CLI UX: `--force`, `--verbose`, `--quiet`, `--no-color`, grouped help,
  picker with selection counter, exit codes 0/1/2
- Repository hygiene: no committed binaries or downloads; Docker-based
  dev/test/CI; golangci-lint standard

## Features

- `get` a single video by URL or code (e.g. `jur-827`); skips existing files
  unless `--force`
- `search`, `latest`, `hot` with an interactive multi-select picker and `--count`
- Parallel segment downloading with retry/backoff, cross-run resume, ffmpeg concat
- `--quality` to cap height (`best`, `360`, `480`, `720`, `1080`)
- `--subtitle` — English subtitles via host `mlx_whisper` (`--task translate`)
- `--subtitle-mode soft|hard` — soft = separate track + `.en.srt`; hard = burn-in
- `--dry-run` preview with size estimates
- `config` to persist `output_dir` / `worker_count`
- Self-update from GitHub releases

## Requirements

- ffmpeg (runtime, for concat/fallback; also audio extract + subtitle embed when `--subtitle`)
- Chrome/Chromium (scraping — bypasses Cloudflare)
- **Optional (host, `--subtitle` only):** [`mlx-whisper`](https://pypi.org/project/mlx-whisper/) on PATH
  (Apple Silicon). Install yourself — agents must not install packages:
  ```bash
  uv tool install mlx-whisper
  # or: pipx install mlx-whisper
  ```
  Default model: `mlx-community/whisper-medium` (override with `--whisper-model`).
  Do not use `whisper-large-v3-turbo` for English translate — on MLX it often
  keeps Japanese. `--subtitle-mode hard` also needs an ffmpeg build with **libass** (`subtitles`
  filter). Confirm with `ffmpeg -filters | grep subtitles`. Soft mode works
  without libass.
- Go 1.26 only when building from source; prebuilt archives need none
- Docker is preferred for lint/CI; host Go is OK when it matches `go 1.26`.
  Running the released binary does not require Docker. **Subtitle generation is a
  host feature** (mlx_whisper / Metal) — do not rely on the project Docker image for it.

## Quick start

```bash
# Prebuilt release archive — no Docker needed:
tar -xzf jabledownloader_v4.2.0_darwin_arm64.tar.gz
sudo mv jabledownloader /usr/local/bin/

jabledownloader get jur-827
jabledownloader get https://en.jable.tv/videos/abf-382/ --subtitle
jabledownloader get abf-382 --subtitle --subtitle-mode hard
jabledownloader search cute --dry-run
jabledownloader latest --count 5
```

English subtitles (`--subtitle`) run on the **host** after the MP4 is ready:
`ffmpeg` extracts audio → `mlx_whisper --task translate` (default model
`mlx-community/whisper-medium`, spoken language `ja`) → `.en.srt`, then either
**soft** mux (`mov_text`, language `eng`; default) or **hard** burn-in (pixels;
needs ffmpeg with libass).

## Optional: cover, screenshots & AI catalog copy (host)

This is **not** part of the `jabledownloader` binary. After you already have
`<code>-<codec>.mp4` and (optionally) `<code>-<codec>.en.srt`, you can build a
cover, multi-frame screenshots, and bilingual story / description with
**ffmpeg** + any vision-capable AI CLI (example: [OpenCode](https://opencode.ai)
`opencode run`).

Example layout under the video folder:

```text
videos/<code>/
  <code>-h264.mp4
  <code>-h264.en.srt
  _meta/
    cover.jpg
    screenshots/shot-01.jpg … shot-N.jpg
    dialogue.en.txt
    metadata.md
```

### 1. Cover + screenshots (ffmpeg)

```bash
DIR=videos/abf-382
MP4=$DIR/abf-382-h264.mp4
OUT=$DIR/_meta
mkdir -p "$OUT/screenshots"

DUR=$(ffprobe -v error -show_entries format=duration \
  -of default=noprint_wrappers=1:nokey=1 "$MP4")

# Cover ~12% in (often avoids a black intro)
ffmpeg -y -ss "$(python3 -c "print(f'{float('$DUR')*0.12:.2f}')")" \
  -i "$MP4" -frames:v 1 -q:v 2 "$OUT/cover.jpg"

# Evenly spaced frames (adjust count / percents as you like)
python3 - <<PY
import subprocess
dur = float("$DUR")
mp4, out = "$MP4", "$OUT/screenshots"
for i, p in enumerate((0.08, 0.16, 0.24, 0.32, 0.40, 0.48, 0.56, 0.64, 0.72, 0.80, 0.88), 1):
    t = dur * p
    subprocess.run(
        ["ffmpeg", "-y", "-ss", f"{t:.2f}", "-i", mp4, "-frames:v", "1", "-q:v", "2",
         f"{out}/shot-{i:02d}.jpg"],
        check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    print(f"shot-{i:02d} @{t:.1f}s")
PY
```

### 2. Dialogue text from `.en.srt`

Strip indexes and timestamps so the model gets spoken lines only:

```bash
python3 - <<'PY'
from pathlib import Path
import re
srt = Path("videos/abf-382/abf-382-h264.en.srt").read_text(encoding="utf-8")
lines = []
for block in re.split(r"\n\s*\n", srt.strip()):
    body = []
    for line in block.splitlines():
        s = line.strip()
        if not s or re.match(r"^\d+$", s) or "-->" in s:
            continue
        body.append(s)
    if body:
        lines.append(" ".join(body))
# drop consecutive duplicates
out = []
for line in lines:
    if not out or out[-1] != line:
        out.append(line)
Path("videos/abf-382/_meta/dialogue.en.txt").write_text("\n".join(out), encoding="utf-8")
print(len(out), "lines")
PY
```

### 3. Story + description via OpenCode (vision)

Use a **vision** model. Some providers cap images per prompt (e.g. DeepSeek
vision: **at most 4 images**) — attach cover + a few key shots, not the full set.

```bash
cd videos/abf-382/_meta

opencode run --auto \
  -m opencode-go/deepseek-v4-flash-vision-exp \
  -f cover.jpg \
  -f screenshots/shot-03.jpg \
  -f screenshots/shot-07.jpg \
  -f screenshots/shot-11.jpg \
  -f dialogue.en.txt \
  --dir . \
  'Write metadata.md with: Description (English), Description (Vietnamese),
Story (English), Story (Vietnamese), and Tags. Base it on the attached images
and dialogue.en.txt. Professional catalog copy; bilingual EN/VI.'
```

Output: `_meta/metadata.md`. Swap the model / CLI if you prefer another local
or hosted agent — the binary is only needed for download (and optional
`--subtitle`).

## CLI / commands

| Command | Purpose |
| --- | --- |
| `jabledownloader get <url\|code>` | Download a single video |
| `jabledownloader search <query>` | Search and download (`--count`) |
| `jabledownloader latest` | Download the latest videos (`--count`) |
| `jabledownloader hot` | Download the trending videos (`--count`) |
| `jabledownloader update` | Self-update from GitHub releases (`--check`) |
| `jabledownloader config` | Show or set persisted settings |
| `jabledownloader completion <shell>` | Shell completion for bash/zsh/fish/powershell |

## Typical workflow

1. `jabledownloader search cute` — pick videos in the interactive list
2. Review the plan (durations + size estimates), confirm
3. Progress bars show segments/ETA/speed; interrupted runs **resume** on the
   next invocation
4. Output lands in `./videos/<code>/<code>-<codec>.mp4`
5. Batch failures exit with code `2`; `--force` re-downloads existing files

## Docker commands

Optional — for running the image instead of a host binary, and mandatory
for development:

```bash
make build          # host binary into bin/
make docker-build   # jooservices/go-jabledownloader:latest
make docker-run     # ARGS="get jur-827" (−it, shm-size, CHROME_PATH)
make release        # cross-compiled archives into dist/
make ci             # fmt + vet + lint + test (container)
```

## Configuration

Persisted settings live in `~/.config/jabledownloader/config.json`
(`jabledownloader config` / `config set`).

| Env var | Purpose |
| --- | --- |
| `CHROME_PATH` | Chromium/Chrome binary for scraping (Docker sets `/usr/bin/chromium`) |
| `OBS_ENDPOINT` | OpenObserve URL; unset = telemetry disabled (default) |
| `OBS_ORG` | OBS organization (default `jooservices`) |
| `OBS_STREAM` | OBS stream (default `jabledownloader`) |
| `OBS_USER` | OBS ingestion user email |
| `OBS_PASSWORD` | OBS ingestion user password |

Env names only — values never live in this repository.

## Observability (optional)

Telemetry is off by default and not bundled: the CLI works fully without
any OBS component. OBS is a separate project — `jooservices/openobserve` —
that exposes a docker-compose OpenObserve instance.

1. **Start OBS** (see its README): `make up && make status`, then
   `make smoke` to bootstrap the org + stream
2. **Create an ingestion user** in the `jooservices` org (IAM → Users) —
   never use root credentials
3. **Export the `OBS_*` variables** from the table above, then run any
   command
4. **Inspect** at `http://localhost:5080` (switch org to `jooservices`):
   stream `jabledownloader` for logs/traces; metric streams
   `crawl_request_total`, `hls.videos`, duration histograms

Fail-open: an unreachable OBS never affects downloads.

## Design contract

Project rules live in [AGENTS.md](AGENTS.md). Key invariants: layered
`internal/` packages, pure HLS engine, fixture-driven scraper tests,
`<code>-<codec>.mp4` output naming, exit codes `0`/`1`/`2`, and release
assets `jabledownloader_vX.Y.Z_{goos}_{goarch}.tar.gz`.

## Documentation

- [CHANGELOG.md](CHANGELOG.md)
- [CONTRIBUTING.md](CONTRIBUTING.md)
- [SECURITY.md](SECURITY.md)
- [SUPPORT.md](SUPPORT.md) / [GOVERNANCE.md](GOVERNANCE.md) /
  [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- [WORKFLOWS.md](WORKFLOWS.md)
- [AGENTS.md](AGENTS.md)

## Development

Prefer Docker so the toolchain matches CI; host Go is OK when it matches
`go 1.26`:

```bash
tools/install-git-hooks   # once after clone
make ci                   # fmt + vet + lint + test
make cover                # coverage report
```

## Branch model & CI

`master`/`develop`, PR required, CI green before merge. Workflows:
`.github/workflows/` — see [WORKFLOWS.md](WORKFLOWS.md).

**CI secrets (organization level):** `CODECOV_TOKEN` and `SONAR_TOKEN` live
under [jooservices organization secrets](https://github.com/organizations/jooservices/settings/secrets/actions)
— not per-repo. `SONAR_HOST_URL` is optional and defaults to
`https://sonarcloud.io`. Grant this repository access when onboarding.

## Community

- Issues: https://github.com/jooservices/go-jabledownloader/issues
- Security: [SECURITY.md](SECURITY.md)
- Support: [SUPPORT.md](SUPPORT.md)
- Contributing: [CONTRIBUTING.md](CONTRIBUTING.md)

## License

[MIT](LICENSE)
