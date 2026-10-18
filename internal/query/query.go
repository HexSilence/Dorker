package query

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/HexSilence/dorker/internal/model"
)

func Load(flags, files []string, stdin io.Reader, readStdin bool) ([]string, error) {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(flags))
	add := func(q string) {
		q = strings.TrimSpace(q)
		if q == "" || strings.HasPrefix(q, "#") {
			return
		}
		if _, ok := seen[q]; !ok {
			seen[q] = struct{}{}
			out = append(out, q)
		}
	}
	for _, q := range flags {
		add(q)
	}
	for _, name := range files {
		f, err := os.Open(name)
		if err != nil {
			return nil, fmt.Errorf("open query file %s: %w", name, err)
		}
		if err := scan(f, add); err != nil {
			f.Close()
			return nil, fmt.Errorf("read query file %s: %w", name, err)
		}
		f.Close()
	}
	if readStdin {
		if err := scan(stdin, add); err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no queries supplied")
	}
	return out, nil
}
func scan(r io.Reader, add func(string)) error {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	for s.Scan() {
		add(s.Text())
	}
	return s.Err()
}

func Plan(queries []string, mode string, max int) ([]model.QueryVariant, error) {
	if max < 1 {
		return nil, fmt.Errorf("max query variants must be positive")
	}
	var out []model.QueryVariant
	for _, q := range queries {
		variants := []string{q}
		if mode == "conservative" {
			// Whitespace canonicalization preserves search semantics for unquoted terms.
			if !strings.Contains(q, "\"") {
				if v := strings.Join(strings.Fields(q), " "); v != q {
					variants = append(variants, v)
				}
			}
		} else if mode != "exact" {
			return nil, fmt.Errorf("query mode must be exact or conservative")
		}
		if len(variants) > max {
			variants = variants[:max]
		}
		for i, v := range variants {
			out = append(out, model.QueryVariant{Original: q, Variant: v, Ordinal: i})
		}
	}
	return out, nil
}
