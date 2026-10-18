APP := $(shell sed -n 's/const Name = "\(.*\)"/\1/p' internal/buildinfo/buildinfo.go)
VERSION ?= $(shell sed -n 's/const ReleaseVersion = "\(.*\)"/\1/p' internal/buildinfo/buildinfo.go)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X github.com/HexSilence/dorker/internal/buildinfo.Version=$(VERSION) -X github.com/HexSilence/dorker/internal/buildinfo.Commit=$(COMMIT) -X github.com/HexSilence/dorker/internal/buildinfo.Date=$(BUILD_DATE)

.PHONY: build test vet fmt lint race clean install uninstall
build:
	mkdir -p bin
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(APP) ./cmd/$(APP)
test:
	go test ./...
vet:
	go vet ./...
fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')
lint:
	@command -v golangci-lint >/dev/null || { printf '%s\n' 'golangci-lint is not installed; see README.md'; exit 1; }
	golangci-lint run
race:
	go test -race ./...
clean:
	rm -rf -- bin

BINDIR ?= $(HOME)/.local/bin
install: build
	install -d '$(BINDIR)'
	install -m 0755 'bin/$(APP)' '$(BINDIR)/$(APP)'
	@printf 'Installed %s\n' '$(BINDIR)/$(APP)'

uninstall:
	rm -f -- '$(BINDIR)/$(APP)'
	@printf 'Removed %s\n' '$(BINDIR)/$(APP)'
