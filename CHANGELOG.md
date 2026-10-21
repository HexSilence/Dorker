# Changelog

## v1.0.1

- Added the confirmation-based `dorker update` stable-release workflow.
- Added cached, optional awareness of future stable releases.
- Made managed SearXNG setup idempotent and safe around compatible legacy containers.
- Added persistent bounded discovery with scope-safe resume and saturation reporting.
- Hardened site validation, output ownership, job leases, platform portability, and build metadata.
- Prepared public Apache-2.0 documentation and release presentation.

## v1.0.0

- Multi-provider search with bounded scheduling, retries, validation, normalization, deduplication, and SQLite persistence.
- Local GHDB catalog and interactive selection.
- TXT, JSONL, and CSV exports with host/domain collapse modes.
- Safe automatic output for interactive searches.
- Configuration initialization/checking, environment doctor, and guided setup.
- Bounded recursive discovery with a persistent frontier, leases, owned artifacts,
  safe resume, and degraded/trustworthy terminal-evidence semantics.
- First-run config bootstrap and OS-native config/state paths.
