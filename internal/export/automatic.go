package export

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxSlugLength = 80

// Slug returns a readable path component containing only portable ASCII.
func Slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	separator := false
	for i := 0; i < len(value) && b.Len() < maxSlugLength; i++ {
		c := value[i]
		alphanumeric := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		// Dots are useful within hostnames, but leading, trailing, and repeated
		// dots are punctuation rather than meaningful path text.
		dot := c == '.' && !separator && b.Len() > 0 && i+1 < len(value) && isASCIIAlphanumeric(value[i+1])
		if alphanumeric || dot {
			if separator && b.Len() > 0 && b.Len() < maxSlugLength {
				b.WriteByte('-')
			}
			if b.Len() < maxSlugLength {
				b.WriteByte(c)
			}
			separator = false
		} else if b.Len() > 0 {
			separator = true
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

func isASCIIAlphanumeric(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
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
