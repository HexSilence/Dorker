# Discovery

`dorker search` retrieves URLs for supplied queries. `dorker discover -q 'site:example.com'` additionally expands each newly observed, accepted host with an internal `site:<host>` query. Expansion uses Dorker's normal providers, scheduler, rate limits, retries, normalization, validation, persistence, and cancellation; it does not invoke a shell or request discovered URLs.

Only URLs accepted by the seed query's locally enforceable site scope can create frontier entries. The frontier is ordered by normalized hostname, and a completed host is not expanded again. Hostnames remain distinct (`example.com` and `www.example.com`), while registrable-domain reporting groups them using the public suffix list.

Useful bounds include `--max-depth`, `--max-hosts`, `--max-queries`, `--max-results`, `--max-results-per-query`, `--max-pages`, and `--max-duration`. Built-in defaults are conservative and can be changed under `discovery:` in the configuration; command-line flags win.

For each depth, host gain is:

```text
new unique hosts at this depth / unique hosts known before this depth
```

The initial seed depth reports a gain of 1 when it discovers hosts. Later zero-gain trustworthy depths stop immediately. Otherwise saturation requires the configured number of consecutive trustworthy depths below `--saturation-threshold`, after the minimum observation depth. Rate-limited, unavailable, challenged, timed-out, or otherwise degraded provider coverage is not evidence of saturation; such a terminal run is reported as partial. A useful SearXNG response remains degraded when it also reports an unresponsive source engine: returned results prove that named sources responded, but do not prove that a missing source could not have contributed more hosts. An earlier coverage gap remains unresolved across later depths; absence of another diagnostic is not proof of recovery. A runner may clear that state only after explicitly proving that the incomplete work was re-executed with full coverage. Current SearXNG responses do not supply that proof. When no new frontier remains under unresolved degraded coverage, the machine-readable stop reason is `FRONTIER_EXHAUSTED_DEGRADED`.

Interactive runs save deterministic `urls.txt`, `hosts.txt`, `roots.txt`, and `discovery.json` artifacts. The JSON report includes limits, status, stop reason, coverage status, terminal detail, counts, and per-depth statistics without provider credentials. `historical_degradation` describes the full run, while `terminal_evidence_status` says whether the terminal saturation decision had sufficient evidence. This keeps a genuinely recovered run understandable: it may retain historical degradation while ending `SATURATED` with sufficient terminal evidence. `responsive_sources_observed` lists source engines attached to accepted results; it is evidence of response, not a completeness claim. Provider-health `count`/`observations` values count distinct query-page-message diagnostic observations. An identical retry for the same query and page is deduplicated, while a changed diagnostic message or the same degradation on another page or query is retained. Existing artifact directories are not overwritten.

Redirected output stays machine-readable:

```sh
dorker discover -q 'site:example.com' --hosts-only | sort -u
dorker discover -q 'site:example.com' --roots-only > roots.txt
```

Progress and diagnostics remain on stderr. Redirected/piped runs do not create an additional automatic artifact directory. Resume an interrupted job with `dorker resume <job-id>`; completed hosts, pending frontier state, depth statistics, limits, validation mode, and provenance are retained. Hosts interrupted while expanding are safely returned to the queue.

Search indexes are incomplete and change over time. Discovery measures what configured providers return; it does not claim exhaustive Internet enumeration.
