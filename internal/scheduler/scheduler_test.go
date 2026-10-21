package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HexSilence/dorker/internal/collector"
	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/normalize"
	"github.com/HexSilence/dorker/internal/provider"
	"github.com/HexSilence/dorker/internal/provider/mock"
	"github.com/HexSilence/dorker/internal/query"
	rl "github.com/HexSilence/dorker/internal/ratelimit"
	"github.com/HexSilence/dorker/internal/store"
	_ "modernc.org/sqlite"
)

type transportFailureProvider struct{}

func (transportFailureProvider) Name() string { return "transport" }
func (transportFailureProvider) Health() provider.HealthStatus {
	return provider.HealthStatus{State: provider.Ready}
}
func (transportFailureProvider) Search(context.Context, provider.SearchRequest) (provider.SearchResponse, error) {
	return provider.SearchResponse{}, &provider.Error{Kind: provider.KindConnection, Message: "connection refused"}
}

func TestTransportFailureDebugExplainsMissingHTTPStatus(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "transport.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.CreateJob(ctx, "job", "{}"); err != nil {
		t.Fatal(err)
	}
	variants := []model.QueryVariant{{Original: "q", Variant: "q"}}
	if err = st.AddVariants(ctx, "job", variants); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	col := &collector.Collector{Store: st, JobID: "job", Normalizer: normalize.New(normalize.Options{})}
	s := New(st, col, log, Options{Workers: 1, MaxPages: 1, PageSize: 10, Retry: rl.RetryPolicy{MaxAttempts: 1}})
	if err = s.Run(ctx, "job", variants, []Runtime{NewRuntime(transportFailureProvider{}, 1000, 1)}, nil); err != nil {
		t.Fatal(err)
	}
	text := logs.String()
	for _, want := range []string{"http_status=0", "response_received=false", "retry_classification=connection_refused", `error="connection refused"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	stats, err := st.Stats(ctx, "job")
	if err != nil || len(stats.ProviderFailures) != 1 || stats.ProviderFailures[0].Kind != "connection_refused" {
		t.Fatalf("stats=%+v err=%v", stats.ProviderFailures, err)
	}
}

type provenanceBreakingProvider struct{}

func (provenanceBreakingProvider) Name() string { return "broken" }
func (provenanceBreakingProvider) Health() provider.HealthStatus {
	return provider.HealthStatus{State: provider.Ready}
}
func (provenanceBreakingProvider) Search(context.Context, provider.SearchRequest) (provider.SearchResponse, error) {
	return provider.SearchResponse{Done: true, Results: []model.Result{{URL: "https://attacker.test/", OriginalQuery: "unscoped"}, {URL: "https://api.example.com/", OriginalQuery: "unscoped"}}}, nil
}

func TestSchedulerOwnsScopeProvenance(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "scope.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.CreateJob(ctx, "job", "{}"); err != nil {
		t.Fatal(err)
	}
	vs := []model.QueryVariant{{Original: "site:example.com", Variant: "site:example.com"}}
	if err = st.AddVariants(ctx, "job", vs); err != nil {
		t.Fatal(err)
	}
	col := &collector.Collector{Store: st, JobID: "job", Normalizer: normalize.New(normalize.Options{}), ValidationMode: query.ValidationOff}
	s := New(st, col, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Workers: 1, MaxPages: 1, PageSize: 10, Retry: rl.RetryPolicy{MaxAttempts: 1}})
	if err = s.Run(ctx, "job", vs, []Runtime{NewRuntime(provenanceBreakingProvider{}, 1000, 1)}, nil); err != nil {
		t.Fatal(err)
	}
	stats, err := st.Stats(ctx, "job")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Unique != 1 || col.Rejected.Load() != 1 {
		t.Fatalf("unique=%d rejected=%d", stats.Unique, col.Rejected.Load())
	}
}

func TestCancellationFailureIsolationAndResume(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.CreateJob(ctx, "job", "{}"); err != nil {
		t.Fatal(err)
	}
	vs := []model.QueryVariant{{Original: "q", Variant: "q"}}
	if err = st.AddVariants(ctx, "job", vs); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	col := &collector.Collector{Store: st, JobID: "job", Normalizer: normalize.New(normalize.Options{})}
	col.OnUnique = func(string) {
		if col.Unique.Load() >= 25 {
			cancel()
		}
	}
	a := mock.New(mock.Config{Name: "limited", Total: 100, PageSize: 10, RateLimitPage: 2, TemporaryPage: -1, QuotaPage: -1, Delay: time.Millisecond, BaseURL: "https://results.test"})
	b := mock.New(mock.Config{Name: "healthy", Total: 100, PageSize: 10, RateLimitPage: -1, TemporaryPage: -1, QuotaPage: -1, Delay: time.Millisecond, BaseURL: "https://results.test"})
	opts := Options{Workers: 2, MaxResults: 100, MaxResultsPerQuery: 100, PageSize: 10, WaitForRecovery: true, MaxRecoveryWait: time.Second, Retry: rl.RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 20 * time.Millisecond}}
	s := New(st, col, slog.New(slog.NewTextHandler(io.Discard, nil)), opts)
	if err = s.Run(cctx, "job", vs, []Runtime{NewRuntime(a, 1000, 10), NewRuntime(b, 1000, 10)}, nil); err == nil {
		t.Fatal("expected cancellation")
	}
	progress, err := st.LoadProgress(ctx, "job")
	if err != nil {
		t.Fatal(err)
	}
	completedPage := false
	for _, p := range progress {
		if p.Page > 0 {
			completedPage = true
		}
	}
	if !completedPage {
		t.Fatal("no checkpoint persisted before cancellation")
	}
	col2 := &collector.Collector{Store: st, JobID: "job", Normalizer: normalize.New(normalize.Options{})}
	s2 := New(st, col2, slog.New(slog.NewTextHandler(io.Discard, nil)), opts)
	if err = s2.Run(ctx, "job", vs, []Runtime{NewRuntime(a, 1000, 10), NewRuntime(b, 1000, 10)}, progress); err != nil {
		t.Fatal(err)
	}
	stats, err := st.Stats(ctx, "job")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Unique != 100 {
		t.Fatalf("unique=%d raw=%d", stats.Unique, stats.Raw)
	}
	progress, err = st.LoadProgress(ctx, "job")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range progress {
		if p.State != "COMPLETE" {
			t.Fatalf("%s state=%s", p.Provider, p.State)
		}
	}
}

func TestResultFailureDoesNotAdvanceCheckpoint(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.CreateJob(ctx, "job", "{}"); err != nil {
		t.Fatal(err)
	}
	vs := []model.QueryVariant{{Original: "q", Variant: "q"}}
	if err = st.AddVariants(ctx, "job", vs); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER fail_results BEFORE INSERT ON discoveries BEGIN SELECT RAISE(ABORT, 'injected'); END;`); err != nil {
		t.Fatal(err)
	}
	col := &collector.Collector{Store: st, JobID: "job", Normalizer: normalize.New(normalize.Options{})}
	p := mock.New(mock.Config{Name: "real-shaped", Total: 30, PageSize: 10, RateLimitPage: -1, TemporaryPage: -1, QuotaPage: -1, BaseURL: "https://example.com"})
	s := New(st, col, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Workers: 1, MaxPages: 3, PageSize: 10, Retry: rl.RetryPolicy{MaxAttempts: 1}})
	if err = s.Run(ctx, "job", vs, []Runtime{NewRuntime(p, 1000, 10)}, nil); err == nil {
		t.Fatal("expected persistence failure")
	}
	progress, err := st.LoadProgress(ctx, "job")
	if err != nil {
		t.Fatal(err)
	}
	if len(progress) != 1 || progress[0].Page != 0 {
		t.Fatalf("%+v", progress)
	}
}
