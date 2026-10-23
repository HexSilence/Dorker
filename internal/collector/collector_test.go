package collector

import (
	"context"
	"fmt"
	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/normalize"
	"github.com/HexSilence/dorker/internal/query"
	"github.com/HexSilence/dorker/internal/store"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilter(t *testing.T) {
	f, err := CompileFilter([]string{"example.com"}, []string{"private.example.com"}, `/docs`, ``)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Accept("https://www.example.com/docs/a", "www.example.com") {
		t.Fatal("expected accepted")
	}
	if f.Accept("https://private.example.com/docs/a", "private.example.com") {
		t.Fatal("expected excluded")
	}
	if f.Accept("https://notexample.com/docs/a", "notexample.com") {
		t.Fatal("domain boundary failure")
	}
}

func TestDorkValidationModes(t *testing.T) {
	urls := []string{"https://mit.edu/report.pdf", "https://mit.edu/page.html", "https://example.com/report.pdf", "https://berkeley.edu/download?id=123"}
	for _, tc := range []struct {
		mode                        query.ValidationMode
		accepted, rejected, unknown int64
	}{{query.ValidationOff, 3, 1, 0}, {query.ValidationConservative, 2, 2, 1}, {query.ValidationStrict, 1, 3, 0}} {
		t.Run(string(tc.mode), func(t *testing.T) {
			s, err := store.Open(filepath.Join(t.TempDir(), "x.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ctx := context.Background()
			if err = s.CreateJob(ctx, "j", "{}"); err != nil {
				t.Fatal(err)
			}
			c := &Collector{Store: s, JobID: "j", Normalizer: normalize.New(normalize.Options{}), ValidationMode: tc.mode}
			var rs []model.Result
			for _, u := range urls {
				rs = append(rs, model.Result{URL: u, Provider: "mock", OriginalQuery: "site:.edu ext:pdf", QueryVariant: "site:.edu ext:pdf", DiscoveredAt: time.Now()})
			}
			if _, err = c.AddBatch(ctx, rs); err != nil {
				t.Fatal(err)
			}
			if c.Accepted.Load() != tc.accepted || c.Rejected.Load() != tc.rejected || c.UnknownKept.Load() != tc.unknown {
				t.Fatalf("accepted=%d rejected=%d unknown=%d", c.Accepted.Load(), c.Rejected.Load(), c.UnknownKept.Load())
			}
		})
	}
}

func TestSiteScopeCannotLeakIntoAuthoritativeResults(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "scope.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateJob(ctx, "j", "{}"); err != nil {
		t.Fatal(err)
	}
	c := &Collector{Store: s, JobID: "j", Normalizer: normalize.New(normalize.Options{}), ValidationMode: query.ValidationOff}
	urls := []string{
		"https://example.ye/", "https://API.EXAMPLE.YE.:443/v1", "https://a.example.edu.ye/report.pdf",
		"https://example.com/", "https://example.yemen/", "https://example.ye.attacker.test/",
		"https://attacker.test/?next=https://example.ye", "https://attacker.test/path/example.ye", "://broken",
	}
	var rs []model.Result
	for _, u := range urls {
		rs = append(rs, model.Result{URL: u, Provider: "mock", OriginalQuery: "site:.ye", QueryVariant: "site:.ye", DiscoveredAt: time.Now()})
	}
	if _, err = c.AddBatch(ctx, rs); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Results(ctx, "j")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var r model.Result
		var seen string
		if err = rows.Scan(&r.NormalizedURL, &r.Domain, &r.Title, &r.Snippet, &r.Provider, &r.OriginalQuery, &r.QueryVariant, &r.Rank, &r.Page, &seen, &r.URL); err != nil {
			t.Fatal(err)
		}
		if !(r.Domain == "ye" || strings.HasSuffix(r.Domain, ".ye")) {
			t.Fatalf("out-of-scope result persisted: %s", r.NormalizedURL)
		}
	}
	if c.Unique.Load() != 3 || c.Rejected.Load() != 5 || c.Malformed.Load() != 1 {
		t.Fatalf("unique=%d rejected=%d malformed=%d", c.Unique.Load(), c.Rejected.Load(), c.Malformed.Load())
	}
}

func TestDiscoveryExpansionRetainsSeedScope(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "scope.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateJob(ctx, "j", "{}"); err != nil {
		t.Fatal(err)
	}
	scope, _ := query.ExtractConstraints("site:example.com -site:dev.example.com")
	c := &Collector{Store: s, JobID: "j", Normalizer: normalize.New(normalize.Options{}), ValidationMode: query.ValidationConservative, ScopeConstraints: &scope}
	rs := []model.Result{{URL: "https://api.example.com/a", Provider: "mock", OriginalQuery: "site:example.com", QueryVariant: "site:example.com", DiscoveredAt: time.Now()}, {URL: "https://dev.example.com/a", Provider: "mock", OriginalQuery: "site:example.com", QueryVariant: "site:example.com", DiscoveredAt: time.Now()}}
	if _, err = c.AddBatch(ctx, rs); err != nil {
		t.Fatal(err)
	}
	if c.Unique.Load() != 1 || c.Rejected.Load() != 1 {
		t.Fatalf("unique=%d rejected=%d", c.Unique.Load(), c.Rejected.Load())
	}
}

func TestUniqueLimitScansPastRejectedMalformedAndDuplicateCandidates(t *testing.T) {
	for _, tc := range []struct {
		name                                                     string
		page                                                     []string
		wantRejected, wantMalformed, wantDuplicates, wantSkipped int64
	}{
		{
			name: "rejected and duplicates before unique",
			page: []string{
				"https://attacker.test/one", "https://attacker.test/two",
				"https://example.sa/old-a", "https://example.sa/old-b",
				"https://example.sa/new-a", "https://example.sa/new-b", "https://example.sa/new-c",
			},
			wantRejected: 2, wantDuplicates: 2,
		},
		{
			name: "many duplicates before unique",
			page: []string{
				"https://example.sa/old-a", "https://example.sa/old-b", "https://example.sa/old-a", "https://example.sa/old-b",
				"https://example.sa/old-a", "https://example.sa/old-b", "https://example.sa/old-a", "https://example.sa/old-b",
				"https://example.sa/old-a", "https://example.sa/old-b",
				"https://example.sa/new-a", "https://example.sa/new-b", "https://example.sa/new-c",
			},
			wantDuplicates: 10,
		},
		{
			name: "cap exactly enforced after all candidate classes",
			page: []string{
				"://bad", "not a URL", "https://attacker.test/one", "https://attacker.test/two",
				"https://example.sa/old-a", "https://example.sa/old-b",
				"https://example.sa/new-a", "https://example.sa/new-b", "https://example.sa/new-c", "https://example.sa/new-d", "https://example.sa/new-e",
			},
			wantMalformed: 2, wantRejected: 2, wantDuplicates: 2, wantSkipped: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s, err := store.Open(filepath.Join(t.TempDir(), "limits.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.CreateJob(ctx, "j", "{}"); err != nil {
				t.Fatal(err)
			}
			seed := &Collector{Store: s, JobID: "j", Normalizer: normalize.New(normalize.Options{}), ValidationMode: query.ValidationConservative}
			base := []model.Result{
				{URL: "https://example.sa/old-a", Provider: "seed", OriginalQuery: "site:.sa", QueryVariant: "site:.sa", DiscoveredAt: time.Now()},
				{URL: "https://example.sa/old-b", Provider: "seed", OriginalQuery: "site:.sa", QueryVariant: "site:.sa", DiscoveredAt: time.Now()},
			}
			if _, err = seed.AddBatch(ctx, base); err != nil {
				t.Fatal(err)
			}
			c := &Collector{Store: s, JobID: "j", Normalizer: normalize.New(normalize.Options{}), ValidationMode: query.ValidationConservative}
			results := make([]model.Result, 0, len(tc.page))
			for _, u := range tc.page {
				results = append(results, model.Result{URL: u, Provider: "test", OriginalQuery: "site:.sa", QueryVariant: "site:.sa", DiscoveredAt: time.Now()})
			}
			fresh, skipped, err := c.AddBatchLimited(ctx, results, 3)
			if err != nil {
				t.Fatal(err)
			}
			if fresh != 3 || skipped != int(tc.wantSkipped) || c.Unique.Load() != 3 || c.Rejected.Load() != tc.wantRejected || c.Malformed.Load() != tc.wantMalformed || c.Duplicates.Load() != tc.wantDuplicates || c.LimitSkipped.Load() != tc.wantSkipped {
				t.Fatalf("fresh=%d skipped=%d unique=%d rejected=%d malformed=%d duplicates=%d limitSkipped=%d", fresh, skipped, c.Unique.Load(), c.Rejected.Load(), c.Malformed.Load(), c.Duplicates.Load(), c.LimitSkipped.Load())
			}
			stats, err := s.Stats(ctx, "j")
			if err != nil || stats.Unique != 5 {
				t.Fatalf("stats=%+v err=%v", stats, err)
			}
		})
	}
}

func TestZeroUniqueLimitMeansUnlimited(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "unlimited.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CreateJob(ctx, "j", "{}"); err != nil {
		t.Fatal(err)
	}
	c := &Collector{Store: s, JobID: "j", Normalizer: normalize.New(normalize.Options{})}
	var results []model.Result
	for i := 0; i < 150; i++ {
		results = append(results, model.Result{URL: fmt.Sprintf("https://example.test/%03d", i), Provider: "test", OriginalQuery: "q", QueryVariant: "q", DiscoveredAt: time.Now()})
	}
	fresh, skipped, err := c.AddBatchLimited(ctx, results, -1)
	if err != nil || fresh != 150 || skipped != 0 {
		t.Fatalf("fresh=%d skipped=%d err=%v", fresh, skipped, err)
	}
}
