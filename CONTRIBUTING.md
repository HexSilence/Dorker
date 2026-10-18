# Contributing

Run `gofmt -w .`, `go test ./...`, `go vet ./...`, `go test -race ./...`, and `go build ./...` before submitting a pull request. Keep changes focused and include tests for behavior changes.

Provider changes must preserve rate limits, bounded retries, credential redaction, and the rule that Dorker uses documented APIs rather than scraping or bypassing access controls. Add unit tests with local HTTP test servers; tests must not require live credentials.

Pull requests should explain user-visible changes and compatibility impact. Do not include databases, result files, local configuration, credentials, or generated binaries.

UI changes must keep stdout data-only when redirected or piped: banners, progress, logs, and summaries belong on stderr. Do not change persisted values, job options, or export schemas casually; document compatibility decisions and add regression tests.
