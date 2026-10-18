package collector

import (
	"context"
	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/normalize"
	"github.com/HexSilence/dorker/internal/query"
	"github.com/HexSilence/dorker/internal/store"
	"path/filepath"
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
	}{{query.ValidationOff, 4, 0, 0}, {query.ValidationConservative, 2, 2, 1}, {query.ValidationStrict, 1, 3, 0}} {
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
