package scheduler

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/HexSilence/dorker/internal/collector"
	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/normalize"
	"github.com/HexSilence/dorker/internal/provider"
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
