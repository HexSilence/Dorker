package export

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HexSilence/dorker/internal/store"
)

func TestDiscoveryDirectoryDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	one, err := DiscoveryDirectory(root, "site:example.com", "job-one")
	if err != nil {
		t.Fatal(err)
	}
	two, err := DiscoveryDirectory(root, "site:example.com", "job-two")
	if err != nil {
		t.Fatal(err)
	}
	if two != one+"-2" {
		t.Fatalf("one=%s two=%s", one, two)
	}
}

func TestDiscoveryDirectoryContinuityAndIsolation(t *testing.T) {
	root := t.TempDir()
	one, err := ResolveDiscoveryDirectory(root, "site:.ye", "job-one", "", "")
	if err != nil {
		t.Fatal(err)
	}
	again, err := ResolveDiscoveryDirectory(root, "site:.ye", "job-one", one, "")
	if err != nil || again != one {
		t.Fatalf("first resume=%q err=%v", again, err)
	}
	secondResume, err := ResolveDiscoveryDirectory(root, "site:.ye", "job-one", one, "")
	if err != nil || secondResume != one {
		t.Fatalf("second resume=%q err=%v", secondResume, err)
	}
	two, err := ResolveDiscoveryDirectory(root, "site:.ye", "job-two", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if two == one {
		t.Fatal("independent jobs shared output")
	}
	if _, err = ResolveDiscoveryDirectory(root, "site:.ye", "job-two", one, ""); err == nil {
		t.Fatal("cross-job overwrite accepted")
	}
}

func TestExplicitAndLegacyDiscoveryDirectory(t *testing.T) {
	root := t.TempDir()
	explicit := filepath.Join(root, "chosen")
	got, err := ResolveDiscoveryDirectory(root, "site:.ye", "job", "", explicit)
	if err != nil || got != explicit {
		t.Fatalf("explicit=%q err=%v", got, err)
	}
	legacy := filepath.Join(root, "discover", "site-ye-2")
	if err = os.MkdirAll(legacy, 0755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(DiscoveryReport{JobID: "legacy-job"})
	if err = os.WriteFile(filepath.Join(legacy, "discovery.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	got, err = ResolveDiscoveryDirectory(root, "site:.ye", "legacy-job", "", "")
	if err != nil || got != legacy {
		t.Fatalf("legacy=%q want=%q err=%v", got, legacy, err)
	}
}

func TestWriteDiscoveryRegeneratesOwnedArtifacts(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateJob(ctx, "j", "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertDiscoveryHost(ctx, "j", store.DiscoveryHost{Host: "a.example.com", RegistrableDomain: "example.com", State: "QUEUED"}); err != nil {
		t.Fatal(err)
	}
	dir, err := DiscoveryDirectory(t.TempDir(), "site:example.com", "j")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = WriteDiscovery(ctx, s, "j", dir, DiscoveryReport{JobID: "j"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertDiscoveryHost(ctx, "j", store.DiscoveryHost{Host: "b.example.com", RegistrableDomain: "example.com", State: "QUEUED"}); err != nil {
		t.Fatal(err)
	}
	if _, err = WriteDiscovery(ctx, s, "j", dir, DiscoveryReport{JobID: "j"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "hosts.txt"))
	if err != nil || string(b) != "a.example.com\nb.example.com\n" {
		t.Fatalf("hosts=%q err=%v", b, err)
	}
}

func TestDiscoveryReportExplainsDegradedPartialAndCounting(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateJob(ctx, "j", "{}"); err != nil {
		t.Fatal(err)
	}
	dir, err := DiscoveryDirectory(t.TempDir(), "site:example.com", "j")
	if err != nil {
		t.Fatal(err)
	}
	report := DiscoveryReport{JobID: "j", Status: "PARTIAL", StopReason: "FRONTIER_EXHAUSTED_DEGRADED", CoverageStatus: "DEGRADED", HistoricalDegradation: true, TerminalEvidenceStatus: "INSUFFICIENT", TerminalDetail: "frontier exhausted without trustworthy saturation evidence", HealthCountSemantics: "distinct_query_page_message_observations", ResponsiveSources: []string{"bing"}, ProviderHealth: []ProviderHealthSummary{{Provider: "searxng", Engine: "brave", Classification: "rate_limited", Count: 3, Observations: 3}}}
	if _, err = WriteDiscovery(ctx, s, "j", dir, report); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "discovery.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"coverage_status": "DEGRADED"`, `"historical_degradation": true`, `"terminal_evidence_status": "INSUFFICIENT"`, `"terminal_reason_detail": "frontier exhausted without trustworthy saturation evidence"`, `"provider_health_count_semantics": "distinct_query_page_message_observations"`, `"responsive_sources_observed": [`, `"observations": 3`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s in %s", want, b)
		}
	}
}

func TestDiscoverySelectionsAreSortedAndDeduplicated(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateJob(ctx, "j", "{}"); err != nil {
		t.Fatal(err)
	}
	for _, h := range []store.DiscoveryHost{{Host: "www.example.co.uk", RegistrableDomain: "example.co.uk", State: "QUEUED"}, {Host: "api.example.co.uk", RegistrableDomain: "example.co.uk", State: "QUEUED"}, {Host: "example.com", RegistrableDomain: "example.com", State: "QUEUED"}} {
		if _, err = s.UpsertDiscoveryHost(ctx, "j", h); err != nil {
			t.Fatal(err)
		}
	}
	var out strings.Builder
	if err = WriteDiscoverySelection(ctx, s, "j", "roots", &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "example.co.uk\nexample.com\n" {
		t.Fatalf("%q", got)
	}
}
