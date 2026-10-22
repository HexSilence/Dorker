# Configuration

The first plain `dorker` run creates a minimal starter file atomically. `dorker setup` does the same during onboarding. `dorker config init` creates it explicitly, and `dorker config check` validates it. Bootstrap never replaces an existing file; only explicit `config init --force` does.

Dorker uses `os.UserConfigDir()`: normally `$XDG_CONFIG_HOME/dorker/config.yaml` or `~/.config/dorker/config.yaml` on Linux, `~/Library/Application Support/dorker/config.yaml` on macOS, and `%AppData%\dorker\config.yaml` on Windows. `DORKER_CONFIG` and `--config` override it.

API credentials are read from `BRAVE_API_KEY`, `GOOGLE_API_KEY` plus `GOOGLE_CSE_ID`, and `SERPAPI_API_KEY`. SearXNG can use `SEARXNG_URL`; when set, it overrides YAML instances for that invocation. YAML instances may name a token environment variable. Never commit credentials.

`validation.mode` accepts `off`, `conservative`, or `strict`. Site inclusion/exclusion is always enforced as scope after URL normalization; `off` disables optional extension and `inurl` checks, not the site boundary.

Persistent defaults do not depend on the working directory:

| OS | Config | Database/state | Output |
|---|---|---|---|
| Linux | `$XDG_CONFIG_HOME/dorker/config.yaml` or `~/.config/dorker/config.yaml` | `$XDG_DATA_HOME/dorker/dorker.db` or `~/.local/share/dorker/dorker.db` | `~/dorker-output` |
| macOS | `~/Library/Application Support/dorker/config.yaml` | `~/Library/Application Support/dorker/dorker.db` | `~/dorker-output` |
| Windows | `%AppData%\dorker\config.yaml` | `%LocalAppData%\dorker\dorker.db` | `%USERPROFILE%\dorker-output` |

OS APIs and environment determine the exact user directories. `DORKER_CONFIG`, `--config`, `database.path`, `--db`, `output.directory`, and `--output-dir` override them. Collapse accepts `none`, `host`, or `domain`.

Discovery defaults are bounded and can be adjusted without changing search behavior:

```yaml
discovery:
  max_depth: 3
  max_hosts: 5000
  max_queries: 10000
  max_duration: 30m
  saturation_threshold: 0.03
  saturation_consecutive_depths: 2
```

Command-line discovery flags override these values. Existing configurations that omit the section receive the built-in defaults.

Passive future-release awareness is enabled by default:

```yaml
update_check:
  enabled: true
```

Interactive commands use cached public GitHub release metadata and may display one concise notice when a future stable release exists. The cache lasts approximately 24 hours. Set `enabled: false` or `DORKER_NO_UPDATE_CHECK=1` to disable passive checks. An explicit `dorker update` request remains available.
