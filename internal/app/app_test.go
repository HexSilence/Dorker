package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/HexSilence/dorker/internal/config"
	"github.com/HexSilence/dorker/internal/discovery"
	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/scheduler"
	"github.com/HexSilence/dorker/internal/store"
	"gopkg.in/yaml.v3"
)

func TestProviderDiagnosticsNeverPrintSecrets(t *testing.T) {
	secrets := []struct{ name, value string }{
		{"GOOGLE_API_KEY", "SUPER_SECRET_GOOGLE_KEY"},
		{"GOOGLE_CSE_ID", "SUPER_SECRET_GOOGLE_CSE"},
		{"BRAVE_API_KEY", "SUPER_SECRET_BRAVE_KEY"},
		{"SERPAPI_API_KEY", "SUPER_SECRET_SERPAPI_KEY"},
		{"SEARXNG_TOKEN", "SUPER_SECRET_SEARXNG_TOKEN"},
	}
	for _, secret := range secrets {
		t.Setenv(secret.name, secret.value)
	}
	var out, stderr bytes.Buffer
	if code := Run([]string{"providers"}, IO{In: strings.NewReader(""), Out: &out, Err: &stderr}); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	text := out.String() + stderr.String()
	for _, secret := range secrets {
		if strings.Contains(text, secret.value) {
			t.Fatalf("secret %s leaked", secret.name)
		}
	}
}

func TestNoArgumentAndHelpPresentation(t *testing.T) {
	t.Setenv("DORKER_CONFIG", filepath.Join(t.TempDir(), "dorker", "config.yaml"))
	var out, errout bytes.Buffer
	if code := Run(nil, IO{In: strings.NewReader(""), Out: &out, Err: &errout}); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(errout.String(), "Quick start") || strings.Contains(errout.String(), "Usage of") {
		t.Fatal(errout.String())
	}
	out.Reset()
	errout.Reset()
	if code := Run([]string{"--help"}, IO{Out: &out, Err: &errout}); code != 0 {
		t.Fatal(code)
	}
	for _, want := range []string{"USAGE", "COMMANDS", "EXAMPLES", "Explain or validate queries"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestNoArgumentBootstrapsConfigWithoutOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "config.yaml")
	t.Setenv("DORKER_CONFIG", path)
	var out, stderr bytes.Buffer
	if code := Run(nil, IO{Out: &out, Err: &stderr}); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	first, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(first), "database:") {
		t.Fatalf("config=%q err=%v", first, err)
	}
	if err = os.WriteFile(path, []byte("# user config\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := Run(nil, IO{Out: &out, Err: &stderr}); code != 0 {
		t.Fatalf("second code=%d stderr=%s", code, stderr.String())
	}
	got, _ := os.ReadFile(path)
	if string(got) != "# user config\n" {
		t.Fatalf("existing config overwritten: %q", got)
	}
}

func TestDoctorCleanMachineIsActionableAndDoesNotLeakSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	t.Setenv("BRAVE_API_KEY", "DO_NOT_PRINT_THIS_SECRET")
	var out, stderr bytes.Buffer
	if code := Run([]string{"doctor", "--config", path}, IO{Out: &out, Err: &stderr}); code != 0 {
		t.Fatalf("code=%d stderr=%s out=%s", code, stderr.String(), out.String())
	}
	text := out.String() + stderr.String()
	for _, want := range []string{"DORKER / DOCTOR", "Version", "Platform", "Config", "Database", "Output dir", "Providers"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	if strings.Contains(text, "DO_NOT_PRINT_THIS_SECRET") {
		t.Fatalf("secret leaked: %s", text)
	}
}

func TestContainerRuntimeGuidanceByPlatform(t *testing.T) {
	for goos, want := range map[string]string{"linux": "package manager", "darwin": "Homebrew", "windows": "winget"} {
		got := containerRuntimeGuidance(goos)
		if !strings.Contains(got, want) || !strings.Contains(got, "will not") {
			t.Fatalf("%s guidance: %s", goos, got)
		}
	}
}

func TestSearchHelpSections(t *testing.T) {
	var out, errout bytes.Buffer
	if code := Run([]string{"search", "--help"}, IO{Out: &out, Err: &errout}); code != 0 {
		t.Fatal(code)
	}
	text := out.String()
	for _, want := range []string{"INPUT", "PROVIDERS", "LIMITS", "VALIDATION", "OUTPUT", "RECOVERY", "DISPLAY", "--quiet", "--no-banner", "--collapse MODE"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	if strings.Contains(text, "Usage of") {
		t.Fatal(text)
	}
	if errout.Len() != 0 {
		t.Fatalf("help wrote to stderr: %s", errout.String())
	}
}

func TestCommandHelpReturnsSuccess(t *testing.T) {
	for _, args := range [][]string{{"providers", "--help"}, {"providers", "test", "--help"}, {"query", "--help"}, {"query", "validate", "--help"}, {"jobs", "--help"}, {"stats", "--help"}, {"export", "--help"}, {"config", "--help"}, {"config", "init", "--help"}, {"config", "check", "--help"}, {"version", "--help"}, {"setup", "searxng", "--help"}} {
		var out, stderr bytes.Buffer
		if code := Run(args, IO{Out: &out, Err: &stderr}); code != 0 {
			t.Fatalf("%v code=%d stderr=%s", args, code, stderr.String())
		}
		if !strings.Contains(out.String(), "Usage:") || stderr.Len() != 0 {
			t.Fatalf("%v out=%q stderr=%q", args, out.String(), stderr.String())
		}
	}
}

func TestInteractiveBannerControlsAndQuiet(t *testing.T) {
	interactive := true
	run := func(extra ...string) (string, string) {
		var out, errout bytes.Buffer
		dir := t.TempDir()
		args := append([]string{"search", "-q", "example", "--provider", "mock", "--max-results", "1", "--max-pages", "1", "--stream", "--db", filepath.Join(dir, "x.db"), "--config", filepath.Join(dir, "missing.yaml")}, extra...)
		if code := Run(args, IO{In: strings.NewReader(""), Out: &out, Err: &errout, Interactive: &interactive}); code != 0 {
			t.Fatalf("code=%d stderr=%s", code, errout.String())
		}
		return out.String(), errout.String()
	}
	stdout, stderr := run()
	if !strings.Contains(stderr, "DORKER / SEARCH") || strings.Contains(stderr, "D O R K E R") || strings.Contains(stdout, "D O R K E R") || strings.Contains(stdout, "\x1b[") {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
	_, stderr = run("--no-banner")
	if strings.Contains(stderr, "D O R K E R") || !strings.Contains(stderr, "DORKER / SEARCH") {
		t.Fatal("--no-banner ignored")
	}
	_, stderr = run("--quiet")
	if stderr != "" {
		t.Fatalf("quiet stderr=%q", stderr)
	}
}

func TestRedirectedStreamingStdoutIsCleanAndQueryQUnchanged(t *testing.T) {
	var out, errout bytes.Buffer
	dir := t.TempDir()
	autoDir := filepath.Join(dir, "out")
	if code := Run([]string{"search", "-q", "example", "--provider", "mock", "--max-results", "2", "--max-pages", "1", "--stream", "--output-dir", autoDir, "--db", filepath.Join(dir, "x.db"), "--config", filepath.Join(dir, "missing.yaml")}, IO{In: strings.NewReader(""), Out: &out, Err: &errout}); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errout.String())
	}
	if strings.Contains(out.String(), "DORKER") || strings.Contains(out.String(), "\x1b[") {
		t.Fatal(out.String())
	}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if !strings.HasPrefix(line, "https://") {
			t.Fatalf("unclean stream line %q", line)
		}
	}
	if _, err := os.Stat(autoDir); !os.IsNotExist(err) {
		t.Fatalf("pipeline created automatic output: %v", err)
	}
}

func TestRedirectedStdoutAutomaticallyExportsCleanResults(t *testing.T) {
	for _, format := range []string{"txt", "jsonl", "csv"} {
		t.Run(format, func(t *testing.T) {
			var out, errout bytes.Buffer
			dir := t.TempDir()
			autoDir := filepath.Join(dir, "out")
			args := []string{"search", "-q", "example", "--provider", "mock", "--max-results", "2", "--max-pages", "1", "--format", format, "--output-dir", autoDir, "--db", filepath.Join(dir, "x.db"), "--config", filepath.Join(dir, "missing.yaml")}
			if code := Run(args, IO{In: strings.NewReader(""), Out: &out, Err: &errout}); code != 0 {
				t.Fatalf("code=%d stderr=%s", code, errout.String())
			}
			if out.Len() == 0 {
				t.Fatal("redirected stdout is empty")
			}
			for _, forbidden := range []string{"D O R K E R", "DORKER / SEARCH", "Searching...", "Search complete", "Results", "Provider", "Saved", "INFO", "\x1b["} {
				if strings.Contains(out.String(), forbidden) {
					t.Fatalf("stdout contains %q: %s", forbidden, out.String())
				}
			}
			if _, err := os.Stat(autoDir); !os.IsNotExist(err) {
				t.Fatalf("redirect created automatic output: %v", err)
			}
			switch format {
			case "txt":
				for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
					if !strings.HasPrefix(line, "https://") {
						t.Fatalf("unclean line %q", line)
					}
				}
			case "jsonl":
				if !strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
					t.Fatal(out.String())
				}
			case "csv":
				if !strings.HasPrefix(out.String(), "url,") {
					t.Fatal(out.String())
				}
			}
		})
	}
}

func TestRedirectedCollapseIsCleanAndDoesNotChangeStoredUnique(t *testing.T) {
	for _, mode := range []string{"host", "domain"} {
		t.Run(mode, func(t *testing.T) {
			var out, errout bytes.Buffer
			dir := t.TempDir()
			db := filepath.Join(dir, "x.db")
			args := []string{"search", "-q", "example", "--provider", "mock", "--max-results", "5", "--max-pages", "1", "--collapse", mode, "--db", db, "--config", filepath.Join(dir, "missing.yaml")}
			if code := Run(args, IO{In: strings.NewReader(""), Out: &out, Err: &errout}); code != 0 {
				t.Fatalf("code=%d stderr=%s", code, errout.String())
			}
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			if len(lines) != 1 || !strings.HasPrefix(lines[0], "https://") || strings.Contains(out.String(), "\x1b[") {
				t.Fatalf("unclean collapsed output %q", out.String())
			}
			st, err := store.Open(db)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			jobs, err := st.Jobs(context.Background())
			if err != nil || len(jobs) != 1 {
				t.Fatalf("jobs=%v err=%v", jobs, err)
			}
			stats, err := st.Stats(context.Background(), jobs[0].ID)
			if err != nil || stats.Unique != 5 {
				t.Fatalf("unique=%d err=%v", stats.Unique, err)
			}
		})
	}
}

func TestExplicitOutputTakesPrecedenceOverAutoStdout(t *testing.T) {
	var out, errout bytes.Buffer
	dir := t.TempDir()
	destination := filepath.Join(dir, "results.txt")
	autoDir := filepath.Join(dir, "out")
	args := []string{"search", "-q", "example", "--provider", "mock", "--max-results", "1", "--max-pages", "1", "-o", destination, "--output-dir", autoDir, "--db", filepath.Join(dir, "x.db"), "--config", filepath.Join(dir, "missing.yaml")}
	if code := Run(args, IO{In: strings.NewReader(""), Out: &out, Err: &errout}); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errout.String())
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected stdout: %q", out.String())
	}
	b, err := os.ReadFile(destination)
	if err != nil || len(b) == 0 {
		t.Fatalf("output=%q err=%v", b, err)
	}
	if _, err := os.Stat(autoDir); !os.IsNotExist(err) {
		t.Fatalf("explicit output also created automatic output: %v", err)
	}
}

func TestInteractiveSearchAutomaticallySavesWithoutDumpingURLs(t *testing.T) {
	interactive := true
	var out, errout bytes.Buffer
	dir := t.TempDir()
	args := []string{"search", "-q", "example", "--provider", "mock", "--max-results", "1", "--max-pages", "1", "--output-dir", filepath.Join(dir, "out"), "--db", filepath.Join(dir, "x.db"), "--config", filepath.Join(dir, "missing.yaml")}
	if code := Run(args, IO{In: strings.NewReader(""), Out: &out, Err: &errout, Interactive: &interactive}); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errout.String())
	}
	if out.Len() != 0 {
		t.Fatalf("interactive stdout=%q", out.String())
	}
	if !strings.Contains(errout.String(), "Search complete") {
		t.Fatal(errout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "search", "example", "results.txt")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errout.String(), "Saved") {
		t.Fatal(errout.String())
	}
}

func TestAutomaticOutputFormatsRepeatedAndEmpty(t *testing.T) {
	interactive := true
	for _, format := range []string{"txt", "jsonl", "csv"} {
		t.Run(format, func(t *testing.T) {
			dir := t.TempDir()
			args := []string{"search", "-q", "site:example.com", "--provider", "mock", "--validation", "off", "--max-results", "2", "--max-pages", "1", "--format", format, "--output-dir", filepath.Join(dir, "out"), "--db", filepath.Join(dir, "x.db"), "--config", filepath.Join(dir, "missing.yaml")}
			for run := 1; run <= 3; run++ {
				var out, errout bytes.Buffer
				if code := Run(args, IO{In: strings.NewReader(""), Out: &out, Err: &errout, Interactive: &interactive}); code != 0 {
					t.Fatalf("run %d code=%d stderr=%s", run, code, errout.String())
				}
				name := "results." + format
				if run == 2 {
					name = "results-2." + format
				} else if run == 3 {
					name = "results-3." + format
				}
				if _, err := os.Stat(filepath.Join(dir, "out", "search", "site-example.com", name)); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	t.Run("empty", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("DORKER_MOCK_RESULTS", "0")
		var out, errout bytes.Buffer
		args := []string{"search", "-q", "empty", "--provider", "mock", "--validation", "off", "--max-pages", "1", "--output-dir", filepath.Join(dir, "out"), "--db", filepath.Join(dir, "x.db"), "--config", filepath.Join(dir, "missing.yaml")}
		if code := Run(args, IO{In: strings.NewReader(""), Out: &out, Err: &errout, Interactive: &interactive}); code != 0 {
			t.Fatalf("code=%d stderr=%s", code, errout.String())
		}
		if _, err := os.Stat(filepath.Join(dir, "out", "search", "empty", "results.txt")); !os.IsNotExist(err) {
			t.Fatalf("empty automatic file exists: %v", err)
		}
		if !strings.Contains(errout.String(), "No results found") {
			t.Fatal(errout.String())
		}
	})
}

func TestInteractiveGHDBAutomaticOutputDomainAndGlobal(t *testing.T) {
	interactive := true
	for _, tc := range []struct {
		name, domain string
		path         []string
	}{{"domain", "Example.COM", []string{"example.com", "directory-listings", "results.csv"}}, {"global", "", []string{"ghdb", "directory-listings", "results.csv"}}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := filepath.Join(dir, "config.yaml")
			contents := "database:\n  path: " + filepath.Join(dir, "x.db") + "\nvalidation:\n  mode: off\noutput:\n  collapse: none\n  directory: " + filepath.Join(dir, "out") + "\nproviders:\n  mock:\n    enabled: true\n    requests_per_second: 100\n    burst: 10\n"
			if err := os.WriteFile(cfg, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			var out, errout bytes.Buffer
			input := "5\n" + tc.domain + "\ny\n"
			// Config is selected through the environment because interactive GHDB forwards no flags.
			t.Setenv("DORKER_CONFIG", cfg)
			if code := Run([]string{"ghdb"}, IO{In: strings.NewReader(input), Out: &out, Err: &errout, Interactive: &interactive}); code != 0 {
				t.Fatalf("code=%d stderr=%s", code, errout.String())
			}
			path := filepath.Join(append([]string{filepath.Join(dir, "out")}, tc.path...)...)
			// Interactive GHDB defaults to txt.
			path = strings.TrimSuffix(path, ".csv") + ".txt"
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			if !strings.Contains(out.String(), "Target domain (optional)\n  Leave empty to search globally.\n\n> ") {
				t.Fatalf("missing global-search guidance: %q", out.String())
			}
		})
	}
}

func TestConfigInitRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dorker", "config.yaml")
	var out, errout bytes.Buffer
	if code := Run([]string{"config", "init", "--config", path}, IO{Out: &out, Err: &errout}); code != 0 {
		t.Fatal(errout.String())
	}
	if code := Run([]string{"config", "init", "--config", path}, IO{Out: &out, Err: &errout}); code != 2 || !strings.Contains(errout.String(), "already exists") {
		t.Fatalf("code=%d %s", code, errout.String())
	}
}

func TestCanonicalVersionFlag(t *testing.T) {
	var out bytes.Buffer
	if code := Run([]string{"--version"}, IO{Out: &out, Err: &out}); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(out.String(), "v1.0.0") {
		t.Fatal(out.String())
	}
}

func TestFinalJobStatuses(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		report scheduler.Report
		unique int64
		want   string
		code   int
	}{
		{"complete", nil, scheduler.Report{}, 1, "COMPLETE", 0},
		{"degraded", nil, scheduler.Report{Degraded: true}, 1, "COMPLETE_DEGRADED", 0},
		{"saturated", nil, scheduler.Report{Saturated: true}, 1, "SATURATED", 0},
		{"partial", nil, scheduler.Report{Partial: true, ErrorPages: 1}, 1, "PARTIAL", 0},
		{"failed", nil, scheduler.Report{Partial: true, ErrorPages: 1}, 0, "FAILED", 1},
		{"fatal", fmt.Errorf("boom"), scheduler.Report{}, 0, "FAILED", 1},
		{"interrupted", context.Canceled, scheduler.Report{}, 0, "INTERRUPTED", 130},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, code := finalJobStatus(tc.err, tc.report, tc.unique)
			if got != tc.want || code != tc.code {
				t.Fatalf("got %s/%d want %s/%d", got, code, tc.want, tc.code)
			}
		})
	}
}

func TestProviderTestRendering(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[{"url":"https://example.com","engines":["bing"]}],"unresponsive_engines":[["brave","too many requests"]]}`)
	}))
	defer s.Close()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	cfg := config.Default()
	providerConfig := cfg.Providers["searxng"]
	providerConfig.Instances = []config.SearXNGInstanceConfig{{Name: "local", URL: s.URL, RequestsPerSecond: 1, Burst: 1}}
	cfg.Providers["searxng"] = providerConfig
	b, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	var out, errout bytes.Buffer
	if code := Run([]string{"providers", "test", "searxng", "--config", configPath}, IO{Out: &out, Err: &errout}); code != 0 {
		t.Fatalf("%d %s", code, errout.String())
	}
	for _, want := range []string{"Status", "Ready", "Instance", "local", "bing", "Responsive", "brave", "Rate limited"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
}

func TestConfigCheckDoesNotPrintSecretsOrMakeRequests(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "SUPER_SECRET_GOOGLE_KEY")
	t.Setenv("GOOGLE_CSE_ID", "SUPER_SECRET_GOOGLE_CSE")
	t.Setenv("BRAVE_API_KEY", "SUPER_SECRET_BRAVE_KEY")
	t.Setenv("SERPAPI_API_KEY", "SUPER_SECRET_SERPAPI_KEY")
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	if err = os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	var out, stderr bytes.Buffer
	db := filepath.Join(tmp, "check.db")
	if code := Run([]string{"config", "check", "--db", db}, IO{In: strings.NewReader(""), Out: &out, Err: &stderr}); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	text := out.String() + stderr.String()
	if strings.Contains(text, "SUPER_SECRET") {
		t.Fatalf("secret leaked: %s", text)
	}
	if !strings.Contains(text, "credentials present") || !strings.Contains(text, "database writable") {
		t.Fatalf("unexpected diagnostics: %s", text)
	}
}

func TestQueryDiagnosticsAreOffline(t *testing.T) {
	for _, args := range [][]string{{"query", "explain", "site:.edu ext:pdf"}, {"query", "validate", "-q", "site:.edu ext:pdf", "-u", "https://mit.edu/report.pdf"}, {"query", "validate", "-q", "site:.edu ext:pdf", "-u", "https://mit.edu/download?id=1", "--validation", "strict"}} {
		var out, stderr bytes.Buffer
		if code := Run(args, IO{In: strings.NewReader(""), Out: &out, Err: &stderr}); code != 0 {
			t.Fatalf("%v code=%d stderr=%s", args, code, stderr.String())
		}
		if !strings.Contains(out.String(), "ext") && !strings.Contains(out.String(), "extension") {
			t.Fatalf("unexpected output %s", out.String())
		}
	}
}

func TestQueryExplainPresentation(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := Run([]string{"query", "explain", "site:example.com intitle:admin -inurl:login"}, IO{Out: &out, Err: &stderr}); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{"Query", "Checked locally", "site", "example.com", "URL excludes", "login", "Left to provider", "intitle:admin"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestQueryValidatePresentation(t *testing.T) {
	tests := []struct {
		name, url, mode, result, reason string
	}{
		{"match", "https://example.com/report.pdf", "conservative", "Match", ""},
		{"mismatch", "https://example.com/report.txt", "conservative", "Mismatch", "different file extension"},
		{"unknown", "https://example.com/download?id=1", "conservative", "Unknown", "doesn't expose enough information"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			args := []string{"query", "validate", "-q", "site:example.com ext:pdf", "-u", tc.url, "--validation", tc.mode}
			if code := Run(args, IO{Out: &out, Err: &stderr}); code != 0 {
				t.Fatalf("code=%d stderr=%s", code, stderr.String())
			}
			for _, want := range []string{"Query", "URL", "Result", tc.result} {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("missing %q in:\n%s", want, out.String())
				}
			}
			if tc.reason != "" && !strings.Contains(out.String(), tc.reason) {
				t.Fatalf("missing reason %q in:\n%s", tc.reason, out.String())
			}
		})
	}
}

func TestNaturalValidationError(t *testing.T) {
	var out, stderr bytes.Buffer
	code := Run([]string{"query", "validate", "-q", "site:example.com", "-u", "https://example.com", "--validation", "foo"}, IO{Out: &out, Err: &stderr})
	if code != 2 {
		t.Fatalf("code=%d", code)
	}
	for _, want := range []string{`error: invalid validation mode "foo"`, "expected: off, conservative, strict"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("missing %q in %s", want, stderr.String())
		}
	}
}

func TestDiscoverHostsOnlyRedirectedStdoutIsClean(t *testing.T) {
	dir := t.TempDir()
	var out, errout bytes.Buffer
	interactive := false
	code := Run([]string{"discover", "-q", "site:example.com", "--provider", "mock", "--hosts-only", "--max-depth", "1", "--max-results", "3", "--max-results-per-query", "3", "--max-pages", "1", "--db", filepath.Join(dir, "d.db"), "--config", filepath.Join(dir, "missing.yaml")}, IO{Out: &out, Err: &errout, Interactive: &interactive})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errout.String())
	}
	if got := out.String(); got != "example.com\n" {
		t.Fatalf("stdout=%q", got)
	}
	for _, bad := range []string{"DORKER", "Depth", "Discovery", "Searching", "\x1b[", "INFO"} {
		if strings.Contains(out.String(), bad) {
			t.Fatalf("stdout contains %q: %q", bad, out.String())
		}
	}
}

func TestDiscoveryCoverageDiagnosticsUseStderrData(t *testing.T) {
	result := discovery.Result{Status: "PARTIAL", StopReason: discovery.StopFrontierDegraded, Stats: []store.DiscoveryDepthStat{{Depth: 0, Trustworthy: false}}}
	stats := model.Stats{BySourceEngine: map[string]int64{"wiby": 2, "bing": 4}, UpstreamDegradation: []model.UpstreamEngineStat{{Engine: "google", Classification: "access_denied"}, {Engine: "brave", Classification: "rate_limited"}, {Engine: "google", Classification: "challenge"}}}
	status, terminalEvidence, detail, historical, responsive, degraded := discoveryCoverageSummary(result, stats)
	if terminalEvidence != "INSUFFICIENT" || !historical {
		t.Fatalf("terminal=%s historical=%v", terminalEvidence, historical)
	}
	var stdout, stderr bytes.Buffer
	writeDiscoveryCoverageDiagnostics(&stderr, status, detail, responsive, degraded)
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q", stdout.String())
	}
	for _, want := range []string{"Coverage          degraded", "Responsive sources observed  bing, wiby", "Degraded sources  brave, google", "frontier exhausted without trustworthy saturation evidence"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("missing %q in %s", want, stderr.String())
		}
	}
}

func TestDiscoveryTerminalEvidenceInvariant(t *testing.T) {
	tests := []struct {
		name     string
		result   discovery.Result
		coverage string
		evidence string
		history  bool
	}{
		{"unresolved", discovery.Result{Status: "PARTIAL", StopReason: discovery.StopFrontierDegraded, Stats: []store.DiscoveryDepthStat{{Trustworthy: false}, {Trustworthy: false}}}, "DEGRADED", "INSUFFICIENT", true},
		{"recovered", discovery.Result{Status: "SATURATED", StopReason: discovery.StopSaturated, Stats: []store.DiscoveryDepthStat{{Trustworthy: false}, {Trustworthy: true}}}, "DEGRADED", "SUFFICIENT", true},
		{"healthy", discovery.Result{Status: "SATURATED", StopReason: discovery.StopSaturated, Stats: []store.DiscoveryDepthStat{{Trustworthy: true}}}, "TRUSTWORTHY", "SUFFICIENT", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			coverage, evidence, _, history, _, _ := discoveryCoverageSummary(tc.result, model.Stats{})
			if coverage != tc.coverage || evidence != tc.evidence || history != tc.history {
				t.Fatalf("coverage=%s evidence=%s history=%v", coverage, evidence, history)
			}
			if tc.result.Status == "PARTIAL" && tc.result.StopReason == discovery.StopSaturated {
				t.Fatal("test fixture violates terminal invariant")
			}
		})
	}
}

func TestDiscoverInteractiveWritesArtifacts(t *testing.T) {
	dir := t.TempDir()
	var out, errout bytes.Buffer
	interactive := true
	root := filepath.Join(dir, "out")
	code := Run([]string{"discover", "-q", "site:example.com", "--provider", "mock", "--max-depth", "0", "--max-results", "2", "--max-results-per-query", "2", "--max-pages", "1", "--output-dir", root, "--db", filepath.Join(dir, "d.db"), "--config", filepath.Join(dir, "missing.yaml")}, IO{Out: &out, Err: &errout, Interactive: &interactive})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errout.String())
	}
	base := filepath.Join(root, "discover", "site-example.com")
	for _, name := range []string{"urls.txt", "hosts.txt", "roots.txt", "discovery.json"} {
		if _, err := os.Stat(filepath.Join(base, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDiscoverResumeReusesOutputDirectory(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "d.db")
	root := filepath.Join(dir, "out")
	interactive := true
	args := []string{"discover", "-q", "site:example.com", "--provider", "mock", "--max-depth", "0", "--max-results", "1", "--max-results-per-query", "1", "--max-pages", "1", "--output-dir", root, "--db", db, "--config", filepath.Join(dir, "missing.yaml")}
	if code := Run(args, IO{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, Interactive: &interactive}); code != 0 {
		t.Fatalf("discover code=%d", code)
	}
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := s.Jobs(context.Background())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs=%v err=%v", jobs, err)
	}
	job := jobs[0].ID
	dj, err := s.DiscoveryJob(context.Background(), job)
	_ = s.Close()
	if err != nil || dj.OutputPath == "" {
		t.Fatalf("discovery job=%+v err=%v", dj, err)
	}
	for i := 0; i < 2; i++ {
		var out, stderr bytes.Buffer
		if code := Run([]string{"resume", job, "--db", db, "--config", filepath.Join(dir, "missing.yaml")}, IO{Out: &out, Err: &stderr, Interactive: &interactive}); code != 0 {
			t.Fatalf("resume %d code=%d stderr=%s", i, code, stderr.String())
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "discover"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || filepath.Join(root, "discover", entries[0].Name()) != dj.OutputPath {
		t.Fatalf("entries=%v output=%s", entries, dj.OutputPath)
	}
}

func TestDiscoverProviderUnavailableDoesNotLeaveRunningJob(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "d.db")
	var out, stderr bytes.Buffer
	if code := Run([]string{"discover", "-q", "site:example.com", "--db", db, "--config", filepath.Join(dir, "missing.yaml")}, IO{Out: &out, Err: &stderr}); code != 3 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	jobs, err := s.Jobs(context.Background())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs=%v err=%v", jobs, err)
	}
	if jobs[0].Status != "FAILED" {
		t.Fatalf("status=%s", jobs[0].Status)
	}
}

func TestDiscoverExplicitOutputWinsWhenRedirected(t *testing.T) {
	dir := t.TempDir()
	var out, errout bytes.Buffer
	interactive := false
	dest := filepath.Join(dir, "artifacts")
	code := Run([]string{"discover", "-q", "site:example.com", "--provider", "mock", "--max-depth", "0", "--max-results", "1", "--max-results-per-query", "1", "--max-pages", "1", "-o", dest, "--db", filepath.Join(dir, "d.db"), "--config", filepath.Join(dir, "missing.yaml")}, IO{Out: &out, Err: &errout, Interactive: &interactive})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errout.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout=%q", out.String())
	}
	if _, err := os.Stat(filepath.Join(dest, "discovery.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out")); !os.IsNotExist(err) {
		t.Fatalf("unexpected automatic output: %v", err)
	}
}

func TestDiscoverZeroResultsCreatesNoAutomaticArtifacts(t *testing.T) {
	t.Setenv("DORKER_MOCK_RESULTS", "0")
	dir := t.TempDir()
	var out, errout bytes.Buffer
	interactive := true
	root := filepath.Join(dir, "out")
	code := Run([]string{"discover", "-q", "site:example.com", "--provider", "mock", "--max-depth", "0", "--max-pages", "1", "--output-dir", root, "--db", filepath.Join(dir, "d.db"), "--config", filepath.Join(dir, "missing.yaml")}, IO{Out: &out, Err: &errout, Interactive: &interactive})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errout.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("automatic root created: %v", err)
	}
}

func TestGHDBListShowFindAndInvalidID(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		wants []string
	}{
		{[]string{"ghdb", "list"}, []string{"GHDB", "1.", "PDF documents", "25.", "Documentation sites"}},
		{[]string{"ghdb", "show", "1"}, []string{"PDF documents", "Documents", "ext:pdf"}},
		{[]string{"ghdb", "find", "login"}, []string{"Login pages"}},
		{[]string{"ghdb", "list", "--category", "documents"}, []string{"PDF documents", "Office documents", "Documentation sites"}},
	} {
		var out, stderr bytes.Buffer
		if code := Run(tc.args, IO{In: strings.NewReader(""), Out: &out, Err: &stderr}); code != 0 {
			t.Fatalf("%v code=%d err=%s", tc.args, code, stderr.String())
		}
		for _, want := range tc.wants {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("%v missing %q in %s", tc.args, want, out.String())
			}
		}
	}
	var out, stderr bytes.Buffer
	if code := Run([]string{"ghdb", "show", "999"}, IO{Out: &out, Err: &stderr}); code != 2 || !strings.Contains(stderr.String(), "not found") {
		t.Fatalf("code/output=%d %q", code, stderr.String())
	}
}

func TestGHDBInteractiveQuitEOFAndConfirmationNo(t *testing.T) {
	interactive := true
	for _, input := range []string{"q\n", "", "5\n\nn\n"} {
		var out, stderr bytes.Buffer
		code := Run([]string{"ghdb"}, IO{In: strings.NewReader(input), Out: &out, Err: &stderr, Interactive: &interactive})
		if code != 0 || !strings.Contains(out.String(), "Cancelled.") {
			t.Fatalf("input=%q code=%d out=%q err=%q", input, code, out.String(), stderr.String())
		}
	}
}

func TestGHDBInteractiveConfirmationYesUsesSearchPath(t *testing.T) {
	t.Setenv("DORKER_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	interactive := true
	var out, stderr bytes.Buffer
	// Entry 5 plus an optional domain resolves to the normal query parser. No
	// provider is configured, so reaching the standard provider error proves the
	// confirmation handed control to runSearch rather than another implementation.
	code := Run([]string{"ghdb"}, IO{In: strings.NewReader("5\nexample.com\n\n"), Out: &out, Err: &stderr, Interactive: &interactive})
	if code != 3 || !strings.Contains(out.String(), `site:example.com intitle:"index of"`) || !strings.Contains(stderr.String(), "no configured provider") {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), stderr.String())
	}
}

func TestGHDBNonTTYDoesNotPrompt(t *testing.T) {
	var out, stderr bytes.Buffer
	code := Run([]string{"ghdb"}, IO{In: strings.NewReader("1\n"), Out: &out, Err: &stderr})
	if code != 2 || !strings.Contains(stderr.String(), "interactive GHDB needs a terminal") || !strings.Contains(stderr.String(), "dorker ghdb list") {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), stderr.String())
	}
}

func TestGHDBRunUsesExistingSearchAndForwardsFlags(t *testing.T) {
	var out, stderr bytes.Buffer
	dir := t.TempDir()
	db := filepath.Join(dir, "x.db")
	args := []string{"ghdb", "run", "1", "--domain", "example.com", "--provider", "mock", "--validation", "off", "--max-results", "2", "--db", db, "--config", filepath.Join(dir, "missing.yaml")}
	if code := Run(args, IO{In: strings.NewReader(""), Out: &out, Err: &stderr}); code != 0 {
		t.Fatalf("code=%d err=%s", code, stderr.String())
	}
	if lines := strings.Split(strings.TrimSpace(out.String()), "\n"); len(lines) != 2 {
		t.Fatalf("output=%q", out.String())
	}
	if strings.Contains(stderr.String(), "level=INFO") {
		t.Fatal(stderr.String())
	}
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	jobs, err := st.Jobs(context.Background())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs=%v err=%v", jobs, err)
	}
	variants, err := st.Variants(context.Background(), jobs[0].ID)
	if err != nil || len(variants) != 1 || variants[0].Original != "site:example.com ext:pdf" {
		t.Fatalf("variants=%v err=%v", variants, err)
	}
}

func TestDefaultSearchLogsAreCleanAndInfoIsExplicit(t *testing.T) {
	for _, extra := range [][]string{nil, {"--stream"}, {"--quiet"}} {
		var out, stderr bytes.Buffer
		dir := t.TempDir()
		args := []string{"search", "-q", "example", "--provider", "mock", "--validation", "off", "--max-results", "2", "--db", filepath.Join(dir, "x.db"), "--config", filepath.Join(dir, "missing.yaml")}
		args = append(args, extra...)
		if code := Run(args, IO{In: strings.NewReader(""), Out: &out, Err: &stderr}); code != 0 {
			t.Fatalf("%v code=%d err=%s", extra, code, stderr.String())
		}
		for _, forbidden := range []string{"level=INFO", `msg="job started"`, `msg="job finished"`, "time="} {
			if strings.Contains(stderr.String(), forbidden) {
				t.Fatalf("%v contains %q: %s", extra, forbidden, stderr.String())
			}
		}
	}
	var out, stderr bytes.Buffer
	dir := t.TempDir()
	args := []string{"search", "-q", "example", "--provider", "mock", "--validation", "off", "--max-results", "1", "--log-level", "info", "--db", filepath.Join(dir, "x.db"), "--config", filepath.Join(dir, "missing.yaml")}
	if code := Run(args, IO{In: strings.NewReader(""), Out: &out, Err: &stderr}); code != 0 || !strings.Contains(stderr.String(), "level=INFO") {
		t.Fatalf("code=%d err=%s", code, stderr.String())
	}
}

func TestProgressLineHidesZeroCounters(t *testing.T) {
	if got := progressLine(28, 0, 0); got != "Searching... 28 unique" {
		t.Fatal(got)
	}
	if got := progressLine(63, 10, 0); got != "Searching... 63 unique · 10 rejected" {
		t.Fatal(got)
	}
	if got := progressLine(63, 10, 4); got != "Searching... 63 unique · 10 rejected · 4 duplicates" {
		t.Fatal(got)
	}
}

func TestSetupSearXNGPreservesInstancesAndIsIdempotent(t *testing.T) {
	originalLook, originalCommand, originalReady, originalPort := setupLookPath, setupCommand, setupReady, setupPortFree
	t.Cleanup(func() {
		setupLookPath, setupCommand, setupReady, setupPortFree = originalLook, originalCommand, originalReady, originalPort
	})
	setupLookPath = func(name string) (string, error) { return "/fake/" + name, nil }
	setupPortFree = func() bool { return true }
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	settingsPath := filepath.Join(dir, "searxng", "settings.yml")
	containerExists := false
	var operations []string
	setupCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		operations = append(operations, args[0])
		if args[0] == "inspect" && !containerExists {
			return nil, fmt.Errorf("not found")
		}
		if args[0] == "inspect" {
			return []byte(fmt.Sprintf(`[{"Config":{"Labels":{},"Image":%q},"State":{"Running":true},"NetworkSettings":{"Ports":{"8080/tcp":[{"HostIp":"127.0.0.1","HostPort":"8080"}]}},"Mounts":[{"Type":"bind","Source":%q,"Destination":"/etc/searxng/settings.yml","RW":false}]}]`, searxImage, settingsPath)), nil
		}
		if args[0] == "run" {
			containerExists = true
		}
		return []byte("ok"), nil
	}
	setupReady = func(context.Context) error { return nil }
	cfg := config.Default()
	pc := cfg.Providers["searxng"]
	pc.Strategy = "coverage"
	pc.Instances = []config.SearXNGInstanceConfig{{Name: "remote", URL: "https://search.example.test", TokenEnv: "REMOTE_TOKEN", RequestsPerSecond: 2, Burst: 3}}
	cfg.Providers["searxng"] = pc
	b, _ := yaml.Marshal(cfg)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		var out, stderr bytes.Buffer
		if code := setupSearXNG([]string{"--yes", "--config", path}, IO{Out: &out, Err: &stderr}); code != 0 {
			t.Fatalf("run %d code=%d stderr=%s", i, code, stderr.String())
		}
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	instances := got.Providers["searxng"].Instances
	if len(instances) != 2 || instances[0].Name != "remote" || instances[1].Name != "local" {
		t.Fatalf("instances=%+v", instances)
	}
	if got.Providers["searxng"].Strategy != "coverage" {
		t.Fatalf("strategy overwritten: %s", got.Providers["searxng"].Strategy)
	}
	if strings.Join(operations, ",") != "inspect,run,inspect" {
		t.Fatalf("operations=%v", operations)
	}
	if _, err = os.Stat(filepath.Join(dir, "searxng", "settings.yml")); err != nil {
		t.Fatal(err)
	}
}

func TestSearXNGReadiness(t *testing.T) {
	tests := []struct {
		name, contentType, body string
		status                  int
		ready                   bool
	}{
		{"results", "application/json", `{"results":[{"url":"https://example.test"}]}`, 200, true},
		{"empty results", "application/json", `{"results":[]}`, 200, true},
		{"unresponsive engines", "application/json", `{"results":[],"unresponsive_engines":[["google","timeout"]]}`, 200, true},
		{"rate limited upstream", "application/json", `{"results":[],"unresponsive_engines":[["brave","Too many requests"]]}`, 200, true},
		{"html", "text/html", `<html>ready</html>`, 200, false},
		{"malformed", "application/json", `{"results":`, 200, false},
		{"http error", "application/json", `{"results":[]}`, 503, false},
		{"array", "application/json", `[]`, 200, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			err := searXNGReady(context.Background(), srv.Client(), srv.URL, 1024)
			if (err == nil) != tt.ready {
				t.Fatalf("ready=%v err=%v", err == nil, err)
			}
		})
	}
}

func TestSearXNGReadinessTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := searXNGReady(ctx, srv.Client(), srv.URL, 1024); err == nil {
		t.Fatal("timeout reported ready")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestSearXNGReadinessConnectionRefused(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	})}
	if err := searXNGReady(context.Background(), client, searxReadinessURL, 1024); err == nil {
		t.Fatal("connection failure reported ready")
	}
}

func TestSetupSearXNGRejectsInconsistentOwnershipWithoutMutation(t *testing.T) {
	originalLook, originalCommand, originalReady := setupLookPath, setupCommand, setupReady
	t.Cleanup(func() { setupLookPath, setupCommand, setupReady = originalLook, originalCommand, originalReady })
	setupLookPath = func(name string) (string, error) { return "/fake/" + name, nil }
	actualSettings := filepath.Join(t.TempDir(), "actual", "searxng", "settings.yml")
	claimedSettings := filepath.Join(t.TempDir(), "claimed", "searxng", "settings.yml")
	var inspected []searxContainerInspect
	if err := json.Unmarshal(compatibleSearXNGInspect(actualSettings, true, map[string]string{searxSettingsLabel: claimedSettings}), &inspected); err != nil {
		t.Fatal(err)
	}
	inspectJSON, _ := json.Marshal(inspected)
	var operations []string
	setupCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		operations = append(operations, args[0])
		return inspectJSON, nil
	}
	setupReady = func(context.Context) error { t.Fatal("readiness must not run on ownership conflict"); return nil }
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := []byte("# preserve alternate config\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := setupSearXNG([]string{"--yes", "--config", path}, IO{Out: &out, Err: &stderr}); code == 0 {
		t.Fatal("ownership conflict succeeded")
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("config changed: %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "searxng")); !os.IsNotExist(err) {
		t.Fatalf("alternate settings directory created: %v", err)
	}
	if strings.Join(operations, ",") != "inspect" {
		t.Fatalf("container was mutated: %v", operations)
	}
	if !strings.Contains(stderr.String(), "inconsistent Dorker ownership metadata") {
		t.Fatalf("non-actionable error: %s", stderr.String())
	}
}

func TestSetupSearXNGReadinessFailureDoesNotChangeConfig(t *testing.T) {
	originalLook, originalCommand, originalReady, originalPort := setupLookPath, setupCommand, setupReady, setupPortFree
	t.Cleanup(func() {
		setupLookPath, setupCommand, setupReady, setupPortFree = originalLook, originalCommand, originalReady, originalPort
	})
	setupLookPath = func(name string) (string, error) { return "/fake/" + name, nil }
	setupPortFree = func() bool { return true }
	setupCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "inspect" {
			return nil, fmt.Errorf("not found")
		}
		return nil, nil
	}
	setupReady = func(context.Context) error { return fmt.Errorf("timeout") }
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("# untouched when readiness fails\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := setupSearXNG([]string{"--yes", "--config", path}, IO{Out: &out, Err: &stderr}); code == 0 {
		t.Fatal("readiness failure succeeded")
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("config changed: %q err=%v", got, err)
	}
}

func compatibleSearXNGInspect(settingsPath string, running bool, labels map[string]string) []byte {
	b, _ := json.Marshal([]searxContainerInspect{{}})
	var containers []searxContainerInspect
	_ = json.Unmarshal(b, &containers)
	c := &containers[0]
	c.Config.Image = searxImage
	c.Config.Labels = labels
	c.State.Running = running
	c.NetworkSettings.Ports = map[string][]struct {
		HostIP   string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	}{"8080/tcp": {{HostIP: "127.0.0.1", HostPort: "8080"}}}
	c.Mounts = append(c.Mounts, struct {
		Type        string `json:"Type"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	}{Type: "bind", Source: settingsPath, Destination: searxSettingsTarget})
	b, _ = json.Marshal(containers)
	return b
}

func withSetupFakes(t *testing.T) {
	t.Helper()
	originalLook, originalCommand, originalReady := setupLookPath, setupCommand, setupReady
	originalPort, originalProbe := setupPortFree, setupProbe
	t.Cleanup(func() {
		setupLookPath, setupCommand, setupReady = originalLook, originalCommand, originalReady
		setupPortFree, setupProbe = originalPort, originalProbe
	})
	setupLookPath = func(name string) (string, error) { return "/fake/" + name, nil }
	setupPortFree = func() bool { return true }
	setupProbe = func(context.Context, *http.Client, string, int64) error { return fmt.Errorf("not SearXNG") }
	setupReady = func(context.Context) error { return nil }
}

func TestSetupSearXNGPreviewDoesNotMutate(t *testing.T) {
	withSetupFakes(t)
	var operations []string
	setupCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		operations = append(operations, args[0])
		return nil, fmt.Errorf("not found")
	}
	dir := t.TempDir()
	var out, stderr bytes.Buffer
	if code := setupSearXNG([]string{"--config", filepath.Join(dir, "config.yaml")}, IO{Out: &out, Err: &stderr}); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if strings.Join(operations, ",") != "inspect" {
		t.Fatalf("mutating operations: %v", operations)
	}
	if _, err := os.Stat(filepath.Join(dir, "searxng")); !os.IsNotExist(err) {
		t.Fatalf("preview created files: %v", err)
	}
}

func TestSetupSearXNGStartsStoppedCompatibleContainer(t *testing.T) {
	withSetupFakes(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	settings := filepath.Join(dir, "searxng", "settings.yml")
	var operations []string
	setupCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		operations = append(operations, args[0])
		if args[0] == "inspect" {
			return compatibleSearXNGInspect(settings, false, nil), nil
		}
		return nil, nil
	}
	var out, stderr bytes.Buffer
	if code := setupSearXNG([]string{"--yes", "--config", path}, IO{Out: &out, Err: &stderr}); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if strings.Join(operations, ",") != "inspect,start" {
		t.Fatalf("operations=%v", operations)
	}
}

func TestSetupSearXNGRunningUnhealthyIsNotMutated(t *testing.T) {
	withSetupFakes(t)
	dir := t.TempDir()
	settings := filepath.Join(dir, "searxng", "settings.yml")
	setupReady = func(context.Context) error { return fmt.Errorf("HTTP 503") }
	var operations []string
	setupCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		operations = append(operations, args[0])
		return compatibleSearXNGInspect(settings, true, nil), nil
	}
	var out, stderr bytes.Buffer
	if code := setupSearXNG([]string{"--yes", "--config", filepath.Join(dir, "config.yaml")}, IO{Out: &out, Err: &stderr}); code == 0 {
		t.Fatal("unhealthy container succeeded")
	}
	if strings.Join(operations, ",") != "inspect" || !strings.Contains(stderr.String(), "dorker-searxng") || !strings.Contains(stderr.String(), "HTTP 503") {
		t.Fatalf("operations=%v stderr=%s", operations, stderr.String())
	}
}

func TestSetupSearXNGOccupiedPortConflicts(t *testing.T) {
	for _, healthy := range []bool{false, true} {
		t.Run(fmt.Sprintf("healthy=%v", healthy), func(t *testing.T) {
			withSetupFakes(t)
			setupPortFree = func() bool { return false }
			if healthy {
				setupProbe = func(context.Context, *http.Client, string, int64) error { return nil }
			}
			var operations []string
			setupCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
				operations = append(operations, args[0])
				return nil, fmt.Errorf("not found")
			}
			var out, stderr bytes.Buffer
			if code := setupSearXNG([]string{"--yes", "--config", filepath.Join(t.TempDir(), "config.yaml")}, IO{Out: &out, Err: &stderr}); code == 0 {
				t.Fatal("occupied port succeeded")
			}
			if strings.Join(operations, ",") != "inspect" || !strings.Contains(stderr.String(), "127.0.0.1:8080") {
				t.Fatalf("operations=%v stderr=%s", operations, stderr.String())
			}
			if healthy && !strings.Contains(stderr.String(), "external SearXNG") {
				t.Fatalf("missing external guidance: %s", stderr.String())
			}
		})
	}
}

func TestSetupSearXNGRuntimeBindRaceIsClassified(t *testing.T) {
	withSetupFakes(t)
	var operations []string
	setupCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		operations = append(operations, args[0])
		if args[0] == "inspect" {
			return nil, fmt.Errorf("not found")
		}
		return []byte("pasta failed: address already in use\nlarge runtime noise"), fmt.Errorf("exit 1")
	}
	var out, stderr bytes.Buffer
	if code := setupSearXNG([]string{"--yes", "--config", filepath.Join(t.TempDir(), "config.yaml")}, IO{Out: &out, Err: &stderr}); code == 0 {
		t.Fatal("bind race succeeded")
	}
	if strings.Join(operations, ",") != "inspect,run" || !strings.Contains(stderr.String(), "became unavailable") || strings.Contains(stderr.String(), "pasta failed") {
		t.Fatalf("operations=%v stderr=%s", operations, stderr.String())
	}
}

func TestSetupSearXNGRuntimePreference(t *testing.T) {
	for _, tt := range []struct {
		name, missing, want string
	}{{"podman preferred", "", "podman"}, {"docker fallback", "podman", "docker"}} {
		t.Run(tt.name, func(t *testing.T) {
			withSetupFakes(t)
			setupLookPath = func(name string) (string, error) {
				if name == tt.missing {
					return "", fmt.Errorf("missing")
				}
				return "/fake/" + name, nil
			}
			used := ""
			setupCommand = func(_ context.Context, runtimeName string, args ...string) ([]byte, error) {
				used = runtimeName
				return nil, fmt.Errorf("not found")
			}
			var out, stderr bytes.Buffer
			if code := setupSearXNG([]string{"--config", filepath.Join(t.TempDir(), "config.yaml")}, IO{Out: &out, Err: &stderr}); code != 0 {
				t.Fatalf("code=%d stderr=%s", code, stderr.String())
			}
			if used != tt.want {
				t.Fatalf("runtime=%s want=%s", used, tt.want)
			}
		})
	}
}

func TestWaitForSearXNGContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForSearXNG(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestLegacyContainerWithoutLabelIsRecognized(t *testing.T) {
	withSetupFakes(t)
	newHome := t.TempDir()
	path := filepath.Join(newHome, ".config", "dorker", "config.yaml")
	settings := filepath.Join(t.TempDir(), "old", "home", ".config", "dorker-searxng", "settings.yml")
	var legacy []searxContainerInspect
	if err := json.Unmarshal(compatibleSearXNGInspect(settings, true, nil), &legacy); err != nil {
		t.Fatal(err)
	}
	legacy[0].Config.Image = "ghcr.io/searxng/searxng:latest"
	legacyJSON, _ := json.Marshal(legacy)
	var operations []string
	setupCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		operations = append(operations, args[0])
		return legacyJSON, nil
	}
	for run := 0; run < 2; run++ {
		var out, stderr bytes.Buffer
		if code := setupSearXNG([]string{"--yes", "--config", path}, IO{Out: &out, Err: &stderr}); code != 0 {
			t.Fatalf("run=%d code=%d stderr=%s", run, code, stderr.String())
		}
		if strings.Contains(stderr.String(), "external") {
			t.Fatalf("legacy container classified as external: %s", stderr.String())
		}
	}
	if strings.Join(operations, ",") != "inspect,inspect" {
		t.Fatalf("operations=%v", operations)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	instances := cfg.Providers["searxng"].Instances
	if len(instances) != 1 || instances[0].URL != "http://127.0.0.1:8080" {
		t.Fatalf("instances=%+v", instances)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("mode=%v", info.Mode().Perm())
		}
	}
}

func TestLegacyContainerFingerprintRejectsIncompatibleContainers(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*searxContainerInspect)
		want   string
	}{
		{"unrelated image", func(c *searxContainerInspect) { c.Config.Image = "example.invalid/not-searxng:latest" }, "unexpected image"},
		{"non-loopback port", func(c *searxContainerInspect) { c.NetworkSettings.Ports["8080/tcp"][0].HostIP = "0.0.0.0" }, "does not publish only"},
		{"incompatible settings mount", func(c *searxContainerInspect) { c.Mounts[0].Destination = "/somewhere/settings.yml" }, "not managed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withSetupFakes(t)
			var containers []searxContainerInspect
			if err := json.Unmarshal(compatibleSearXNGInspect(filepath.Join(t.TempDir(), "old", "home", "settings.yml"), true, nil), &containers); err != nil {
				t.Fatal(err)
			}
			tt.mutate(&containers[0])
			inspectJSON, _ := json.Marshal(containers)
			setupCommand = func(context.Context, string, ...string) ([]byte, error) { return inspectJSON, nil }
			var out, stderr bytes.Buffer
			if code := setupSearXNG([]string{"--yes", "--config", filepath.Join(t.TempDir(), "config.yaml")}, IO{Out: &out, Err: &stderr}); code == 0 {
				t.Fatal("incompatible container succeeded")
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr=%s want=%q", stderr.String(), tt.want)
			}
		})
	}
}

func TestPodmanRuntimeUsesAccountHomeWhenApplicationHomeIsIsolated(t *testing.T) {
	account, err := user.Current()
	if err != nil || account.HomeDir == "" {
		t.Skip("account home unavailable")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	env := containerRuntimeEnv("podman")
	values := map[string]string{}
	for _, item := range env {
		parts := strings.SplitN(item, "=", 2)
		values[parts[0]] = parts[1]
	}
	if values["HOME"] != account.HomeDir {
		t.Fatalf("HOME=%q want=%q", values["HOME"], account.HomeDir)
	}
	if _, ok := values["XDG_CONFIG_HOME"]; ok {
		t.Fatal("isolated XDG_CONFIG_HOME leaked to Podman")
	}
	if _, ok := values["XDG_DATA_HOME"]; ok {
		t.Fatal("isolated XDG_DATA_HOME leaked to Podman")
	}
}

func TestConfigureLocalSearXNGPreservesExistingEndpointSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Default()
	pc := cfg.Providers["searxng"]
	pc.Instances = []config.SearXNGInstanceConfig{{Name: "custom-local", URL: "http://127.0.0.1:8080/", TokenEnv: "CUSTOM_TOKEN", RequestsPerSecond: 4, Burst: 7}}
	cfg.Providers["searxng"] = pc
	b, _ := yaml.Marshal(cfg)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := configureLocalSearXNG(path); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	instance := got.Providers["searxng"].Instances[0]
	if instance.Name != "custom-local" || instance.TokenEnv != "CUSTOM_TOKEN" || instance.RequestsPerSecond != 4 || instance.Burst != 7 {
		t.Fatalf("existing endpoint settings changed: %+v", instance)
	}
}
