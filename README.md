# Dorker

[![Latest Release](https://img.shields.io/github/v/release/HexSilence/Dorker?display_name=tag)](https://github.com/HexSilence/Dorker/releases/latest)
[![Test](https://github.com/HexSilence/Dorker/actions/workflows/test.yml/badge.svg)](https://github.com/HexSilence/Dorker/actions/workflows/test.yml)
[![Build](https://github.com/HexSilence/Dorker/actions/workflows/build.yml/badge.svg)](https://github.com/HexSilence/Dorker/actions/workflows/build.yml)
[![Go 1.24+](https://img.shields.io/badge/Go-1.24%2B-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/github/license/HexSilence/Dorker)](LICENSE)

Dorker is a cross-platform Go CLI for structured multi-provider search, Google-dork-style query workflows, result validation, persistent collection, and bounded host discovery. It is designed for authorized security research and other legitimate public-web research.

Built by **Silence**. Original-project attribution: **HexSilence**.

**Latest stable: v1.0.1**

## Features

- Aggregates Google Custom Search JSON API, Brave Search API, SerpAPI's Google engine, and self-hosted or user-configured SearXNG.
- Plans exact or conservative Google-dork-style queries while retaining provider provenance.
- Enforces positive and negative `site:` scope locally after URL normalization, preventing out-of-scope results from reaching persistence or exports.
- Locally evaluates `site:`, `-site:`, `ext:`, `filetype:`, `-ext:`, `-filetype:`, `inurl:`, and `-inurl:` where conclusive.
- Normalizes URLs, removes common tracking parameters, deduplicates results, and stores canonical data in pure-Go SQLite.
- Preserves provider, instance, source-engine, query-variant, discovery-edge, and repeated-source provenance.
- Supports checkpoints, resumable searches, persistent discovery frontiers, crash recovery, and job leases.
- Honors provider rate limits, `Retry-After`, exponential backoff with jitter, cooldowns, health state, and bounded adaptive scheduling.
- Exports TXT, JSONL, and CSV; supports host/domain collapse and clean redirected stdout.
- Includes a local structured GHDB catalog, provider diagnostics, `doctor`, safe first-run configuration, and managed SearXNG setup.
- Recursively discovers in-scope hosts with hard limits, public-suffix-aware roots, per-depth gain, degradation-aware saturation, and deterministic artifacts.
- Provides the first built-in `dorker update` workflow and cached awareness of future stable releases.
- Supports Linux amd64/arm64, macOS amd64/arm64, and Windows amd64.

## Installation

Dorker requires Go 1.24 or newer. Pin the current stable release for a reproducible installation:

```sh
go install github.com/HexSilence/dorker/cmd/dorker@v1.0.1
```

Or ask Go for the latest published module version:

```sh
go install github.com/HexSilence/dorker/cmd/dorker@latest
```

`@v1.0.1` always selects this exact release. `@latest` selects the newest semantic module version visible to Go. The executable is installed to `GOBIN`, or `GOPATH/bin` when `GOBIN` is unset; that directory must be on `PATH`.

Platform-specific installation details are in [docs/installation.md](docs/installation.md).

## Quick start

```sh
dorker
dorker setup
dorker doctor
dorker providers
dorker search -q 'site:example.com'
dorker search -q 'site:example.com filetype:pdf'
dorker discover -q 'site:example.com'
dorker ghdb
dorker update
```

The first plain invocation atomically creates a default user config if none exists and displays onboarding. It does not install packages or start containers.

## Updates

```sh
dorker version
dorker update
```

`dorker update` performs one workflow: it retrieves the latest published stable Dorker release, compares versions, and reports when the installation is current. When a newer release exists, Dorker requests interactive confirmation before changing a recognized Go installation.

For an unknown installation method—or on Windows, where replacing a running executable is unsafe—Dorker prints an exact validated `go install ...@vVERSION` command instead of guessing or overwriting the executable. Dorker does not perform unchecked release-binary replacement.

Starting with v1.0.1, interactive commands may display one concise future-release notice. Passive metadata is cached for approximately 24 hours and failures never affect normal commands. Disable passive checks with:

```yaml
update_check:
  enabled: false
```

or `DORKER_NO_UPDATE_CHECK=1`. This does not disable an explicit `dorker update` request.

The immutable v1.0.0 release predates built-in update awareness. Existing v1.0.0 users can install v1.0.1 with the pinned command in [Installation](#installation).

## SearXNG

Preview managed local setup, then explicitly apply it:

```sh
dorker setup searxng
dorker setup searxng --yes
```

Setup prefers Podman and falls back to Docker. It creates or starts the digest-pinned `dorker-searxng` container, binds only `http://127.0.0.1:8080`, waits for a valid JSON response, and updates Dorker's config only after readiness succeeds. Compatible legacy containers are reused safely; unrelated or incompatible containers are not replaced. Repeated setup does not duplicate the local instance and preserves user-managed instances.

An existing SearXNG service can instead be supplied through `SEARXNG_URL` or configured instances. Dorker respects upstream rate limits and restrictions; it does not provide CAPTCHA or restriction bypasses.

## Providers

| Provider | Configuration | Diagnostic command |
|---|---|---|
| SearXNG | `SEARXNG_URL` or configured instances; optional token environment variable | `dorker providers test searxng` |
| Brave Search API | `BRAVE_API_KEY` | `dorker providers test brave` |
| Google Custom Search JSON API | `GOOGLE_API_KEY`, `GOOGLE_CSE_ID` | `dorker providers test google-api` |
| SerpAPI Google engine | `SERPAPI_API_KEY`; enable in config | `dorker providers test serpapi` |

The mock provider is reserved for deterministic development and testing. API-backed providers require credentials from the user; Dorker does not bundle or print credentials.

## Queries and validation

```sh
dorker search -q 'site:example.com'
dorker search -q 'site:example.com ext:pdf'
dorker query explain 'site:example.com -site:dev.example.com filetype:pdf'
dorker query validate -q 'site:example.com ext:pdf' -u 'https://docs.example.com/report.pdf'
```

Hostname matching is DNS-label aware. A result under `example.com.attacker.test`, or merely containing `example.com` in its path/query, does not satisfy `site:example.com`. Mandatory positive-site scope remains fail-closed in every validation mode. `off`, `conservative`, and `strict` control optional locally enforceable checks; they do not weaken site scope. Provider-only operators remain provider-side rather than being falsely rejected locally.

Search limits count normalized unique URLs: `--max-results` is job-wide and `--max-results-per-query` applies to each original query. Rejected, malformed, and duplicate provider entries do not consume those budgets; `0` means unlimited. `--max-pages` is a per-provider/query ceiling, but explicit provider exhaustion or evidence-based saturation can stop earlier. Common tracking-only parameters, including `utm_*`, `gclid`, `fbclid`, and `srsltid`, are removed during normalization unless `--keep-tracking-params` is selected.

## Discovery

```text
seed query -> validated URLs -> normalized hosts -> bounded frontier
           -> host expansion -> validation/deduplication/provenance
           -> per-depth gain -> saturation or hard limit
```

```sh
dorker discover -q 'site:example.com'
dorker discover -q 'site:example.com' --max-depth 3
dorker discover -q 'site:example.com' --max-depth 3 --max-hosts 5000
dorker discover -q 'site:example.com' --hosts-only | sort -u
dorker resume <job-id>
```

Important limits include `--max-depth`, `--max-hosts`, `--max-queries`, `--max-results`, `--max-results-per-query`, `--max-pages`, and `--max-duration`. Saturation requires trustworthy provider coverage; degraded or failed providers are not mistaken for evidence of exhaustion. See [docs/discovery.md](docs/discovery.md).

## GHDB

Dorker contains a local structured catalog; it does not fetch an external GHDB at runtime.

```sh
dorker ghdb
dorker ghdb list
dorker ghdb show 5
dorker ghdb find login
dorker ghdb run 5 --domain example.com
```

GHDB execution uses the normal provider, validation, persistence, and export pipeline. See [docs/ghdb.md](docs/ghdb.md).

## Output and persistence

Interactive non-empty searches save beneath the configured output root (normally `~/dorker-output`). Redirected or piped stdout contains result data only; progress and diagnostics stay on stderr and no extra automatic result file is created. Explicit `-o` wins.

Search supports TXT, JSONL, CSV, and `none`, `host`, or `domain` collapse. Discovery writes `urls.txt`, `hosts.txt`, `roots.txt`, and `discovery.json`. SQLite remains authoritative and retains full canonical URL and provenance data even when presentation output is collapsed.

## Configuration

Default config locations:

| Platform | Configuration |
|---|---|
| Linux | `$XDG_CONFIG_HOME/dorker/config.yaml`, or `~/.config/dorker/config.yaml` |
| macOS | `~/Library/Application Support/dorker/config.yaml` |
| Windows | `%AppData%\dorker\config.yaml` |

`DORKER_CONFIG` or command-level `--config` selects another file. Data uses the OS-appropriate application data directory; results default to `~/dorker-output`. Credentials belong in documented environment variables, not YAML. See [docs/configuration.md](docs/configuration.md) and [configs/config.example.yaml](configs/config.example.yaml).

## Supported platforms

| Platform | Architecture | Validation |
|---|---|---|
| Linux | amd64, arm64 | cross-build; amd64 CI runtime and race tests |
| macOS | amd64, arm64 | cross-build; native GitHub-hosted runner |
| Windows | amd64 | cross-build; native GitHub-hosted runner |

Cross-compilation is not presented as runtime testing. Windows arm64 is not currently claimed.

## Versioning

Current stable: **v1.0.1**.

Use `dorker version` for local build metadata and `dorker update` for the latest stable release workflow. v1.0.0 remains an immutable historical release and module version; v1.0.1 contains the subsequent stabilization and update work.

## Responsible use

Use Dorker only for targets and data you are authorized to research. It searches public provider indexes and does not perform exploitation, CAPTCHA solving, proxy rotation, or provider-control evasion. See [SECURITY.md](SECURITY.md).

## Development

```sh
go mod verify
go test ./...
go vet ./...
go test -race ./...
go build ./cmd/dorker
```

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Dorker is licensed under the [Apache License 2.0](LICENSE). [NOTICE](NOTICE) preserves HexSilence's legal/original-project attribution.
