package update

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSemanticVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"v1.0.1", "v1.0.1", 0}, {"v1.0.2", "v1.0.1", 1}, {"v1.1.0", "v1.0.1", 1}, {"v2.0.0", "v1.0.1", 1}, {"v1.0.0", "v1.0.1", -1},
	} {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Fatalf("Compare(%q,%q)=%d want %d", tc.a, tc.b, got, tc.want)
		}
	}
	for _, invalid := range []string{"1.0.1", "v1.0", "v1.0.1-rc.1", "v01.0.1", "latest", "v999999999999999999999.0.0"} {
		if Valid(invalid) {
			t.Fatalf("accepted %q", invalid)
		}
	}
}

func TestLatestFiltersDraftPrereleaseAndMalformed(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `[
{"tag_name":"v9.0.0","draft":true,"prerelease":false},
{"tag_name":"v8.0.0","draft":false,"prerelease":true},
{"tag_name":"not-a-version","draft":false,"prerelease":false},
{"tag_name":"v1.0.2","html_url":"https://github.com/HexSilence/Dorker/releases/tag/v1.0.2","draft":false,"prerelease":false},
{"tag_name":"v1.1.0","draft":false,"prerelease":false}]`)
	}))
	got, err := c.Latest(context.Background(), true)
	if err != nil || got.Version != "v1.1.0" {
		t.Fatalf("release=%+v err=%v", got, err)
	}
}

func TestLatestFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"malformed JSON", 200, "{"}, {"forbidden", 403, "no"}, {"rate limited", 429, "slow"}, {"server", 503, "down"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) }))
			if _, err := c.Latest(context.Background(), true); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	t.Run("oversized", func(t *testing.T) {
		c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, strings.Repeat("x", MaxResponseSize+1))
		}))
		if _, err := c.Latest(context.Background(), true); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(100 * time.Millisecond)
			io.WriteString(w, `[]`)
		}))
		c.HTTP.Timeout = time.Millisecond
		if _, err := c.Latest(context.Background(), true); err == nil {
			t.Fatal("expected timeout")
		}
	})
}

func TestCacheHitExpiredAndCorrupt(t *testing.T) {
	now := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	calls := 0
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; io.WriteString(w, `[{"tag_name":"v1.0.2"}]`) }))
	c.Now = func() time.Time { return now }
	if _, err := c.Latest(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Latest(context.Background(), false); err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	c.Now = func() time.Time { return now.Add(25 * time.Hour) }
	if _, err := c.Latest(context.Background(), false); err != nil || calls != 2 {
		t.Fatalf("expired calls=%d err=%v", calls, err)
	}
	if err := os.WriteFile(c.CachePath, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Latest(context.Background(), false); err != nil || calls != 3 {
		t.Fatalf("corrupt calls=%d err=%v", calls, err)
	}
}

func TestDetectGoInstallation(t *testing.T) {
	sep := string(os.PathListSeparator)
	for _, tc := range []struct{ name, goos, exe, gobin, gopath, want string }{
		{"linux GOBIN", "linux", "/opt/go-bin/dorker", "/opt/go-bin", "", "go"},
		{"mac GOPATH", "darwin", "/Users/a/go/bin/dorker", "", "/Users/a/go", "go"},
		{"windows GOPATH", "windows", `C:\Users\a\go\bin\dorker.exe`, "", `C:\Users\a\go`, "go"},
		{"unknown", "linux", "/usr/local/bin/dorker", "", "/home/a/go" + sep + "/work/go", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectGoInstallation(tc.exe, "go", tc.gobin, tc.gopath, tc.goos)
			if got.Method != tc.want {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return &Client{HTTP: s.Client(), Endpoint: s.URL, CachePath: filepath.Join(t.TempDir(), "update.json"), TTL: DefaultTTL, Now: time.Now}
}

func TestNetworkError(t *testing.T) {
	c := &Client{HTTP: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}, Endpoint: "https://api.github.com/x", CachePath: filepath.Join(t.TempDir(), "x")}
	if _, err := c.Latest(context.Background(), true); err == nil {
		t.Fatal("expected error")
	}
}

func TestProductionRedirectPolicy(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "update.json"), "Dorker/test", time.Second)
	good, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/HexSilence/Dorker/releases", nil)
	bad, _ := http.NewRequest(http.MethodGet, "https://example.test/releases", nil)
	via, _ := http.NewRequest(http.MethodGet, DefaultEndpoint, nil)
	if err := c.HTTP.CheckRedirect(good, []*http.Request{via}); err != nil {
		t.Fatal(err)
	}
	if err := c.HTTP.CheckRedirect(bad, []*http.Request{via}); err == nil {
		t.Fatal("cross-host redirect accepted")
	}
}
