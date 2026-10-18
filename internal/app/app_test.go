package app

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HexSilence/dorker/internal/scheduler"
	"github.com/HexSilence/dorker/internal/store"
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

func TestSearchHelpSections(t *testing.T) {
	var out, errout bytes.Buffer
	if code := Run([]string{"search", "--help"}, IO{Out: &out, Err: &errout}); code != 0 {
		t.Fatal(code)
	}
	text := errout.String()
	for _, want := range []string{"INPUT", "PROVIDERS", "LIMITS", "VALIDATION", "OUTPUT", "RECOVERY", "DISPLAY", "--quiet", "--no-banner", "--collapse MODE"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	if strings.Contains(text, "Usage of") {
		t.Fatal(text)
	}
}

func TestCommandHelpReturnsSuccess(t *testing.T) {
	for _, args := range [][]string{{"providers", "--help"}, {"providers", "test", "--help"}, {"query", "--help"}, {"jobs", "--help"}, {"stats", "--help"}, {"export", "--help"}, {"config", "--help"}, {"version", "--help"}} {
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
	t.Setenv("SEARXNG_URL", s.URL)
	var out, errout bytes.Buffer
	if code := Run([]string{"providers", "test", "searxng"}, IO{Out: &out, Err: &errout}); code != 0 {
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
