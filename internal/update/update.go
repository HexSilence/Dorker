package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultEndpoint = "https://api.github.com/repos/HexSilence/Dorker/releases?per_page=30"
	DefaultTTL      = 24 * time.Hour
	MaxResponseSize = 256 << 10
)

var stableVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Release struct {
	Version string `json:"version"`
	URL     string `json:"url,omitempty"`
}

type cacheFile struct {
	CheckedAt time.Time `json:"checked_at"`
	Release   Release   `json:"release"`
}

type Client struct {
	HTTP      *http.Client
	Endpoint  string
	CachePath string
	TTL       time.Duration
	Now       func() time.Time
}

func New(cachePath, userAgent string, timeout time.Duration) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	client := &http.Client{
		Timeout: timeout,
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			req.Header.Set("Accept", "application/vnd.github+json")
			req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
			req.Header.Set("User-Agent", userAgent)
			return transport.RoundTrip(req)
		}),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 || req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Hostname(), "api.github.com") {
				return errors.New("unsafe GitHub API redirect")
			}
			return nil
		},
	}
	return &Client{HTTP: client, Endpoint: DefaultEndpoint, CachePath: cachePath, TTL: DefaultTTL, Now: time.Now}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func (c *Client) Latest(ctx context.Context, fresh bool) (Release, error) {
	if !fresh {
		if release, ok := c.cached(); ok {
			return release, nil
		}
	}
	release, err := c.fetch(ctx)
	if err != nil {
		return Release{}, err
	}
	_ = c.writeCache(cacheFile{CheckedAt: c.now().UTC(), Release: release})
	return release, nil
}

func (c *Client) fetch(ctx context.Context) (Release, error) {
	if c.HTTP == nil {
		return Release{}, errors.New("update HTTP client is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint, nil)
	if err != nil {
		return Release{}, fmt.Errorf("create release request: %w", err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("query GitHub releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return Release{}, fmt.Errorf("GitHub releases returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseSize+1))
	if err != nil {
		return Release{}, fmt.Errorf("read GitHub releases: %w", err)
	}
	if len(body) > MaxResponseSize {
		return Release{}, errors.New("GitHub release response exceeds 256 KiB")
	}
	var records []struct {
		Tag        string `json:"tag_name"`
		URL        string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err = dec.Decode(&records); err != nil {
		return Release{}, fmt.Errorf("decode GitHub releases: %w", err)
	}
	if err = dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Release{}, errors.New("decode GitHub releases: trailing data")
	}
	latest := Release{}
	for _, record := range records {
		if record.Draft || record.Prerelease || !Valid(record.Tag) {
			continue
		}
		if latest.Version == "" || Compare(record.Tag, latest.Version) > 0 {
			latest = Release{Version: record.Tag, URL: record.URL}
		}
	}
	if latest.Version == "" {
		return Release{}, errors.New("GitHub has no valid stable Dorker release")
	}
	return latest, nil
}

func (c *Client) cached() (Release, bool) {
	b, err := os.ReadFile(c.CachePath)
	if err != nil || len(b) > 32<<10 {
		return Release{}, false
	}
	var cached cacheFile
	if json.Unmarshal(b, &cached) != nil || !Valid(cached.Release.Version) {
		return Release{}, false
	}
	ttl := c.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	age := c.now().Sub(cached.CheckedAt)
	return cached.Release, age >= 0 && age < ttl
}

func (c *Client) writeCache(value cacheFile) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	dir := filepath.Dir(c.CachePath)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".update-cache-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return replaceFile(tmp, c.CachePath)
}

func replaceFile(tmp, target string) error {
	if err := os.Rename(tmp, target); err == nil {
		return nil
	}
	backup := target + ".old"
	_ = os.Remove(backup)
	if err := os.Rename(target, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Rename(backup, target)
		return err
	}
	_ = os.Remove(backup)
	return nil
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func Valid(version string) bool {
	_, ok := versionParts(version)
	return ok
}

func Compare(a, b string) int {
	pa, oka := versionParts(a)
	pb, okb := versionParts(b)
	if !oka || !okb {
		return 0
	}
	for i := range pa {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

func versionParts(v string) ([3]uint64, bool) {
	var out [3]uint64
	m := stableVersion.FindStringSubmatch(v)
	if m == nil {
		return out, false
	}
	for i := range out {
		n, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

type Installation struct {
	Method     string
	Executable string
	GoBinary   string
	GoBin      string
}

func DetectGoInstallation(executable, goBinary, goBin, goPath, goos string) Installation {
	actual := absoluteForOS(executable, goos)
	candidates := []string{}
	if goBin != "" {
		candidates = append(candidates, joinForOS(goos, goBin, executableName(goos)))
	}
	for _, root := range splitListForOS(goPath, goos) {
		if root != "" {
			candidates = append(candidates, joinForOS(goos, root, "bin", executableName(goos)))
		}
	}
	for _, candidate := range candidates {
		candidate = absoluteForOS(candidate, goos)
		if samePath(actual, candidate, goos) {
			return Installation{Method: "go", Executable: actual, GoBinary: goBinary, GoBin: dirForOS(candidate, goos)}
		}
	}
	return Installation{Method: "unknown", Executable: actual}
}

func executableName(goos string) string {
	if goos == "windows" {
		return "dorker.exe"
	}
	return "dorker"
}

func samePath(a, b, goos string) bool {
	if goos == "windows" {
		return strings.EqualFold(normalizeWindows(a), normalizeWindows(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func normalizeWindows(value string) string { return path.Clean(strings.ReplaceAll(value, "\\", "/")) }
func joinForOS(goos string, parts ...string) string {
	if goos == "windows" {
		return strings.ReplaceAll(path.Join(parts...), "/", "\\")
	}
	return filepath.Join(parts...)
}
func dirForOS(value, goos string) string {
	if goos == "windows" {
		return strings.ReplaceAll(path.Dir(normalizeWindows(value)), "/", "\\")
	}
	return filepath.Dir(value)
}
func splitListForOS(value, goos string) []string {
	if goos == "windows" {
		return strings.Split(value, ";")
	}
	return filepath.SplitList(value)
}
func absoluteForOS(value, goos string) string {
	if goos == "windows" {
		return value
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return value
	}
	return abs
}

func RuntimeInstallation() Installation {
	executable, err := os.Executable()
	if err != nil {
		return Installation{Method: "unknown"}
	}
	goBinary, err := findExecutable("go")
	if err != nil {
		return Installation{Method: "unknown", Executable: executable}
	}
	goBin, goPath := os.Getenv("GOBIN"), os.Getenv("GOPATH")
	if out, envErr := runGoEnv(goBinary); envErr == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if goBin == "" && len(lines) > 0 {
			goBin = strings.TrimSpace(lines[0])
		}
		if goPath == "" && len(lines) > 1 {
			goPath = strings.TrimSpace(lines[1])
		}
	}
	if goPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			goPath = filepath.Join(home, "go")
		}
	}
	return DetectGoInstallation(executable, goBinary, goBin, goPath, runtime.GOOS)
}

var findExecutable = func(name string) (string, error) { return osExecutableLookPath(name) }
var runGoEnv = func(goBinary string) ([]byte, error) {
	return exec.Command(goBinary, "env", "GOBIN", "GOPATH").Output()
}
