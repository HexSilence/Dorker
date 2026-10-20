# Installation

Dorker requires Go 1.24 or newer. Podman or Docker is optional and is needed only when Dorker manages a local SearXNG instance.

## A. Recommended Go installation

After the public `v1.0.0` tag is available:

```sh
go version
go install github.com/HexSilence/dorker/cmd/dorker@v1.0.0
```

This installs a self-contained command. A repository clone, CGO compiler, config file, and source-tree assets are not required.

## B. Put the Go binary directory on PATH

`go install` writes to `GOBIN`. When `GOBIN` is empty, it writes to `GOPATH/bin`. Inspect both with `go env GOBIN` and `go env GOPATH`. Go does not edit `PATH` automatically.

### bash

Add this to `~/.bashrc`, then open a new shell:

```sh
export PATH="$PATH:$(go env GOPATH)/bin"
```

### zsh

Add this to `~/.zshrc`, then open a new shell:

```sh
export PATH="$PATH:$(go env GOPATH)/bin"
```

### fish

Run once:

```fish
fish_add_path (go env GOPATH)/bin
```

For any shell, use the non-empty `go env GOBIN` value instead when configured.

### Windows PowerShell

```powershell
$goBin = go env GOBIN
if (-not $goBin) { $goBin = Join-Path (go env GOPATH) "bin" }
$goBin
```

Add that directory to the user-level `Path` through Settings → System → About → Advanced system settings → Environment Variables, then open a new PowerShell window. Avoid replacing the existing `Path`. The usual fallback is `%USERPROFILE%\go\bin`.

## C. Verify

```sh
dorker version
dorker doctor
```

On a clean machine, missing providers and optional runtimes are setup notices. Invalid configuration or unwritable required paths return failure.

## D. First setup

```sh
dorker setup
```

Choose an API provider or preview local SearXNG with `dorker setup searxng`. Dorker never runs `sudo`, APT, DNF, pacman, Homebrew, winget, Chocolatey, or another system installer.

## E. First search

```sh
dorker search -q 'site:example.com'
```

## F. Discovery

```sh
dorker discover -q 'site:example.com'
```

Discovery requires a positive `site:` scope and enforces it even when optional validation is disabled.

## Platform notes

- Linux: config honors `XDG_CONFIG_HOME`; state honors `XDG_DATA_HOME`. Rootless Podman is supported when its CLI already works.
- macOS: Go normally installs to `~/go/bin`; Intel and Apple silicon are supported. Podman Desktop or Docker Desktop is optional.
- Windows: use PowerShell and `dorker.exe`; Windows amd64 is supported. Confirm the Podman or Docker CLI works before local setup.

Distribution repositories sometimes carry an older Go release. Check `go version`; if it is older than 1.24, use [go.dev](https://go.dev/dl/). Runtime package-manager commands shown by setup are guidance only.

## Updating and uninstalling

Update by running the same versioned `go install` command for the desired published release.

To uninstall, resolve `GOBIN`/`GOPATH/bin` and remove only `dorker` (`dorker.exe` on Windows). Config, SQLite state, output, and an optional `dorker-searxng` container are retained.

## Release validation

Before the tag exists, CI installs the checkout into a temporary `GOBIN`, changes to an unrelated temporary directory, and runs home/help/version/doctor on Linux, macOS, and Windows. After publication, repeat that smoke test with the canonical `@v1.0.0` command above.
