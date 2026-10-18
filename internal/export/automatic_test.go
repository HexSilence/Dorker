package export

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSlugPortableAndSafe(t *testing.T) {
	tests := map[string]string{
		"Directory Listings":         "directory-listings",
		"PDF Documents":              "pdf-documents",
		"Example.COM":                "example.com",
		"../../NUL\\x\x00":           "nul-x",
		"site:example.com":           "site-example.com",
		"site:.ye":                   "site-ye",
		"ext:pdf":                    "ext-pdf",
		"site:example.com ext:pdf":   "site-example.com-ext-pdf",
		"...site:::...ye---ext::pdf": "site-ye-ext-pdf",
	}
	for in, want := range tests {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q)=%q want %q", in, got, want)
		}
	}
	for _, in := range []string{"../escape", `/absolute`, `..\\escape`, "\x00bad"} {
		got := Slug(in)
		if strings.ContainsAny(got, `/\\`) || got == ".." || strings.ContainsRune(got, 0) {
			t.Fatalf("unsafe slug %q", got)
		}
	}
	if got := Slug(strings.Repeat("long-value-", 20)); len(got) > maxSlugLength || strings.ContainsAny(got[len(got)-1:], ".-") {
		t.Fatalf("unbounded or punctuated slug %q", got)
	}
	_ = runtime.GOOS // portability is enforced independent of host OS.
}

func TestAutomaticPathRepeated(t *testing.T) {
	root := t.TempDir()
	p, err := AutomaticPath(root, []string{"Example.COM", "Directory Listings"}, "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "example.com", "directory-listings", "results.jsonl")
	if p != want {
		t.Fatalf("%q != %q", p, want)
	}
	if err := os.WriteFile(p, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err = AutomaticPath(root, []string{"example.com", "directory-listings"}, "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p, "results-2.jsonl") {
		t.Fatal(p)
	}
}
