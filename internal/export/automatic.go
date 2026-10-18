package export

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Slug returns a readable path component containing only portable ASCII.
func Slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' {
			b.WriteRune(r)
			dash = false
		} else {
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-.")
	if s == "" || s == "." || s == ".." {
		return "search"
	}
	// Avoid Windows device names, even when running on another OS.
	base := strings.TrimSuffix(s, filepath.Ext(s))
	switch strings.ToUpper(base) {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return "search-" + s
	}
	return s
}

// AutomaticPath selects results.ext, results-2.ext, ... without overwriting.
func AutomaticPath(root string, components []string, format string) (string, error) {
	if format != "txt" && format != "jsonl" && format != "csv" {
		return "", fmt.Errorf("unsupported format %q", format)
	}
	dir := filepath.Clean(root)
	for _, component := range components {
		dir = filepath.Join(dir, Slug(component))
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	for n := 1; ; n++ {
		name := "results." + format
		if n > 1 {
			name = fmt.Sprintf("results-%d.%s", n, format)
		}
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path, nil
		} else if err != nil {
			return "", err
		}
	}
}
