# Dorker

Advanced search and reconnaissance engine for security researchers and bug bounty hunters.

Built by Silence && Fro7h.

```text
D O R K E R
Advanced Search Intelligence
& Reconnaissance Engine

Silence && Fro7h
```

Dorker is a cross-platform Go CLI that collects results from documented search APIs. It validates and normalizes URLs, removes duplicates, stores provenance in SQLite, and exports clean data for later analysis.

## Features

- SearXNG, Brave Search, Google Custom Search, and SerpAPI providers
- Guided local SearXNG setup with Podman or Docker
- Interactive local GHDB catalog
- Query-aware result validation
- Canonical URL deduplication and persisted checkpoints
- Host and registrable-domain collapsing
- Automatic interactive result saving
- TXT, JSONL, and CSV output
- Provider health, cooldown, bounded retry, and resumable jobs
- Pure-Go SQLite and cross-platform builds

## Installation

Dorker requires Go 1.24 or newer. The repository is currently private, so installation requires GitHub access and authenticated Git. See [Private repository access](#private-repository-access).

### Windows

In PowerShell, install Go with the [official installer](https://go.dev/dl/) or:

```powershell
winget install --id GoLang.Go -e
go version
go install github.com/HexSilence/dorker/cmd/dorker@latest
```

Go normally installs the binary in `%USERPROFILE%\go\bin`. Use `go env GOPATH` and `go env GOBIN` to confirm. If `dorker` is not found, add that bin directory to your user `Path` through Windows Environment Variables, then open a new PowerShell window.

Docker Desktop or Podman Desktop is optional and only needed for local SearXNG. Container CLI behavior varies with the selected desktop runtime; verify that `podman` or `docker` works in PowerShell before running `dorker setup searxng`.

### macOS

Install Go from [go.dev](https://go.dev/dl/) or Homebrew:

```sh
brew install go
go version
go install github.com/HexSilence/dorker/cmd/dorker@latest
```

The default binary directory is `$(go env GOPATH)/bin`, usually `~/go/bin`. Add it to `PATH` in your zsh or fish configuration if needed. Podman or Docker is optional.

### Arch Linux

```sh
sudo pacman -S go
go version
go install github.com/HexSilence/dorker/cmd/dorker@latest
```

For fish, `fish_add_path (go env GOPATH)/bin` handles a custom GOPATH. For bash or zsh, add `export PATH="$PATH:$(go env GOPATH)/bin"` to the appropriate shell profile. Install `podman` only if you want local SearXNG: `sudo pacman -S podman`.

### Kali Linux

```sh
sudo apt update
sudo apt install golang-go
go version
```

Continue only if this reports Go 1.24+. Otherwise install Go from [go.dev](https://go.dev/doc/install). Then run the standard `go install` command above. Podman is optional: `sudo apt install podman`.

### Ubuntu / Debian

Install `golang-go` with APT when that distribution release provides Go 1.24+. Otherwise use the [official Go installation](https://go.dev/doc/install). Then run:

```sh
go install github.com/HexSilence/dorker/cmd/dorker@latest
```

### Fedora

```sh
sudo dnf install golang
go version
go install github.com/HexSilence/dorker/cmd/dorker@latest
```

Podman is optional: `sudo dnf install podman`.

### Generic Go install

```sh
go install github.com/HexSilence/dorker/cmd/dorker@latest
go env GOPATH
go env GOBIN
```

If `GOBIN` is empty, the binary is placed in `$(go env GOPATH)/bin`. That directory must be on `PATH`. Full platform instructions, updates, and uninstall steps are in [docs/installation.md](docs/installation.md).

### Private repository access

Authenticated collaborators can tell Go not to use the public module proxy:

```sh
go env -w GOPRIVATE=github.com/HexSilence/*
go install github.com/HexSilence/dorker/cmd/dorker@latest
```

Git must already be authenticated for GitHub using the user's normal SSH or credential-manager setup. Do not embed tokens in repository URLs. If the repository becomes public later, `go install ...@latest` will work without private-module authentication.

## Quick start

```sh
dorker
dorker doctor
dorker setup
dorker search -q 'site:example.com'
dorker search -q 'site:example.com ext:pdf'
dorker ghdb
dorker providers
```

## First-time setup

`dorker setup` creates a safe starter configuration and lists provider choices. `dorker doctor` checks configuration, paths, provider availability, and optional container runtimes. A missing Podman or Docker installation is not an error unless local SearXNG is desired.

```text
Dorker Doctor

  Platform       Ready · linux/amd64
  Go             Ready · 1.24
  Config         Ready
  Database       Ready
  Output dir     Ready
  Podman         Ready
  Providers      1 available
```

## Search

```sh
dorker search -q 'site:example.com'
dorker search -q 'site:example.com ext:pdf'
dorker search -q 'site:example.com' --collapse host
dorker search -f queries.txt
cat queries.txt | dorker search
dorker search -q 'site:example.com' -o results.txt
dorker search -q 'site:example.com' --format jsonl -o results.jsonl
dorker search -q 'site:example.com' --format csv -o results.csv
```

Dorker sends query text only to configured providers. Local validation never requests discovered URLs.

## GHDB

`dorker ghdb` opens a numbered catalog of curated, local query templates. Select an entry, optionally enter a target domain, review the expanded query, and confirm the search.

```text
dorker ghdb
dorker ghdb list
dorker ghdb show 5
dorker ghdb find login
dorker ghdb run 5 --domain example.com
```

GHDB searches use the normal provider, validation, deduplication, persistence, collapse, and export pipeline. See [docs/ghdb.md](docs/ghdb.md).

## Automatic output

Interactive searches save non-empty results under `~/dorker-output` by default:

```text
example.com/directory-listings/results.txt
ghdb/directory-listings/results.txt
search/site-example.com/results.txt
```

Repeated runs use `results-2.txt`, `results-3.txt`, and so on. Existing files are not overwritten. `--format jsonl` and `--format csv` select matching extensions. `--collapse none|host|domain` controls the saved selection while SQLite retains canonical data.

Explicit `-o` always wins. Redirects and pipelines write only to stdout and do not create an extra automatic file:

```sh
dorker search -q 'site:example.com' > results.txt
dorker search -q 'site:example.com' | httpx
```

Zero-result automatic searches do not create empty files. Change the root with `output.directory` or `--output-dir`.

## Providers

| Provider | Credentials | Test |
|---|---|---|
| SearXNG | URL; optional token environment variable | `dorker providers test searxng` |
| Brave Search | `BRAVE_API_KEY` | `dorker providers test brave` |
| Google Custom Search | `GOOGLE_API_KEY`, `GOOGLE_CSE_ID` | `dorker providers test google-api` |
| SerpAPI | `SERPAPI_API_KEY` | `dorker providers test serpapi` |

`dorker providers` performs a local configuration/status check. The explicit `providers test` command makes one small live request. Details are in [docs/providers.md](docs/providers.md).

## Local SearXNG

```sh
dorker setup searxng
dorker setup searxng --yes
```

Dorker prefers Podman and falls back to Docker. It explains the action before requiring confirmation, generates a local secret without displaying it, mounts only its settings file, binds `127.0.0.1:8080`, waits for the JSON API, and updates Dorker's config only after success. Settings live beside the Dorker config under `searxng/settings.yml`.

The image is digest-pinned. Dorker does not pull or update it during normal startup.

## Configuration

```sh
dorker config init
dorker config check
```

The config path comes from Go's `os.UserConfigDir()`:

- Linux: `$XDG_CONFIG_HOME/dorker/config.yaml`, normally `~/.config/dorker/config.yaml`
- macOS: `~/Library/Application Support/dorker/config.yaml`
- Windows: `%AppData%\dorker\config.yaml`

Use `DORKER_CONFIG` or `--config` to override it. Example YAML is in [configs/config.example.yaml](configs/config.example.yaml). Credentials remain in environment variables and are never printed.

## Commands

| Command | Purpose |
|---|---|
| `search` | Search, validate, collect, and export URLs |
| `ghdb` | Browse and run local GHDB templates |
| `providers` | Show or test provider status |
| `query` | Explain query constraints or validate a URL |
| `jobs` | List persisted jobs |
| `resume` | Continue a checkpointed job |
| `stats` | Show job statistics |
| `export` | Export a persisted job |
| `config` | Print, initialize, or check configuration |
| `setup` | Create configuration or bootstrap SearXNG |
| `doctor` | Inspect the local environment |
| `version` | Show build information |

Run `dorker <command> --help` for flags.

## Updating

```sh
go install github.com/HexSilence/dorker/cmd/dorker@latest
```

Private-repository authentication still applies. Source contributors can pull the reviewed branch and run `go install ./cmd/dorker`.

## Uninstalling

Remove only the binary from `go env GOBIN`, or from `$(go env GOPATH)/bin` when `GOBIN` is empty. On Windows, remove `dorker.exe` from that directory. On macOS/Linux, remove `dorker` from it.

Configuration, the SQLite database, saved results, and the optional `dorker-searxng` container are separate and are not removed automatically. Review those paths individually before deleting any data.

## Troubleshooting

Start with `dorker doctor`. Common PATH, provider, configuration, permission, rate-limit, SearXNG, and private-module issues are covered in [docs/troubleshooting.md](docs/troubleshooting.md).

## Development

```sh
gofmt -w .
go test ./...
go vet ./...
go test -race ./...
go build ./...
```

The SQLite driver is pure Go, so CGO is not required. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

Use Dorker only where you have authorization. It does not solve CAPTCHAs, evade access controls, rotate proxies, or bypass provider limits. See [SECURITY.md](SECURITY.md) for vulnerability-reporting guidance.

## License

No license has been published. The owner has not granted redistribution rights through an open-source license.
