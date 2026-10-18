# GHDB searches

`dorker ghdb` opens the local catalog. Leave the optional target domain empty to search globally. A domain search is saved below `~/dorker-output/<domain>/<dork-name>/`; a global search is saved below `~/dorker-output/ghdb/<dork-name>/`. Names are sanitized portable path components. Repeated runs use `results-2.txt`, `results-3.txt`, and so on.

The selected query still uses the normal provider, validation, collapse, persistence, and export pipeline. No file is created for zero results.
