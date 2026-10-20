package store

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/provider"
	"github.com/HexSilence/dorker/internal/query"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreResultAndProgress(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateJob(ctx, "j", "{}"); err != nil {
		t.Fatal(err)
	}
	r := model.Result{URL: "https://EXAMPLE.com", NormalizedURL: "https://example.com/", Domain: "example.com", Provider: "a", OriginalQuery: "q", QueryVariant: "q", DiscoveredAt: time.Now()}
	fresh, err := s.InsertResult(ctx, "j", r)
	if err != nil || !fresh {
		t.Fatalf("%v %v", fresh, err)
	}
	r.Provider = "b"
	fresh, err = s.InsertResult(ctx, "j", r)
	if err != nil || fresh {
		t.Fatalf("%v %v", fresh, err)
	}
	st, err := s.Stats(ctx, "j")
	if err != nil || st.Unique != 1 || st.Raw != 2 {
		t.Fatalf("%+v %v", st, err)
	}
	p := Progress{JobID: "j", Provider: "a", OriginalQuery: "q", QueryVariant: "q", Page: 2, State: "READY"}
	if err = s.SaveProgress(ctx, p); err != nil {
		t.Fatal(err)
	}
	ps, err := s.LoadProgress(ctx, "j")
	if err != nil || len(ps) != 1 || ps[0].Page != 2 {
		t.Fatalf("%+v %v", ps, err)
	}
}

func TestJobOptionsPreserveValidationMode(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CreateJob(context.Background(), "j", `{"Validation":"strict"}`); err != nil {
		t.Fatal(err)
	}
	got, err := s.JobOptions(context.Background(), "j")
	if err != nil || got != `{"Validation":"strict"}` {
		t.Fatalf("%q %v", got, err)
	}
}

func TestValidationAndUpstreamAggregation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateJob(ctx, "j", "{}"); err != nil {
		t.Fatal(err)
	}
	if err = s.AddMetrics(ctx, "j", 10, 2, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.AddValidationMetrics(ctx, "j", 6, 3, 1, map[query.ValidationReason]int64{query.ReasonExtensionMismatch: 2, query.ReasonSiteMismatch: 1}); err != nil {
		t.Fatal(err)
	}
	d := provider.UpstreamDiagnostic{Provider: "searxng", Instance: "local", Engine: "brave", Classification: provider.UpstreamRateLimited, Message: "too many requests", Timestamp: time.Now()}
	if err = s.UpstreamDiagnostic(ctx, "j", d); err != nil {
		t.Fatal(err)
	}
	d.Page = 2
	if err = s.UpstreamDiagnostic(ctx, "j", d); err != nil {
		t.Fatal(err)
	}
	if err = s.UpstreamDiagnostic(ctx, "j", d); err != nil {
		t.Fatal(err)
	}
	d.Message = "too many requests (alternate detail)"
	if err = s.UpstreamDiagnostic(ctx, "j", d); err != nil {
		t.Fatal(err)
	}
	st, err := s.Stats(ctx, "j")
	if err != nil {
		t.Fatal(err)
	}
	if st.Accepted != 6 || st.Rejected != 3 || st.UnknownKept != 1 || st.RejectionReasons["extension_mismatch"] != 2 || len(st.UpstreamDegradation) != 1 || st.UpstreamDegradation[0].Count != 3 {
		t.Fatalf("%+v", st)
	}
}

func TestSearXNGInstanceAndEngineProvenance(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateJob(ctx, "j", "{}"); err != nil {
		t.Fatal(err)
	}
	for _, instance := range []string{"primary", "secondary"} {
		r := model.Result{URL: "https://example.com/a", NormalizedURL: "https://example.com/a", Domain: "example.com", Provider: "searxng", ProviderInstance: instance, SourceEngines: []string{"google", "bing"}, OriginalQuery: "q", QueryVariant: "q", Rank: 1, Page: "1", DiscoveredAt: time.Now()}
		if _, err = s.InsertResult(ctx, "j", r); err != nil {
			t.Fatal(err)
		}
	}
	r := model.Result{URL: "https://example.com/a", NormalizedURL: "https://example.com/a", Domain: "example.com", Provider: "brave", OriginalQuery: "q", QueryVariant: "q", Rank: 1, Page: "0", DiscoveredAt: time.Now()}
	if _, err = s.InsertResult(ctx, "j", r); err != nil {
		t.Fatal(err)
	}
	var unique, discoveries int
	if err = s.db.QueryRow(`SELECT count(*) FROM urls`).Scan(&unique); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT count(*) FROM discoveries`).Scan(&discoveries); err != nil {
		t.Fatal(err)
	}
	if unique != 1 || discoveries != 3 {
		t.Fatalf("unique=%d discoveries=%d", unique, discoveries)
	}
	st, err := s.Stats(ctx, "j")
	if err != nil || st.BySourceEngine["google"] != 2 || st.ByProvider["searxng"] != 2 {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestMigrationFromPhase2Schema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	old := `CREATE TABLE jobs(id TEXT PRIMARY KEY,status TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,options_json TEXT NOT NULL DEFAULT '{}');CREATE TABLE urls(id INTEGER PRIMARY KEY,normalized_url TEXT NOT NULL UNIQUE,domain TEXT NOT NULL,first_seen TEXT NOT NULL);CREATE TABLE discoveries(id INTEGER PRIMARY KEY,job_id TEXT NOT NULL REFERENCES jobs(id),url_id INTEGER NOT NULL REFERENCES urls(id),original_url TEXT NOT NULL,title TEXT,snippet TEXT,provider TEXT NOT NULL,original_query TEXT NOT NULL,query_variant TEXT NOT NULL,rank INTEGER,page TEXT,discovered_at TEXT NOT NULL,UNIQUE(job_id,url_id,provider,query_variant,page,rank));INSERT INTO jobs VALUES('j','complete','x','x','{}');INSERT INTO urls VALUES(1,'https://example.com/','example.com','x');INSERT INTO discoveries VALUES(1,'j',1,'https://example.com','','','brave','q','q',1,'0','x');`
	if _, err = db.Exec(old); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var instance, engines string
	if err = s.db.QueryRow(`SELECT provider_instance,source_engines FROM discoveries WHERE id=1`).Scan(&instance, &engines); err != nil {
		t.Fatal(err)
	}
	if instance != "" || engines != "[]" {
		t.Fatalf("%q %q", instance, engines)
	}
	for _, table := range []string{"discovery_jobs", "discovery_hosts", "discovery_edges", "discovery_depth_stats"} {
		var n int
		if err = s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("migration missing %s: n=%d err=%v", table, n, err)
		}
	}
	var oldCount int
	if err = s.db.QueryRow(`SELECT count(*) FROM discoveries WHERE job_id='j'`).Scan(&oldCount); err != nil || oldCount != 1 {
		t.Fatalf("old discoveries not preserved: %d %v", oldCount, err)
	}
}

func TestBatchDedupPreservesProviderProvenance(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateJob(ctx, "j", "{}"); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"google-api", "brave", "serpapi"} {
		results := []model.Result{
			{URL: "https://example.com/a", NormalizedURL: "https://example.com/a", Domain: "example.com", Provider: name, OriginalQuery: "q", QueryVariant: "q", Rank: 1, Page: "0", DiscoveredAt: time.Now()},
			{URL: fmt.Sprintf("https://example.com/%d", i), NormalizedURL: fmt.Sprintf("https://example.com/%d", i), Domain: "example.com", Provider: name, OriginalQuery: "q", QueryVariant: "q", Rank: 2, Page: "0", DiscoveredAt: time.Now()},
		}
		if _, err = s.InsertResults(ctx, "j", results); err != nil {
			t.Fatal(err)
		}
	}
	var unique, provenance int
	if err = s.db.QueryRowContext(ctx, `SELECT count(DISTINCT url_id) FROM discoveries WHERE job_id='j'`).Scan(&unique); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM discoveries d JOIN urls u ON u.id=d.url_id WHERE d.job_id='j' AND u.normalized_url='https://example.com/a'`).Scan(&provenance); err != nil {
		t.Fatal(err)
	}
	if unique != 4 || provenance != 3 {
		t.Fatalf("unique=%d provenance=%d", unique, provenance)
	}
}

func TestAdaptiveProgressAndPageOutcomeAreAdditiveAndIdempotent(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "adaptive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CreateJob(ctx, "j", "{}"); err != nil {
		t.Fatal(err)
	}
	p := Progress{JobID: "j", Provider: "searxng", OriginalQuery: "q", QueryVariant: "q", State: "COOLDOWN", ZeroYieldStreak: 2, LastOutcome: "DUPLICATE_ONLY", LastErrorClass: "rate_limited", CooldownUntil: time.Now().Add(time.Minute)}
	if err = s.SaveProgress(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadProgress(ctx, "j")
	if err != nil || len(got) != 1 || got[0].ZeroYieldStreak != 2 || got[0].LastOutcome != "DUPLICATE_ONLY" || got[0].LastErrorClass != "rate_limited" {
		t.Fatalf("progress=%+v err=%v", got, err)
	}
	o := PageOutcome{JobID: "j", Provider: "searxng", QueryVariant: "q", Page: 1, Outcome: "USEFUL_PAGE", Raw: 10, Accepted: 9, Rejected: 1, Duplicates: 2, Unique: 7}
	if err = s.SavePageOutcome(ctx, o); err != nil {
		t.Fatal(err)
	}
	o.Unique = 99
	if err = s.SavePageOutcome(ctx, o); err != nil {
		t.Fatal(err)
	}
	var unique int
	if err = s.db.QueryRowContext(ctx, `SELECT unique_yield FROM page_outcomes WHERE job_id='j'`).Scan(&unique); err != nil {
		t.Fatal(err)
	}
	if unique != 7 {
		t.Fatalf("idempotent unique_yield=%d", unique)
	}
}

func BenchmarkInsertResult(b *testing.B) {
	s, _ := Open(filepath.Join(b.TempDir(), "x.db"))
	defer s.Close()
	ctx := context.Background()
	_ = s.CreateJob(ctx, "j", "{}")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := model.Result{URL: "https://example.com/x", NormalizedURL: "https://example.com/x", Domain: "example.com", Provider: "p", OriginalQuery: "q", QueryVariant: "q", Rank: i, Page: "1", DiscoveredAt: time.Now()}
		_, _ = s.InsertResult(ctx, "j", r)
	}
}
