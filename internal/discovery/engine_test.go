package discovery

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/HexSilence/dorker/internal/collector"
	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/normalize"
	"github.com/HexSilence/dorker/internal/query"
	"github.com/HexSilence/dorker/internal/store"
)

func newEngineStore(t *testing.T, opts Options) *store.Store {
	return newEngineStoreForSeed(t, opts, "site:example.test")
}

func newEngineStoreForSeed(t *testing.T, opts Options, seed string) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if err = s.CreateJob(ctx, "job", "{}"); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateDiscoveryJob(ctx, store.DiscoveryJob{JobID: "job", SeedQuery: seed, OptionsJSON: EncodeOptions(opts), StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSuffixScopedSeedPreservesValidResultsWhenCoverageDegraded(t *testing.T) {
	o := DefaultOptions()
	o.MaxDepth = 0
	s := newEngineStoreForSeed(t, o, "site:.sa")
	ctx := context.Background()
	scope, err := query.ExtractConstraints("site:.sa")
	if err != nil {
		t.Fatal(err)
	}
	col := &collector.Collector{Store: s, JobID: "job", Normalizer: normalize.New(normalize.Options{}), ValidationMode: query.ValidationConservative, ScopeConstraints: &scope}
	urls := []string{
		"https://example.sa/", "https://www.example.sa/", "https://foo.gov.sa/", "https://docs.example.sa/report.pdf",
		"https://example.sa.attacker.test/", "https://attacker.test/example.sa", "https://attacker.test/?next=example.sa", "://malformed",
	}
	var summary ProcessingSummary
	runner := RunnerFunc(func(ctx context.Context, vs []model.QueryVariant) (RunReport, error) {
		results := make([]model.Result, 0, len(urls))
		for _, raw := range urls {
			results = append(results, model.Result{URL: raw, Provider: "searxng", OriginalQuery: vs[0].Original, QueryVariant: vs[0].Variant, DiscoveredAt: time.Now().UTC()})
		}
		if _, err := col.AddBatch(ctx, results); err != nil {
			return RunReport{}, err
		}
		return RunReport{Degraded: true, Raw: len(urls), Normalized: len(urls) - 1, Accepted: 4, Rejected: 3, Malformed: 1, RejectionReasons: map[string]int{"site_mismatch": 3, "malformed_url": 1}}, nil
	})
	got, err := (&Engine{Store: s, Runner: runner, OnProcessing: func(v ProcessingSummary) { summary = v }}).Run(ctx, "job")
	if err != nil {
		t.Fatal(err)
	}
	if got.Hosts != 4 || got.Stats[0].NewHosts != 4 || got.Stats[0].AcceptedURLs != 4 {
		t.Fatalf("result=%+v stats=%+v", got, got.Stats)
	}
	if got.Status != "PARTIAL" || got.Stats[0].Trustworthy {
		t.Fatalf("degraded coverage was lost: %+v", got)
	}
	if summary.FrontierAdded != 4 || summary.HostsExtracted != 4 || summary.DuplicateHosts != 0 || summary.Rejected != 3 || summary.Malformed != 1 {
		t.Fatalf("summary=%+v", summary)
	}
	hosts, err := s.DiscoveryHosts(ctx, "job")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"docs.example.sa", "example.sa", "foo.gov.sa", "www.example.sa"}
	if len(hosts) != len(want) {
		t.Fatalf("hosts=%+v", hosts)
	}
	for i := range want {
		if hosts[i].Host != want[i] {
			t.Fatalf("hosts=%+v", hosts)
		}
	}
}

func TestRecursiveFrontierAndZeroGainSaturation(t *testing.T) {
	o := DefaultOptions()
	o.MaxDepth = 5
	o.SaturationThreshold = .5
	o.SaturationConsecutiveDepths = 2
	s := newEngineStore(t, o)
	calls := map[string]int{}
	runner := RunnerFunc(func(ctx context.Context, vs []model.QueryVariant) (RunReport, error) {
		for _, v := range vs {
			calls[v.Original]++
			hosts := []string{}
			switch v.Original {
			case "site:example.test":
				hosts = []string{"a.example.test", "b.example.test"}
			case "site:a.example.test":
				hosts = []string{"a.example.test", "c.example.test"}
			case "site:b.example.test":
				hosts = []string{"b.example.test", "c.example.test"}
			case "site:c.example.test":
				hosts = []string{"c.example.test"}
			}
			for i, h := range hosts {
				r := model.Result{URL: "https://" + h + fmt.Sprintf("/%d", i), NormalizedURL: "https://" + h + fmt.Sprintf("/%d", i), Domain: h, Provider: "mock", OriginalQuery: v.Original, QueryVariant: v.Variant, Rank: i, DiscoveredAt: time.Now().UTC()}
				if _, err := s.InsertResult(ctx, "job", r); err != nil {
					return RunReport{}, err
				}
			}
		}
		return RunReport{}, nil
	})
	got, err := (&Engine{Store: s, Runner: runner}).Run(context.Background(), "job")
	if err != nil {
		t.Fatal(err)
	}
	if got.StopReason != StopSaturated || got.Hosts != 3 || got.Depth != 2 {
		t.Fatalf("result=%+v", got)
	}
	for _, q := range []string{"site:a.example.test", "site:b.example.test", "site:c.example.test"} {
		if calls[q] != 1 {
			t.Fatalf("%s calls=%d", q, calls[q])
		}
	}
}

func TestDegradedZeroGainIsPartialNotSaturated(t *testing.T) {
	o := DefaultOptions()
	o.MaxDepth = 2
	s := newEngineStore(t, o)
	got, err := (&Engine{Store: s, Runner: RunnerFunc(func(context.Context, []model.QueryVariant) (RunReport, error) {
		return RunReport{Degraded: true, ProviderFailures: 1}, nil
	})}).Run(context.Background(), "job")
	if err != nil {
		t.Fatal(err)
	}
	if got.StopReason != StopFrontierDegraded || got.Status != "PARTIAL" {
		t.Fatalf("%+v", got)
	}
}

func TestUsefulResultsWithPersistentSourceDegradationStayUntrustworthy(t *testing.T) {
	o := DefaultOptions()
	o.MaxDepth = 4
	s := newEngineStore(t, o)
	calls := 0
	runner := RunnerFunc(func(ctx context.Context, vs []model.QueryVariant) (RunReport, error) {
		calls++
		if calls == 1 {
			v := vs[0]
			r := model.Result{URL: "https://a.example.test/", NormalizedURL: "https://a.example.test/", Domain: "a.example.test", Provider: "searxng", SourceEngines: []string{"bing"}, OriginalQuery: v.Original, QueryVariant: v.Variant, DiscoveredAt: time.Now().UTC()}
			if _, err := s.InsertResult(ctx, "job", r); err != nil {
				return RunReport{}, err
			}
		}
		return RunReport{Degraded: true}, nil
	})
	got, err := (&Engine{Store: s, Runner: runner}).Run(context.Background(), "job")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "PARTIAL" || got.StopReason != StopFrontierDegraded || calls != 2 || len(got.Stats) != 2 {
		t.Fatalf("calls=%d result=%+v", calls, got)
	}
	for _, stat := range got.Stats {
		if stat.Trustworthy {
			t.Fatalf("degraded depth became trustworthy: %+v", stat)
		}
	}
}

func TestProviderFailureCannotSaturate(t *testing.T) {
	o := DefaultOptions()
	s := newEngineStore(t, o)
	got, err := (&Engine{Store: s, Runner: RunnerFunc(func(context.Context, []model.QueryVariant) (RunReport, error) {
		return RunReport{Partial: true, ProviderFailures: 1}, nil
	})}).Run(context.Background(), "job")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "PARTIAL" || got.StopReason != StopFrontierDegraded || got.Stats[0].Trustworthy || got.Stats[0].ProviderFailures != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestHistoricalDegradationRequiresExplicitRecoveryProof(t *testing.T) {
	for _, tc := range []struct {
		name           string
		recoveryProven bool
		wantStatus     string
		wantReason     string
		wantLastTrust  bool
	}{
		{"absence of new diagnostics is not recovery", false, "PARTIAL", StopFrontierDegraded, false},
		{"explicit full recovery permits saturation", true, "SATURATED", StopSaturated, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := DefaultOptions()
			o.MaxDepth = 6
			s := newEngineStore(t, o)
			depth := 0
			runner := RunnerFunc(func(ctx context.Context, vs []model.QueryVariant) (RunReport, error) {
				current := depth
				depth++
				if current < 3 {
					v := vs[0]
					host := fmt.Sprintf("h%d.example.test", current)
					r := model.Result{URL: "https://" + host + "/", NormalizedURL: "https://" + host + "/", Domain: host, Provider: "searxng", SourceEngines: []string{"bing"}, OriginalQuery: v.Original, QueryVariant: v.Variant, DiscoveredAt: time.Now().UTC()}
					if _, err := s.InsertResult(ctx, "job", r); err != nil {
						return RunReport{}, err
					}
					return RunReport{Degraded: true}, nil
				}
				return RunReport{RecoveryProven: tc.recoveryProven}, nil
			})
			got, err := (&Engine{Store: s, Runner: runner}).Run(context.Background(), "job")
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.wantStatus || got.StopReason != tc.wantReason || len(got.Stats) != 4 || got.Stats[3].Trustworthy != tc.wantLastTrust {
				t.Fatalf("result=%+v stats=%+v", got, got.Stats)
			}
			persisted, err := s.DiscoveryJob(context.Background(), "job")
			if err != nil || persisted.StopReason != tc.wantReason {
				t.Fatalf("persisted=%+v err=%v", persisted, err)
			}
		})
	}
}

func TestCrashRecoveryRequeuesExpandingHost(t *testing.T) {
	o := DefaultOptions()
	s := newEngineStore(t, o)
	ctx := context.Background()
	_, err := s.UpsertDiscoveryHost(ctx, "job", store.DiscoveryHost{Host: "a.example.test", Depth: 0, State: "EXPANDING"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverDiscoveryFrontier(ctx, "job"); err != nil {
		t.Fatal(err)
	}
	hs, err := s.DiscoveryFrontier(ctx, "job", 0, 0)
	if err != nil || len(hs) != 1 || hs[0].State != "QUEUED" {
		t.Fatalf("hosts=%+v err=%v", hs, err)
	}
}

func TestFrontierRetriesOnlyEligibleStates(t *testing.T) {
	o := DefaultOptions()
	s := newEngineStore(t, o)
	ctx := context.Background()
	for _, h := range []store.DiscoveryHost{{Host: "retry.example.test", Depth: 0, State: "FAILED_RETRYABLE"}, {Host: "final.example.test", Depth: 0, State: "FAILED_FINAL"}, {Host: "done.example.test", Depth: 0, State: "EXPANDED"}} {
		if _, err := s.UpsertDiscoveryHost(ctx, "job", h); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.DiscoveryFrontier(ctx, "job", 0, 0)
	if err != nil || len(got) != 1 || got[0].Host != "retry.example.test" {
		t.Fatalf("frontier=%+v err=%v", got, err)
	}
}

func TestLimitsValidation(t *testing.T) {
	o := DefaultOptions()
	o.SaturationThreshold = 1.1
	if o.Validate() == nil {
		t.Fatal("expected invalid threshold")
	}
	o = DefaultOptions()
	o.MaxDuration = 0
	if o.Validate() == nil {
		t.Fatal("expected invalid duration")
	}
}

func TestHardStopReasons(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Options)
		want   string
	}{{"depth", func(o *Options) { o.MaxDepth = 0 }, StopMaxDepth}, {"hosts", func(o *Options) { o.MaxHosts = 1 }, StopMaxHosts}, {"queries", func(o *Options) { o.MaxQueries = 1 }, StopMaxQueries}} {
		t.Run(tc.name, func(t *testing.T) {
			o := DefaultOptions()
			tc.mutate(&o)
			s := newEngineStore(t, o)
			runner := RunnerFunc(func(ctx context.Context, vs []model.QueryVariant) (RunReport, error) {
				v := vs[0]
				r := model.Result{URL: "https://a.example.test/", NormalizedURL: "https://a.example.test/", Domain: "a.example.test", Provider: "mock", OriginalQuery: v.Original, QueryVariant: v.Variant, DiscoveredAt: time.Now().UTC()}
				_, err := s.InsertResult(ctx, "job", r)
				return RunReport{}, err
			})
			got, err := (&Engine{Store: s, Runner: runner}).Run(context.Background(), "job")
			if err != nil {
				t.Fatal(err)
			}
			if got.StopReason != tc.want {
				t.Fatalf("got %s want %s", got.StopReason, tc.want)
			}
		})
	}
}

func TestDurationStopsWithoutProviderCall(t *testing.T) {
	o := DefaultOptions()
	o.MaxDuration = time.Second
	s := newEngineStore(t, o)
	called := false
	future := time.Now().Add(time.Hour)
	got, err := (&Engine{Store: s, Now: func() time.Time { return future }, Runner: RunnerFunc(func(context.Context, []model.QueryVariant) (RunReport, error) { called = true; return RunReport{}, nil })}).Run(context.Background(), "job")
	if err != nil {
		t.Fatal(err)
	}
	if called || got.StopReason != StopMaxDuration {
		t.Fatalf("called=%v result=%+v", called, got)
	}
}

func TestInterruptedSeedResumesWithoutDuplicateResults(t *testing.T) {
	o := DefaultOptions()
	o.MaxDepth = 0
	s := newEngineStore(t, o)
	first := &Engine{Store: s, Runner: RunnerFunc(func(context.Context, []model.QueryVariant) (RunReport, error) { return RunReport{}, context.Canceled })}
	if _, err := first.Run(context.Background(), "job"); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	calls := 0
	second := &Engine{Store: s, Runner: RunnerFunc(func(ctx context.Context, vs []model.QueryVariant) (RunReport, error) {
		calls++
		v := vs[0]
		r := model.Result{URL: "https://a.example.test/", NormalizedURL: "https://a.example.test/", Domain: "a.example.test", Provider: "mock", OriginalQuery: v.Original, QueryVariant: v.Variant, DiscoveredAt: time.Now().UTC()}
		_, err := s.InsertResult(ctx, "job", r)
		return RunReport{}, err
	})}
	got, err := second.Run(context.Background(), "job")
	if err != nil || calls != 1 || got.Hosts != 1 {
		t.Fatalf("calls=%d result=%+v err=%v", calls, got, err)
	}
}

func TestMultipleProvenanceEdgesArePreserved(t *testing.T) {
	o := DefaultOptions()
	o.MaxDepth = 0
	s := newEngineStore(t, o)
	runner := RunnerFunc(func(ctx context.Context, vs []model.QueryVariant) (RunReport, error) {
		v := vs[0]
		for _, p := range []string{"one", "two"} {
			r := model.Result{URL: "https://a.example.test/", NormalizedURL: "https://a.example.test/", Domain: "a.example.test", Provider: p, OriginalQuery: v.Original, QueryVariant: v.Variant, DiscoveredAt: time.Now().UTC()}
			if _, err := s.InsertResult(ctx, "job", r); err != nil {
				return RunReport{}, err
			}
		}
		return RunReport{}, nil
	})
	if _, err := (&Engine{Store: s, Runner: runner}).Run(context.Background(), "job"); err != nil {
		t.Fatal(err)
	}
	edges, err := s.DiscoveryEdges(context.Background(), "job")
	if err != nil || len(edges) != 2 {
		t.Fatalf("edges=%+v err=%v", edges, err)
	}
}
