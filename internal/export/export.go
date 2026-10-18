package export

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/store"
)

func Write(ctx context.Context, st *store.Store, job, format, destination string, overwrite bool, stdout io.Writer) error {
	_, err := WriteCollapsed(ctx, st, job, format, destination, overwrite, stdout, CollapseNone)
	return err
}

func WriteCollapsed(ctx context.Context, st *store.Store, job, format, destination string, overwrite bool, stdout io.Writer, mode CollapseMode) (int, error) {
	if destination == "" {
		return 0, nil
	}
	if destination == "-" {
		return write(ctx, st, job, format, stdout, mode)
	}
	destination = filepath.Clean(destination)
	if info, err := os.Stat(destination); err == nil && !overwrite {
		return 0, fmt.Errorf("output %s already exists (use --overwrite)", destination)
	} else if err == nil && info.IsDir() {
		return 0, fmt.Errorf("output %s is a directory", destination)
	} else if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	dir := filepath.Dir(destination)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, err
	}
	f, err := os.CreateTemp(dir, ".dorker-export-*")
	if err != nil {
		return 0, err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(tmp)
		}
	}()
	if err = f.Chmod(0600); err != nil {
		return 0, err
	}
	count, err := write(ctx, st, job, format, f, mode)
	if err != nil {
		return 0, err
	}
	if err = f.Sync(); err != nil {
		return 0, err
	}
	if err = f.Close(); err != nil {
		return 0, err
	}
	if overwrite {
		if err = os.Rename(tmp, destination); err != nil {
			return 0, err
		}
	} else {
		// A hard link publishes the completed file atomically and cannot replace a
		// destination created after the initial existence check.
		if err = os.Link(tmp, destination); err != nil {
			return 0, fmt.Errorf("publish output: %w", err)
		}
		if err = os.Remove(tmp); err != nil {
			return 0, err
		}
	}
	ok = true
	return count, nil
}
func write(ctx context.Context, st *store.Store, job, format string, w io.Writer, mode CollapseMode) (int, error) {
	rows, err := st.Results(ctx, job)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var results []model.Result
	for rows.Next() {
		var r model.Result
		var discovered string
		if err := rows.Scan(&r.NormalizedURL, &r.Domain, &r.Title, &r.Snippet, &r.Provider, &r.OriginalQuery, &r.QueryVariant, &r.Rank, &r.Page, &discovered, &r.URL); err != nil {
			return 0, err
		}
		r.DiscoveredAt, _ = time.Parse(time.RFC3339Nano, discovered)
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	results, err = Collapse(results, mode)
	if err != nil {
		return 0, err
	}
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	var cw *csv.Writer
	if format == "csv" {
		cw = csv.NewWriter(bw)
		if err := cw.Write([]string{"url", "original_url", "domain", "title", "snippet", "provider", "original_query", "query_variant", "rank", "page", "discovered_at"}); err != nil {
			return 0, err
		}
	}
	for _, r := range results {
		switch format {
		case "txt":
			if _, err = fmt.Fprintln(bw, r.NormalizedURL); err != nil {
				return 0, err
			}
		case "jsonl":
			var b []byte
			b, err = json.Marshal(r)
			if err == nil {
				_, err = bw.Write(append(b, '\n'))
			}
			if err != nil {
				return 0, err
			}
		case "csv":
			if err = cw.Write([]string{r.NormalizedURL, r.URL, r.Domain, r.Title, r.Snippet, r.Provider, r.OriginalQuery, r.QueryVariant, strconv.Itoa(r.Rank), r.Page, r.DiscoveredAt.Format(time.RFC3339Nano)}); err != nil {
				return 0, err
			}
		default:
			return 0, fmt.Errorf("unsupported format %q", format)
		}
	}
	if cw != nil {
		cw.Flush()
		if err := cw.Error(); err != nil {
			return 0, err
		}
	}
	if err := bw.Flush(); err != nil {
		return 0, err
	}
	return len(results), nil
}
