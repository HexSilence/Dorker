#!/bin/sh
set -eu

if ! command -v go >/dev/null 2>&1; then
  printf '%s\n' 'Go is required. Install Go using your operating system or https://go.dev/dl/.' >&2
  exit 1
fi

go_version=$(go env GOVERSION)
version=${go_version#go}
major=${version%%.*}
rest=${version#*.}
minor=${rest%%.*}
if [ "$major" -lt 1 ] || { [ "$major" -eq 1 ] && [ "$minor" -lt 24 ]; }; then
  printf 'Go 1.24 or newer is required; found %s.\n' "$go_version" >&2
  exit 1
fi

printf '%s\n' 'Installing Dorker from this source tree. No system packages or configuration will be changed.'
go install ./cmd/dorker
printf '%s\n' 'Installed. If dorker is not found, add $(go env GOPATH)/bin (or GOBIN) to PATH.'
