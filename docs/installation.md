# Installation

Dorker requires Go 1.24+. Podman or Docker is optional and only needed for a local SearXNG provider.

The repository is private. Collaborators need existing GitHub access and authenticated Git. Configure the module namespace without placing credentials in a URL:

```text
go env -w GOPRIVATE=github.com/HexSilence/*
```

When the repository becomes public, the same install command works without private-module authentication.

## Windows

Open PowerShell. Install Go using the official installer from https://go.dev/dl/ or Windows Package Manager:

```powershell
winget install --id GoLang.Go -e
go version
```

Confirm Go 1.24 or newer, then install Dorker:

```powershell
go install github.com/HexSilence/dorker/cmd/dorker@latest
```

Inspect the install locations:

```powershell
go env GOPATH
go env GOBIN
```

When `GOBIN` is empty, the normal location is `%USERPROFILE%\go\bin`. Add the resolved directory to the user-level `Path` through Settings → System → About → Advanced system settings → Environment Variables. Open a new PowerShell window afterward. This avoids overwriting the existing `Path` accidentally.

```powershell
dorker
dorker doctor
dorker setup
```

Docker Desktop or Podman Desktop is optional. `dorker setup searxng` can use their command-line tools, but desktop/runtime integration varies on Windows. Confirm `docker version` or `podman version` works first.

Update with the same `go install ...@latest` command. To uninstall, find the directory above and remove only `dorker.exe`:

```powershell
$bin = if (go env GOBIN) { go env GOBIN } else { Join-Path (go env GOPATH) "bin" }
Remove-Item (Join-Path $bin "dorker.exe")
```

This does not remove config, databases, output, or a SearXNG container.

## macOS

Install Go using the package from https://go.dev/dl/ or Homebrew:

```sh
brew install go
go version
go install github.com/HexSilence/dorker/cmd/dorker@latest
```

Inspect `go env GOBIN` and `go env GOPATH`. When GOBIN is empty, the binary is normally in `~/go/bin`. For zsh, add the resolved directory to `PATH` in `~/.zshrc`; for fish use `fish_add_path (go env GOPATH)/bin`. Dorker does not modify shell files.

Run `dorker`, `dorker doctor`, and `dorker setup`. Podman or Docker is optional. Update with the same Go install command. Uninstall by removing only the `dorker` binary from GOBIN or GOPATH/bin.

## Arch Linux

```sh
sudo pacman -S go
go version
go install github.com/HexSilence/dorker/cmd/dorker@latest
```

For fish:

```fish
fish_add_path (go env GOPATH)/bin
```

For bash/zsh, add `export PATH="$PATH:$(go env GOPATH)/bin"` to the appropriate profile. Verify custom locations with `go env GOBIN` and `go env GOPATH`.

```sh
dorker
dorker doctor
dorker setup
```

For optional rootless local SearXNG:

```sh
sudo pacman -S podman
dorker setup searxng
```

Podman is not required for other providers. Once rootless Podman works, Dorker's container does not require sudo.

## Kali Linux

```sh
sudo apt update
sudo apt install golang-go
go version
```

The reported version must be Go 1.24+. If the current Kali repository is older, use https://go.dev/doc/install rather than an unsupported compiler.

```sh
go install github.com/HexSilence/dorker/cmd/dorker@latest
```

The normal binary location is `$(go env GOPATH)/bin`, usually `~/go/bin`. Add it to PATH if necessary. Optional local SearXNG uses `sudo apt install podman`. Then run `dorker doctor` and `dorker setup`.

## Ubuntu / Debian

Use `sudo apt install golang-go` only when the distribution supplies Go 1.24+. Otherwise use the official Go installation instructions. Install Dorker with:

```sh
go install github.com/HexSilence/dorker/cmd/dorker@latest
```

Add GOBIN or `$(go env GOPATH)/bin` to PATH. Podman or Docker is optional and only needed for local SearXNG.

## Fedora

```sh
sudo dnf install golang
go version
go install github.com/HexSilence/dorker/cmd/dorker@latest
```

Add GOBIN or `$(go env GOPATH)/bin` to PATH. Optional local SearXNG uses `sudo dnf install podman`. Run `dorker doctor` and `dorker setup` after installation.

## Generic Go install

```sh
go version
go install github.com/HexSilence/dorker/cmd/dorker@latest
go env GOPATH
go env GOBIN
```

Go must be 1.24+. If GOBIN is empty, use `$(go env GOPATH)/bin`. The selected bin directory must be on PATH.

## Updating

Run the same install command again:

```sh
go install github.com/HexSilence/dorker/cmd/dorker@latest
```

Source contributors may update their checkout and run `go install ./cmd/dorker`.

## Uninstalling

Remove only `dorker` (`dorker.exe` on Windows) from the resolved Go bin directory. `make uninstall` removes the developer installation at `BINDIR` when that workflow was used.

Configuration, SQLite data, saved results, and `dorker-searxng` are separate. They are intentionally retained. Inspect each location or container before removing it; do not recursively delete broad user directories.
