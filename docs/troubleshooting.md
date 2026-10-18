# Troubleshooting

Start with:

```text
dorker doctor
```

## `dorker`: command not found

Run `go env GOBIN` and `go env GOPATH`. If GOBIN is empty, the binary is under GOPATH/bin. Add that exact directory to PATH and open a new terminal.

- Windows: add the directory to the user `Path` through Environment Variables.
- macOS zsh: add it to `PATH` in `~/.zshrc`.
- Arch fish: run `fish_add_path (go env GOPATH)/bin`.

## No provider configured

Run `dorker setup`, configure one provider, then use `dorker providers`. API providers need their documented environment variables. SearXNG needs a valid instance URL.

## SearXNG isn't configured

Set `SEARXNG_URL`, add an instance to the YAML config, or run `dorker setup searxng` for the guided local option.

## SearXNG container will not start

Confirm `podman version` or `docker version` works without Dorker, ensure port 8080 is free, and inspect logs with the selected runtime's `logs dorker-searxng` command. Dorker binds only `127.0.0.1:8080`.

## Podman isn't installed

Podman is optional. Use a remote/API provider, Docker, or install Podman through the operating system's package manager. Dorker never installs it automatically.

## Provider rate limited or access denied

Dorker honors cooldowns and does not bypass provider protections. Wait for the provider limit to recover, verify account/quota status, or select another configured provider. Use `dorker providers test NAME` for one explicit diagnostic request.

## Configuration invalid

Run `dorker config check`. Unknown YAML fields, unsupported enum values, invalid URLs, and missing required provider fields are reported without printing secrets.

## Permission denied for output

Check `output.directory` and `--output-dir`. The current user must be able to create directories and files there. Dorker should not be run with sudo to work around ownership problems.

## Private repository installation fails

Confirm the GitHub account has repository access and Git authentication already works. Then configure `GOPRIVATE=github.com/HexSilence/*`. Do not place personal access tokens in clone or module URLs.

## Windows PATH issue

The usual location is `%USERPROFILE%\go\bin`, but use `go env GOBIN` and `go env GOPATH` as the source of truth. Change the user Path, not the system Path, unless system-wide administration is intentional.

## macOS PATH issue

The usual location is `~/go/bin`. Add the resolved Go bin directory to zsh or fish configuration, then start a new terminal.
