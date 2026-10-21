# Dorker

Dorker is a cross-platform search and passive reconnaissance CLI for authorized security research. It queries documented search APIs, enforces local scope, normalizes and deduplicates URLs, records provenance in SQLite, and produces reusable output.

Built by Silence.

## Key features

- SearXNG, Brave Search, Google Custom Search, and SerpAPI providers
- Bounded retries, provider cooldowns, health reporting, and resumable jobs
- Mandatory `site:` boundary enforcement, even when optional validation is off
- Persistent recursive discovery frontier with leases and safe resume
- Trustworthy/degraded discovery coverage and terminal-evidence reporting
- Local GHDB catalog, URL normalization, and provenance-aware deduplication
- TXT, JSONL, and CSV exports with `none`, `host`, and `domain` collapse modes
- Automatic output for interactive runs; data-only stdout for redirects and pipelines
- Pure-Go SQLite: CGO is not required

## Installation

Dorker requires Go 1.24 or newer. After the public `v1.0.0` tag is available:

```sh
go install github.com/HexSilence/dorker/cmd/dorker@v1.0.0
```

Go installs commands to `GOBIN`, or to `GOPATH/bin` when `GOBIN` is unset. That directory must be on your `PATH`; Go and Dorker do not modify shell configuration automatically. See [docs/installation.md](docs/installation.md).

## Quick start

```sh
dorker
dorker doctor
dorker setup
dorker search -q 'site:example.com'
dorker discover -q 'site:example.com'
```

Running `dorker` for the first time creates a minimal user config without overwriting an existing file, then shows a short onboarding screen. It does not install system packages or start containers.

## First-time setup

`dorker doctor` reports the version, OS/architecture, resolved config/database/output paths, optional container runtimes, and provider readiness without printing credentials. `dorker setup` safely creates a config if needed and explains provider choices.

For a local backend:

```sh
dorker setup searxng
dorker setup searxng --yes
```

The first command previews the container action; `--yes` explicitly approves it.

## Search examples

```sh
dorker search -q 'site:example.com'
dorker search -q 'site:example.com ext:pdf'
dorker search -f queries.txt
dorker search -q 'site:example.com' --collapse host
dorker search -q 'site:example.com' --format jsonl -o results.jsonl
cat queries.txt | dorker search
dorker search -q 'site:example.com' | httpx
```

Local validation never requests discovered URLs. A positive `site:` boundary is always enforced after normalization; `--validation off` disables optional checks, not scope protection.

## Discovery examples

```sh
dorker discover -q 'site:example.com'
dorker discover -q 'site:example.com' --max-depth 3 --max-hosts 5000
dorker discover -q 'site:example.com' --hosts-only | sort -u
dorker discover -q 'site:example.com' --roots-only > roots.txt
```

Discovery recursively searches accepted in-scope hosts. Its frontier, completed work, depth statistics, limits, provenance, and output-directory ownership persist for resume. Rate limits, challenges, timeouts, and unavailable sources do not count as saturation evidence. If the frontier empties while coverage remains degraded, the stop reason is `FRONTIER_EXHAUSTED_DEGRADED` and the run remains partial.

See [docs/discovery.md](docs/discovery.md) for coverage, historical degradation, terminal evidence, leases, resume, and artifact semantics.

## GHDB

```sh
dorker ghdb
dorker ghdb list
dorker ghdb show 5
dorker ghdb find login
dorker ghdb run 5 --domain example.com
```

GHDB is bundled in the binary; no source-tree files are needed at runtime. Searches use the same provider, scope, persistence, collapse, and export pipeline. See [docs/ghdb.md](docs/ghdb.md).

## Providers

| Provider | Configuration | Test command |
|---|---|---|
| SearXNG | instance URL; optional token environment variable | `dorker providers test searxng` |
| Brave Search | `BRAVE_API_KEY` | `dorker providers test brave` |
| Google Custom Search | `GOOGLE_API_KEY` and `GOOGLE_CSE_ID` | `dorker providers test google-api` |
| SerpAPI | `SERPAPI_API_KEY`; enable it in config | `dorker providers test serpapi` |

`dorker providers` is an offline status view. `providers test` makes one small explicit request. Details are in [docs/providers.md](docs/providers.md).

## SearXNG

Dorker can use an external SearXNG JSON API through `SEARXNG_URL` or configured instances. Guided local setup prefers Podman, falls back to Docker, binds only `127.0.0.1:8080`, generates a private settings secret, waits for valid JSON, and changes Dorker config only after readiness succeeds. Setup is idempotent: it reuses an existing compatible `dorker-searxng` container, starting it when stopped, without recreating it.

The image is digest-pinned. Setup will not install a runtime, use sudo/admin access, replace an unrelated container, or remount an existing compatible Dorker container.

## Output

Interactive searches automatically save non-empty output below the configured output root. Discovery saves `urls.txt`, `hosts.txt`, `roots.txt`, and `discovery.json`. Existing automatic files and discovery directories are not overwritten; resumed discovery reuses only its persisted owned directory.

Explicit `-o` wins. Redirected or piped searches emit data to stdout and do not create an extra automatic output file. Progress, summaries, and diagnostics go to stderr.

## Resume and jobs

```sh
dorker jobs
dorker resume <job-id>
dorker stats <job-id>
dorker export <job-id> --format jsonl -o results.jsonl
```

Job leases prevent two active processes from owning the same work. Search resume preserves provider progress; discovery resume additionally restores its frontier and output ownership.

## Configuration

```sh
dorker config
dorker config init
dorker config check
```

`DORKER_CONFIG` and `--config` override the default. Credentials remain in environment variables and are never written by bootstrap or printed by diagnostics. See [docs/configuration.md](docs/configuration.md) and [configs/config.example.yaml](configs/config.example.yaml).

## Supported platforms

| Platform | Architecture | v1.0.0 status |
|---|---|---|
| Linux | amd64, arm64 | supported |
| macOS | amd64, arm64 | supported |
| Windows | amd64 | supported |

CI is configured for native tests and binary/install smoke tests on Ubuntu, macOS, and Windows. Cross-builds cover all five target pairs. Windows arm64 is not claimed.

## Responsible and authorized use

Use Dorker only where you have authorization. Dorker does not solve CAPTCHAs, evade anti-bot controls, rotate proxies, or bypass provider limits. It honors retry and cooldown signals. See [SECURITY.md](SECURITY.md).

## Development and building

```sh
go mod download
go test ./...
go vet ./...
go test -race ./...
go build ./...
```

Source contributors can use `go install ./cmd/dorker`. Normal users do not need a clone. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Dorker is licensed under the [Apache License 2.0](LICENSE). The [NOTICE](NOTICE) preserves the original-project attribution:

> Dorker — Original project by HexSilence
>
> https://github.com/HexSilence/Dorker

Forks and modified distributions must follow the license and preserve applicable notices. Contributors may identify their own modifications; the original-project credit remains HexSilence. The license does not grant permission to use project names or marks to misrepresent a derivative as the original project.
