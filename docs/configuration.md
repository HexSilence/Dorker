# Configuration

Run `dorker config init` to create a starter file and `dorker config check` to validate it. Existing files are not replaced unless `--force` is given.

Dorker uses `os.UserConfigDir()`: normally `$XDG_CONFIG_HOME/dorker/config.yaml` or `~/.config/dorker/config.yaml` on Linux, `~/Library/Application Support/dorker/config.yaml` on macOS, and `%AppData%\dorker\config.yaml` on Windows. `DORKER_CONFIG` and `--config` override it.

API credentials are read from `BRAVE_API_KEY`, `GOOGLE_API_KEY` plus `GOOGLE_CSE_ID`, and `SERPAPI_API_KEY`. SearXNG can use `SEARXNG_URL`; YAML instances may name a token environment variable. Never commit credentials.

Automatic output defaults to `~/dorker-output`; change `output.directory` or pass `--output-dir`. Collapse accepts `none`, `host`, or `domain`.
