package scheduler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HexSilence/dorker/internal/collector"
	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/normalize"
	"github.com/HexSilence/dorker/internal/provider"
	"github.com/HexSilence/dorker/internal/query"
	rl "github.com/HexSilence/dorker/internal/ratelimit"
	"github.com/HexSilence/dorker/internal/store"
)

type sequenceProvider struct {
	mu        sync.Mutex
	name      string
	responses []provider.SearchResponse
	errors    []error
	calls     int
}

func (p *sequenceProvider) Name() string { return p.name }
func (p *sequenceProvider) Health() provider.HealthStatus {
	return provider.HealthStatus{State: provider.Ready}
}
func (p *sequenceProvider) Pagination(r provider.SearchRequest) provider.PaginationInfo {
	return provider.PaginationInfo{UpstreamPage: r.Page + 1}
}
func (p *sequenceProvider) Search(context.Context, provider.SearchRequest) (provider.SearchResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := p.calls
	p.calls++
	if i < len(p.errors) && p.errors[i] != nil {
		return provider.SearchResponse{}, p.errors[i]
	}
	if i < len(p.responses) {
		return p.responses[i], nil
	}
	return provider.SearchResponse{Done: true}, nil
}

func TestPageOutcomeClassification(t *testing.T) {
	degraded := provider.SearchResponse{Diagnostics: []provider.UpstreamDiagnostic{{Engine: "brave", Classification: provider.UpstreamRateLimited}}}
	tests := []struct {
		name             string
		response         provider.SearchResponse
		unique, accepted int
		err              error
		want             PageOutcome
	}{
		{"useful", provider.SearchResponse{Results: []model.Result{{URL: "https://example.test/a"}}}, 1, 1, nil, UsefulPage},
		{"clean empty", provider.SearchResponse{}, 0, 0, nil, CleanEmpty},
		{"degraded empty", degraded, 0, 0, nil, DegradedEmpty},
		{"duplicate only", provider.SearchResponse{Results: []model.Result{{URL: "https://example.test/a"}}}, 0, 1, nil, DuplicateOnly},
		{"rejected only", provider.SearchResponse{Results: []model.Result{{URL: "https://attacker.test/a"}}}, 0, 0, nil, RejectedOnly},
		{"error", provider.SearchResponse{}, 0, 0, context.DeadlineExceeded, ErrorPage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyPage(tc.response, tc.unique, tc.accepted, tc.err); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestRejectedOnlyPageDoesNotPreventLaterInScopeResults(t *testing.T) {
	st, col, vs := adaptiveFixture(t)
	scope, err := query.ExtractConstraints("site:.sa")
	if err != nil {
		t.Fatal(err)
	}
	col.ScopeConstraints = &scope
	bad := model.Result{URL: "https://attacker.test/example.sa", Provider: "p", DiscoveredAt: time.Now()}
	good := model.Result{URL: "https://example.sa/", Provider: "p", DiscoveredAt: time.Now()}
	p := &sequenceProvider{name: "p", responses: []provider.SearchResponse{{Results: []model.Result{bad}}, {Results: []model.Result{bad}}, {Results: []model.Result{good}, Done: true}}}
	s := New(st, col, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Workers: 1, MaxPages: 10, PageSize: 10, SaturationEnabled: true, ConsecutiveZeroYieldPages: 2, Retry: rl.RetryPolicy{MaxAttempts: 1}})
	if err = s.Run(context.Background(), "job", vs, []Runtime{NewRuntime(p, 1000, 1)}, nil); err != nil {
		t.Fatal(err)
	}
	if p.calls != 3 || col.Unique.Load() != 1 || s.Report().Saturated || s.Report().RejectedOnlyPages != 2 {
		t.Fatalf("calls=%d unique=%d report=%+v", p.calls, col.Unique.Load(), s.Report())
	}
}

func adaptiveFixture(t *testing.T) (*store.Store, *collector.Collector, []model.QueryVariant) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err = st.CreateJob(context.Background(), "job", "{}"); err != nil {
		t.Fatal(err)
	}
	vs := []model.QueryVariant{{Original: "q", Variant: "q"}}
	if err = st.AddVariants(context.Background(), "job", vs); err != nil {
		t.Fatal(err)
	}
	col := &collector.Collector{Store: st, JobID: "job", Normalizer: normalize.New(normalize.Options{})}
	return st, col, vs
}

func TestDuplicateOnlySaturatesButDegradedEmptyDoesNot(t *testing.T) {
	t.Run("duplicates", func(t *testing.T) {
		st, col, vs := adaptiveFixture(t)
		r := model.Result{URL: "https://example.test/a", Provider: "p", OriginalQuery: "q", QueryVariant: "q", DiscoveredAt: time.Now()}
		p := &sequenceProvider{name: "p", responses: []provider.SearchResponse{{Results: []model.Result{r}}, {Results: []model.Result{r}}, {Results: []model.Result{r}}, {Results: []model.Result{r}}}}
		s := New(st, col, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Workers: 1, MaxPages: 10, PageSize: 10, SaturationEnabled: true, ConsecutiveZeroYieldPages: 2, Retry: rl.RetryPolicy{MaxAttempts: 1}})
		if err := s.Run(context.Background(), "job", vs, []Runtime{NewRuntime(p, 1000, 1)}, nil); err != nil {
			t.Fatal(err)
		}
		if !s.Report().Saturated || p.calls != 3 {
			t.Fatalf("report=%+v calls=%d", s.Report(), p.calls)
		}
	})
	t.Run("degraded", func(t *testing.T) {
		st, col, vs := adaptiveFixture(t)
		p := &sequenceProvider{name: "p", responses: []provider.SearchResponse{{Diagnostics: []provider.UpstreamDiagnostic{{Provider: "p", Engine: "brave", Classification: provider.UpstreamRateLimited}}}}}
		s := New(st, col, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Workers: 1, MaxPages: 10, PageSize: 10, SaturationEnabled: true, ConsecutiveZeroYieldPages: 1, Retry: rl.RetryPolicy{MaxAttempts: 1}})
		if err := s.Run(context.Background(), "job", vs, []Runtime{NewRuntime(p, 1000, 1)}, nil); err != nil {
			t.Fatal(err)
		}
		report := s.Report()
		if report.Saturated || report.DegradedEmptyPages != 1 || !report.Partial {
			t.Fatalf("%+v", report)
		}
		progress, _ := st.LoadProgress(context.Background(), "job")
		if progress[0].Page != 0 || progress[0].State != "DEGRADED" {
			t.Fatalf("%+v", progress[0])
		}
	})
}

func TestDegradedDuplicateStreakUsesLowerConfidenceStop(t *testing.T) {
	st, col, vs := adaptiveFixture(t)
	r := model.Result{URL: "https://example.test/a", Provider: "searxng", DiscoveredAt: time.Now()}
	degraded := []provider.UpstreamDiagnostic{{Provider: "searxng", Engine: "brave", Classification: provider.UpstreamRateLimited}}
	p := &sequenceProvider{name: "searxng", responses: []provider.SearchResponse{
		{Results: []model.Result{r}, Diagnostics: degraded},
		{Results: []model.Result{r}, Diagnostics: degraded},
		{Results: []model.Result{r}, Diagnostics: degraded},
		{Results: []model.Result{{URL: "https://example.test/later", Provider: "searxng", DiscoveredAt: time.Now()}}, Diagnostics: degraded},
		{Results: []model.Result{r}, Diagnostics: degraded},
		{Results: []model.Result{r}, Diagnostics: degraded},
		{Results: []model.Result{r}, Diagnostics: degraded},
		{Results: []model.Result{r}, Diagnostics: degraded},
	}}
	s := New(st, col, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Workers: 1, MaxPages: 20, PageSize: 100, SaturationEnabled: true, ConsecutiveZeroYieldPages: 2, Retry: rl.RetryPolicy{MaxAttempts: 1}})
	if err := s.Run(context.Background(), "job", vs, []Runtime{NewRuntime(p, 1000, 1)}, nil); err != nil {
		t.Fatal(err)
	}
	report := s.Report()
	if p.calls != 8 || col.Unique.Load() != 2 || report.Saturated || !report.DegradedEarlyStop || !report.Partial {
		t.Fatalf("calls=%d unique=%d report=%+v", p.calls, col.Unique.Load(), report)
	}
	progress, err := st.LoadProgress(context.Background(), "job")
	if err != nil || len(progress) != 1 || progress[0].State != "DEGRADED_COMPLETE" {
		t.Fatalf("progress=%+v err=%v", progress, err)
	}
	resumed := New(st, col, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Workers: 1, MaxPages: 20, PageSize: 100, SaturationEnabled: true, ConsecutiveZeroYieldPages: 2, Retry: rl.RetryPolicy{MaxAttempts: 1}})
	if err = resumed.Run(context.Background(), "job", vs, []Runtime{NewRuntime(p, 1000, 1)}, progress); err != nil {
		t.Fatal(err)
	}
	if p.calls != 8 {
		t.Fatalf("completed degraded task reran on resume: calls=%d", p.calls)
	}
}

func TestHealthyDuplicateThresholdRemainsAdaptive(t *testing.T) {
	st, col, vs := adaptiveFixture(t)
	r := model.Result{URL: "https://example.test/a", Provider: "p", DiscoveredAt: time.Now()}
	p := &sequenceProvider{name: "p", responses: []provider.SearchResponse{
		{Results: []model.Result{r}},
		{Results: []model.Result{r}},
		{Results: []model.Result{r}},
		{Results: []model.Result{{URL: "https://example.test/later", Provider: "p", DiscoveredAt: time.Now()}}, Done: true},
	}}
	s := New(st, col, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Workers: 1, MaxPages: 10, PageSize: 10, SaturationEnabled: true, ConsecutiveZeroYieldPages: 2, Retry: rl.RetryPolicy{MaxAttempts: 1}})
	if err := s.Run(context.Background(), "job", vs, []Runtime{NewRuntime(p, 1000, 1)}, nil); err != nil {
		t.Fatal(err)
	}
	if p.calls != 3 || !s.Report().Saturated || col.Unique.Load() != 1 {
		t.Fatalf("calls=%d unique=%d report=%+v", p.calls, col.Unique.Load(), s.Report())
	}
}

func TestMaxPagesIsHardCeiling(t *testing.T) {
	st, col, vs := adaptiveFixture(t)
	p := &sequenceProvider{name: "p", responses: []provider.SearchResponse{
		{Results: []model.Result{{URL: "https://example.test/a", Provider: "p", DiscoveredAt: time.Now()}}},
		{Results: []model.Result{{URL: "https://example.test/b", Provider: "p", DiscoveredAt: time.Now()}}},
		{Results: []model.Result{{URL: "https://example.test/c", Provider: "p", DiscoveredAt: time.Now()}}},
	}}
	s := New(st, col, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Workers: 1, MaxPages: 2, PageSize: 10, SaturationEnabled: false, Retry: rl.RetryPolicy{MaxAttempts: 1}})
	if err := s.Run(context.Background(), "job", vs, []Runtime{NewRuntime(p, 1000, 1)}, nil); err != nil {
		t.Fatal(err)
	}
	if p.calls != 2 || col.Unique.Load() != 2 || s.Report().SaturationReason != "maximum page limit reached" {
		t.Fatalf("calls=%d unique=%d report=%+v", p.calls, col.Unique.Load(), s.Report())
	}
}

func TestMaxResultsHasDistinctStopReason(t *testing.T) {
	st, col, vs := adaptiveFixture(t)
	p := &sequenceProvider{name: "p", responses: []provider.SearchResponse{{Results: []model.Result{
		{URL: "https://example.test/a", Provider: "p", DiscoveredAt: time.Now()},
		{URL: "https://example.test/b", Provider: "p", DiscoveredAt: time.Now()},
		{URL: "https://example.test/c", Provider: "p", DiscoveredAt: time.Now()},
	}}}}
	s := New(st, col, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Workers: 1, MaxPages: 10, MaxResults: 2, MaxResultsPerQuery: 10, PageSize: 10, Retry: rl.RetryPolicy{MaxAttempts: 1}})
	if err := s.Run(context.Background(), "job", vs, []Runtime{NewRuntime(p, 1000, 1)}, nil); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 || col.Unique.Load() != 2 || s.Report().SaturationReason != "result limit reached" {
		t.Fatalf("calls=%d unique=%d report=%+v", p.calls, col.Unique.Load(), s.Report())
	}
}

func TestConcurrentProvidersRespectExactUniqueCap(t *testing.T) {
	st, col, vs := adaptiveFixture(t)
	results := func(prefix string) []model.Result {
		out := make([]model.Result, 0, 5)
		for i := 0; i < 5; i++ {
			out = append(out, model.Result{URL: fmt.Sprintf("https://example.test/%s-%d", prefix, i), Provider: prefix, DiscoveredAt: time.Now()})
		}
		return out
	}
	a := &sequenceProvider{name: "a", responses: []provider.SearchResponse{{Results: results("a")}}}
	b := &sequenceProvider{name: "b", responses: []provider.SearchResponse{{Results: results("b")}}}
	s := New(st, col, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Workers: 2, MaxPages: 10, MaxResults: 3, MaxResultsPerQuery: 3, PageSize: 10, Retry: rl.RetryPolicy{MaxAttempts: 1}})
	if err := s.Run(context.Background(), "job", vs, []Runtime{NewRuntime(a, 1000, 1), NewRuntime(b, 1000, 1)}, nil); err != nil {
		t.Fatal(err)
	}
	stats, err := st.Stats(context.Background(), "job")
	if err != nil || stats.Unique != 3 || col.Unique.Load() != 3 || col.LimitSkipped.Load() != 7 {
		t.Fatalf("stats=%+v unique=%d skipped=%d err=%v", stats, col.Unique.Load(), col.LimitSkipped.Load(), err)
	}
}

func TestZeroResultLimitsAreUnlimited(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		maxResults, maxPerQuery int
	}{
		{"both unlimited", 0, 0},
		{"job unlimited", 0, 150},
		{"per query unlimited", 150, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, col, vs := adaptiveFixture(t)
			var responses []provider.SearchResponse
			for page := 0; page < 15; page++ {
				var results []model.Result
				for i := 0; i < 10; i++ {
					results = append(results, model.Result{URL: fmt.Sprintf("https://example.test/%03d", page*10+i), Provider: "p", DiscoveredAt: time.Now()})
				}
				responses = append(responses, provider.SearchResponse{Results: results, Done: page == 14})
			}
			p := &sequenceProvider{name: "p", responses: responses}
			s := New(st, col, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Workers: 1, MaxPages: 20, MaxResults: tc.maxResults, MaxResultsPerQuery: tc.maxPerQuery, PageSize: 100, SaturationEnabled: false, Retry: rl.RetryPolicy{MaxAttempts: 1}})
			if err := s.Run(context.Background(), "job", vs, []Runtime{NewRuntime(p, 1000, 1)}, nil); err != nil {
				t.Fatal(err)
			}
			if p.calls != 15 || col.Unique.Load() != 150 {
				t.Fatalf("calls=%d unique=%d", p.calls, col.Unique.Load())
			}
		})
	}
}

func TestDebugPageDiagnosticsExposePaginationAndYield(t *testing.T) {
	st, col, vs := adaptiveFixture(t)
	p := &sequenceProvider{name: "p", responses: []provider.SearchResponse{{Results: []model.Result{{URL: "https://example.test/a", Provider: "p", DiscoveredAt: time.Now()}}, Done: true}}}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := New(st, col, logger, Options{Workers: 1, MaxPages: 2, PageSize: 100, Retry: rl.RetryPolicy{MaxAttempts: 1}})
	if err := s.Run(context.Background(), "job", vs, []Runtime{NewRuntime(p, 1000, 1)}, nil); err != nil {
		t.Fatal(err)
	}
	text := logs.String()
	for _, want := range []string{"logical_page=0", "upstream_page=1", "requested_page_size=100", `msg="provider page processed"`, "provider_results=1", "processed=1", "accepted=1", "new_unique=1", "limit_skipped=0", "outcome=USEFUL_PAGE", "provider_done=true", "coverage=trustworthy"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in logs:\n%s", want, text)
		}
	}
}

func TestUsefulPageWithSourceDegradationRemainsDegraded(t *testing.T) {
	st, col, vs := adaptiveFixture(t)
	r := model.Result{URL: "https://example.test/a", Provider: "searxng", SourceEngines: []string{"bing"}, OriginalQuery: "q", QueryVariant: "q", DiscoveredAt: time.Now()}
	p := &sequenceProvider{name: "searxng", responses: []provider.SearchResponse{{Results: []model.Result{r}, Done: true, Diagnostics: []provider.UpstreamDiagnostic{{Provider: "searxng", Engine: "brave", Classification: provider.UpstreamRateLimited}}}}}
	s := New(st, col, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Workers: 1, MaxPages: 10, PageSize: 10, Retry: rl.RetryPolicy{MaxAttempts: 1}})
	if err := s.Run(context.Background(), "job", vs, []Runtime{NewRuntime(p, 1000, 1)}, nil); err != nil {
		t.Fatal(err)
	}
	report := s.Report()
	if report.UsefulPages != 1 || !report.Degraded || report.Partial || report.ProviderFailures != 0 {
		t.Fatalf("%+v", report)
	}
}

func TestCooldownIsPersistedAndDoesNotBlockByDefault(t *testing.T) {
	st, col, vs := adaptiveFixture(t)
	p := &sequenceProvider{name: "p", errors: []error{&provider.Error{Kind: provider.KindRateLimited, Message: "slow down", RetryAfter: time.Minute}}}
	s := New(st, col, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Workers: 1, PageSize: 10, Retry: rl.RetryPolicy{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: time.Minute}})
	start := time.Now()
	if err := s.Run(context.Background(), "job", vs, []Runtime{NewRuntime(p, 1000, 1)}, nil); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("scheduler waited during default cooldown")
	}
	progress, _ := st.LoadProgress(context.Background(), "job")
	if progress[0].State != "COOLDOWN" || time.Until(progress[0].CooldownUntil) < 50*time.Second {
		t.Fatalf("%+v", progress[0])
	}
	if !s.Report().Partial {
		t.Fatalf("%+v", s.Report())
	}
	if s.Report().ProviderFailures != 1 || s.Report().ErrorPages != 1 {
		t.Fatalf("provider failure/error attempt accounting=%+v", s.Report())
	}
}
