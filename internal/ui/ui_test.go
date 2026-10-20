package ui

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/HexSilence/dorker/internal/model"
)

func TestBannerEnabledDisabled(t *testing.T) {
	var b bytes.Buffer
	r := New(&b, Options{Banner: true, Interactive: true, Color: ColorNever})
	r.Banner()
	if !strings.Contains(b.String(), "D O R K E R") || !strings.Contains(b.String(), "Silence") {
		t.Fatal(b.String())
	}
	b.Reset()
	New(&b, Options{Banner: true, Interactive: false, Color: ColorNever}).Banner()
	if b.Len() != 0 {
		t.Fatal("non-TTY banner")
	}
	New(&b, Options{Banner: true, Interactive: true, NoBanner: true}).Banner()
	if b.Len() != 0 {
		t.Fatal("disabled banner")
	}
}
func TestColorPolicy(t *testing.T) {
	var b bytes.Buffer
	t.Setenv("NO_COLOR", "temporary")
	os.Unsetenv("NO_COLOR")
	defer os.Unsetenv("NO_COLOR")
	New(&b, Options{Banner: true, Interactive: true, Color: ColorAlways}).Banner()
	if !strings.Contains(b.String(), "\x1b[") {
		t.Fatal("expected color")
	}
	b.Reset()
	os.Setenv("NO_COLOR", "")
	New(&b, Options{Banner: true, Interactive: true, Color: ColorAlways}).Banner()
	if strings.Contains(b.String(), "\x1b[") {
		t.Fatal("NO_COLOR ignored")
	}
	_ = os.Getenv("NO_COLOR")
}
func TestUnicodeBannerAlignment(t *testing.T) {
	var b bytes.Buffer
	New(&b, Options{Banner: true, Interactive: true, Color: ColorNever}).Banner()
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if len([]rune(line)) != 48 {
			t.Fatalf("width=%d %q", len([]rune(line)), line)
		}
	}
}

func TestBannerTextAndSpacing(t *testing.T) {
	var b bytes.Buffer
	New(&b, Options{Banner: true, Interactive: true, Color: ColorNever}).Banner()
	want := "" +
		"┌──────────────────────────────────────────────┐\n" +
		"│                                              │\n" +
		"│                 D O R K E R                  │\n" +
		"│         Advanced Search Intelligence         │\n" +
		"│           & Reconnaissance Engine            │\n" +
		"│                                              │\n" +
		"│          Developed & Engineered by           │\n" +
		"│                   Silence                    │\n" +
		"│                                              │\n" +
		"│   Built for Precision • Driven by Research   │\n" +
		"│              © 2026 HexSilence               │\n" +
		"│              Apache License 2.0              │\n" +
		"└──────────────────────────────────────────────┘\n\n"
	if b.String() != want {
		t.Fatalf("banner mismatch:\n%s", b.String())
	}
}

func TestSearchStart(t *testing.T) {
	var b bytes.Buffer
	r := New(&b, Options{Interactive: true, Color: ColorNever})
	r.SearchStart(SearchStart{Query: "site:.ye", Provider: "searxng", Validation: "conservative", Limit: 100})
	want := "DORKER / SEARCH\n\n  Query       site:.ye\n  Provider    searxng\n  Validation  conservative\n  Limit       100\n\n"
	if b.String() != want {
		t.Fatalf("search start mismatch:\n%s", b.String())
	}
}

func TestSearchSummaries(t *testing.T) {
	tests := []struct {
		status string
		want   []string
	}{
		{"COMPLETE", []string{"Search complete", "Unique        24", "Duplicates   3"}},
		{"COMPLETE_DEGRADED", []string{"Search complete · degraded"}},
		{"FAILED", []string{"Search failed"}},
	}
	for _, tc := range tests {
		t.Run(tc.status, func(t *testing.T) {
			var b bytes.Buffer
			New(&b, Options{Interactive: true, Color: ColorNever}).SummaryDetails(Summary{Status: tc.status, Output: "results.txt", Stats: model.Stats{Unique: 24, Raw: 30, Duplicates: 3}})
			for _, want := range tc.want {
				if !strings.Contains(b.String(), want) {
					t.Fatalf("missing %q in:\n%s", want, b.String())
				}
			}
			if strings.Contains(b.String(), "Errors") || strings.Contains(b.String(), "Malformed") {
				t.Fatalf("zero values should be hidden:\n%s", b.String())
			}
		})
	}
}

func TestSemanticColorSequences(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	os.Unsetenv("NO_COLOR")
	defer os.Unsetenv("NO_COLOR")
	var b bytes.Buffer
	r := New(&b, Options{Banner: true, Interactive: true, Color: ColorAlways})
	r.Banner()
	r.SearchStart(SearchStart{Query: "x", Provider: "mock", Validation: "off", Limit: 1})
	r.ProviderTest(ProviderTest{Name: "mock", State: "READY"})
	text := b.String()
	for _, seq := range []string{"\x1b[1;96m", "D O R K E R", "\x1b[1;96mDORKER / SEARCH", "\x1b[1;92mReady"} {
		if !strings.Contains(text, seq) {
			t.Errorf("missing %q", seq)
		}
	}
	b.Reset()
	PrintError(&b, "boom")
	// bytes.Buffer is not a TTY; direct primitives prove the semantic prefix roles.
	if !strings.Contains(r.Error("error:"), "\x1b[1;91m") || !strings.Contains(r.Warning("warning:"), "\x1b[1;93m") {
		t.Fatal("semantic prefix colors missing")
	}
}

func TestQuietAndNonInteractiveSearchUI(t *testing.T) {
	for _, opts := range []Options{{Interactive: true, Quiet: true}, {Interactive: false}} {
		var b bytes.Buffer
		r := New(&b, opts)
		r.SearchStart(SearchStart{Query: "test"})
		r.Summary("COMPLETE", "", model.Stats{})
		if b.Len() != 0 {
			t.Fatalf("unexpected UI: %q", b.String())
		}
	}
}

func TestStatusColorRolesAndNever(t *testing.T) {
	old, hadNoColor := os.LookupEnv("NO_COLOR")
	os.Unsetenv("NO_COLOR")
	t.Cleanup(func() {
		if hadNoColor {
			os.Setenv("NO_COLOR", old)
		} else {
			os.Unsetenv("NO_COLOR")
		}
	})
	var b bytes.Buffer
	r := New(&b, Options{Interactive: true, Color: ColorAlways})
	for _, status := range []string{"READY", "COMPLETE", "COOLDOWN", "RATE_LIMITED", "FAILED", "UNAVAILABLE"} {
		b.Reset()
		r.ProviderTest(ProviderTest{Name: "test", State: status})
		if !strings.Contains(b.String(), "\x1b[") {
			t.Fatalf("missing color for %s", status)
		}
	}
	b.Reset()
	New(&b, Options{Interactive: true, Color: ColorNever}).ProviderTest(ProviderTest{Name: "test", State: "READY"})
	if strings.Contains(b.String(), "\x1b[") {
		t.Fatal("color=never emitted ANSI")
	}
}

func TestGHDBReusesCurrentColorRoles(t *testing.T) {
	old, had := os.LookupEnv("NO_COLOR")
	os.Unsetenv("NO_COLOR")
	defer func() {
		if had {
			os.Setenv("NO_COLOR", old)
		} else {
			os.Unsetenv("NO_COLOR")
		}
	}()
	var b bytes.Buffer
	New(&b, Options{Interactive: true, Color: ColorAlways}).GHDBList([]GHDBEntry{{ID: 1, Title: "PDF documents", Description: "Find indexed PDF documents."}}, true)
	for _, want := range []string{"\x1b[1;96mGHDB", "\x1b[1;36m1.", "\x1b[1;97mPDF documents", "\x1b[2mFind indexed"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("missing %q in %q", want, b.String())
		}
	}
	b.Reset()
	New(&b, Options{Interactive: true, Color: ColorNever}).GHDBList([]GHDBEntry{{ID: 1, Title: "x", Description: "y"}}, true)
	if strings.Contains(b.String(), "\x1b[") {
		t.Fatal("color=never emitted ANSI")
	}
	b.Reset()
	os.Setenv("NO_COLOR", "1")
	New(&b, Options{Interactive: true, Color: ColorAlways}).GHDBList([]GHDBEntry{{ID: 1, Title: "x", Description: "y"}}, true)
	if strings.Contains(b.String(), "\x1b[") {
		t.Fatal("NO_COLOR ignored")
	}
}
