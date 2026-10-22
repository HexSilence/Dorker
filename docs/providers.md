# Providers

`dorker providers` reports local configuration and eligibility without making network requests. `Ready` there means the provider is configured and can be selected. `dorker providers test NAME` performs one small live request without pagination or retry loops and reports endpoint reachability and, for SearXNG, available upstream-engine diagnostics.

## SearXNG

SearXNG is the free-first option and requires an instance URL, not an API key. Use a self-hosted instance or one whose operator permits API access. JSON output must be enabled.

For one instance, set `SEARXNG_URL=http://127.0.0.1:8080`. For multiple instances, use `providers.searxng.instances` in YAML. A protected instance can name an environment variable in `token_env`; Dorker reads the token from that variable.

```sh
dorker providers test searxng
```

For a search that explicitly selects SearXNG engines, use `--provider searxng --searxng-engines engine1,engine2`. Values are trimmed, case-normalized, and deduplicated. `--searxng-strategy balanced` selects one healthy configured instance with failover; `coverage` queries every usable configured instance and merges their results. These SearXNG-only options are rejected when SearXNG is not selected.

Dorker's logical page zero maps to SearXNG `pageno=1`, and later logical pages advance sequentially. SearXNG does not expose a portable result-count parameter, so `--page-size` is not sent to SearXNG and a short non-empty response does not prove exhaustion. An empty response does. Repeated pages can still trigger adaptive early stopping; when SearXNG simultaneously reports degraded upstream engines, Dorker requires additional low-yield evidence and reports the stop as partial degraded coverage rather than trustworthy saturation. `--max-pages` remains a hard ceiling, not a required request count.

## Brave Search API

Create an API credential through Brave Search and expose it to Dorker without writing it into example configuration:

```text
BRAVE_API_KEY
```

Test with `dorker providers test brave`.

## Google Custom Search

Google Custom Search requires both:

```text
GOOGLE_API_KEY
GOOGLE_CSE_ID
```

Test with `dorker providers test google-api`.

## SerpAPI

SerpAPI requires:

```text
SERPAPI_API_KEY
```

Enable the provider in YAML and test with `dorker providers test serpapi`.

## Development provider

The deterministic `mock` provider is for tests and local development. It is disabled in normal configuration and requires no credentials.

## Provider behavior

Each provider has bounded request rates and independent health state. Rate limits, quota failures, authentication failures, transport errors, or temporary errors affect that provider task without discarding results from healthy providers. A single-provider failure is reported as such; a continuation notice is shown only when another active provider actually continued. Credentials are never included in status output.
