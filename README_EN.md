<p align="center"><img src="images/logo.svg" width="120" alt="Tokens Statistic"></p>
<h1 align="center">Tokens Statistic</h1>
<p align="center"><a href="README.md">简体中文</a> | English</p>
<p align="center">
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/License-MIT-blue"></a>
  <a href="https://github.com/Pet-Max/cpa-plugin-tokens-statistic/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/Pet-Max/cpa-plugin-tokens-statistic?logo=github&label=Release"></a>
  <a href="https://go.dev"><img alt="Go" src="https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoSize=auto&label=Language"></a>
  <a href="https://github.com/Pet-Max/cpa-plugin-tokens-statistic/releases/latest"><img alt="Platform" src="https://img.shields.io/badge/Platform-Linux%20%7C%20MacOS%20%7C%20Windows-lightgrey"></a>
</p>

Tokens Statistic is a usage-statistics plugin for [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI). Once installed, the Management Center gains a "Tokens Statistic" page: every model call is recorded with its model, token counts, latency, outcome and cache hits, presented as trend charts and detail tables for any time range. All data stays in a local database — nothing is uploaded.

## Screenshots

**Dashboard overview**

![Dashboard overview: summary cards and the trend chart](images/dashboard-overview.png)

| Model details | Request details |
|:-:|:-:|
| ![Model details](images/dashboard-dimensions.png) | ![Request details](images/dashboard-requests.png) |

**Model pricing & sync**

![Model pricing and sync](images/full-mode-pricing.png)

## Features

- Per-call records: model, input / output / reasoning / cache tokens, latency, TTFT, average TPS, success rate and cache hits
- Multi-dimensional grouping: model, provider, executor, source, auth type, service tier, reasoning effort, failure status
- Summary cards, trend charts, dimension tables and per-request detail
- Time ranges: today, last 5 hours / 7 days / 30 days, current month, custom
- Trend aggregation from minute to month, wheel zoom and pan
- Table pagination, sorting and persisted column preferences
- Filter by multiple API keys (union), key labels
- Token unit toggle (full / K / M / B), persisted
- Estimated-cost ￥/$ toggle, persisted
- Follows the Management Center theme and browser language; Simplified Chinese, Traditional Chinese, English and Russian built in
- Single-file plugin for Linux, Mac and Windows

### Average TPS definition

Average TPS = all generated tokens / the total request latency reported by the host in seconds, including the wait and reasoning before the first token. OpenAI / Codex output tokens already include reasoning tokens, so they are not added again; protocols such as Gemini that report reasoning separately use output plus reasoning. TTFT is displayed separately and is not subtracted from the average TPS denominator.

For example, 539 output tokens (including 516 reasoning tokens) over 22.57 seconds gives about 23.88 average TPS. This measures overall request throughput, not the model's pure decoding speed. Accurate hidden-reasoning or text decoding rates require matching generation-stage timing from upstream. TPS is displayed as 0 when total latency is missing or zero.

Historical requests are recalculated with the same definition when queried, without a database migration. Request details and CSV exports use the same average TPS.

## Deployment

### Method 1 (recommended):

One-click install from the CPA plugin store inside the Management Center.

### Method 2:

1. Download the zip for your platform from [Releases](https://github.com/Pet-Max/cpa-plugin-tokens-statistic/releases) and extract the dynamic library.
2. Place the library under `plugins/<os>/<arch>/` in your CLIProxyAPI directory.
3. Configure it in CLIProxyAPI's config.yaml:

```yaml
plugins:
  enabled: true
  configs:
    tokens-statistic:
      enabled: true     # required: the host does not load plugins that are not explicitly enabled
      db: data/tokens-statistic.db   # database path (relative to the CLIProxyAPI working directory); default <CLIProxyAPI dir>/data/tokens-statistic.db
      retention: 365    # days to keep per-minute aggregates and request details (1–3650); default 365
      flush: 5s         # batch write interval (1s–1h). Recommended for small hosts (NAS / SD-card, write-sensitive devices); omit the line to commit per record (default)
      secret: "123456"  # API-key encryption key. 123456 is the default (local testing only); use a random value of at least 32 bytes in production
```

4. Field reference:

| Field | Default | Description |
|---|---|---|
| `db` | `<CLIProxyAPI dir>/data/tokens-statistic.db` | bbolt database path. Empty uses the default location; relative paths resolve against the CLIProxyAPI working directory |
| `retention` | `365` | Days to keep per-minute aggregates and request details (1–3650), cleaned up automatically |
| `flush` | empty | Empty commits every record immediately; set an interval for batch writes (1s–1h). 5s is recommended for small hosts or high-traffic setups; bursts are absorbed by the internal 100-record batch cap |
| `secret` | `123456` | API-key encryption and fingerprint key; custom values need at least 32 bytes. Must be changed for public deployments; empty disables API-key tracking |

## Build

There are two paths: **local builds** are for testing on your own machine, and **CI release builds** are for publishing.

| | Local build | CI release build |
|:--|:--|:--|
| Platforms | 4 (Windows, Linux × amd64/arm64) | 6 (adds macOS amd64/arm64) |
| Trigger | Run the scripts in `scripts/` manually | Automatically on pushing a `v*` tag |
| Where artifacts go | Stay in local `dist/`, never committed or uploaded | Attached to the GitHub Release automatically |
| Purpose | Local verification and real-machine smoke tests | Official releases for users to download |

### Local builds (4 platforms, for local testing)

Requirements: Go 1.26+, `CGO_ENABLED=1`; [zig](https://zig.dev) is the recommended cross compiler, MinGW-w64 works for Windows and aarch64-linux-gnu-gcc for Linux arm64.

| Platform | Command |
|---|---|
| Linux amd64 | `bash scripts/build-linux-amd64.sh` |
| Linux arm64 | `bash scripts/build-linux-arm64.sh` |
| Windows amd64 / arm64 | `powershell -ExecutionPolicy Bypass -File scripts/build_dll.ps1` |

Or call go build directly (Linux amd64 shown):

```bash
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
  CC="zig cc -target x86_64-linux-gnu" \
  go build -buildmode=c-shared -trimpath -buildvcs=false \
  -ldflags="-s -w -X github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin.version=v0.1.2" \
  -o tokens-statistic.so .
```

Artifacts:

- Raw dynamic libraries land in `dist/`;
- Store-conformant release packages (`tokens-statistic_<version>_<platform>.zip` + `checksums.txt`) land in `dist/release/v<version>/`, matching the assets CI publishes on releases;
- Override the version with `VERSION`, e.g. `VERSION=v0.2.0 bash scripts/build-linux-amd64.sh`.

Tests:

```bash
gofmt -w *.go
go vet ./...
go test -count=1 ./...
```

Optional browser regression tests need Node, Chrome/Edge and playwright-core (`npm ci`), plus `CHROME_PATH` pointing at the browser; they skip automatically when any of these is missing.

### CI release builds (6 platforms, auto-published on tag push)

Pushing a `v*` tag makes CI compile all six platforms on GitHub's build servers, generate checksums.txt and create the Release automatically.

## Privacy and Security

- No prompts, request bodies or response bodies are stored — only the metadata and counters needed for statistics
- API keys are stored encrypted with keyed fingerprints; plaintext is served on demand only through management routes that require the CLIProxyAPI management key, never embedded in static page content, logs or browser storage
- Dashboard data and every management operation are protected by the CLIProxyAPI management key: opening the dashboard inside the Management Center reuses the signed-in identity automatically, while standalone access asks for the management key once (kept only for the current tab, invalidated as soon as it closes)
- The default `secret` (123456) is for local testing only; use a random value of at least 32 bytes for public deployments. Rotating `secret` preserves historical ciphertext but it can no longer be revealed
- Leaving `secret` empty disables API-key tracking entirely

## Acknowledgements

[AITNR](https://github.com/AITNR/cap-token-usage-tracker)

## License

[MIT](LICENSE)
