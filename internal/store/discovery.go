package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/HexSilence/dorker/internal/model"
)

type DiscoveryJob struct {
	JobID, SeedQuery, OptionsJSON, StopReason, OutputPath string
	CurrentDepth, LowGainStreak                           int
	StartedAt, FinishedAt                                 time.Time
}

type DiscoveryHost struct {
	Host              string    `json:"host"`
	RegistrableDomain string    `json:"registrable_domain,omitempty"`
	ParentHost        string    `json:"parent_host,omitempty"`
	State             string    `json:"state"`
	Depth             int       `json:"depth"`
	Attempts          int       `json:"expansion_attempts"`
	SeedDerived       bool      `json:"seed_derived"`
	FirstSeen         time.Time `json:"first_seen"`
	LastSeen          time.Time `json:"last_seen"`
}

type DiscoveryEdge struct {
	ParentHost       string    `json:"parent_host,omitempty"`
	ChildHost        string    `json:"child_host"`
	Query            string    `json:"query"`
	Provider         string    `json:"provider"`
	ProviderInstance string    `json:"provider_instance,omitempty"`
	SourceEngine     string    `json:"source_engine,omitempty"`
	FirstSeen        time.Time `json:"first_seen"`
}

type DiscoveryDepthStat struct {
	Depth            int     `json:"depth"`
	AcceptedURLs     int     `json:"accepted_urls"`
	NewURLs          int     `json:"new_unique_urls"`
	HostsObserved    int     `json:"hosts_observed"`
	NewHosts         int     `json:"new_unique_hosts"`
	RootsObserved    int     `json:"registrable_domains_observed"`
	NewRoots         int     `json:"new_registrable_domains"`
	QueriesExecuted  int     `json:"queries_executed"`
	ProviderFailures int     `json:"provider_failures"`
	RejectedResults  int     `json:"rejected_results"`
	DuplicateResults int     `json:"duplicate_results"`
	GainRatio        float64 `json:"host_gain_ratio"`
	Trustworthy      bool    `json:"trustworthy"`
}

func (s *Store) CreateDiscoveryJob(ctx context.Context, d DiscoveryJob) error {
	if d.StartedAt.IsZero() {
		d.StartedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO discovery_jobs(job_id,seed_query,options_json,current_depth,low_gain_streak,stop_reason,output_path,started_at) VALUES(?,?,?,?,?,?,?,?)`, d.JobID, d.SeedQuery, d.OptionsJSON, d.CurrentDepth, d.LowGainStreak, d.StopReason, d.OutputPath, d.StartedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) DiscoveryJob(ctx context.Context, id string) (DiscoveryJob, error) {
	var d DiscoveryJob
	var start, finish string
	err := s.db.QueryRowContext(ctx, `SELECT job_id,seed_query,options_json,current_depth,low_gain_streak,stop_reason,output_path,started_at,COALESCE(finished_at,'') FROM discovery_jobs WHERE job_id=?`, id).Scan(&d.JobID, &d.SeedQuery, &d.OptionsJSON, &d.CurrentDepth, &d.LowGainStreak, &d.StopReason, &d.OutputPath, &start, &finish)
	d.StartedAt, _ = time.Parse(time.RFC3339Nano, start)
	d.FinishedAt, _ = time.Parse(time.RFC3339Nano, finish)
	return d, err
}

func (s *Store) SetDiscoveryOutputPath(ctx context.Context, id, path string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE discovery_jobs SET output_path=? WHERE job_id=?`, path, id)
	return err
}

func (s *Store) UpdateDiscoveryJob(ctx context.Context, id string, depth, streak int, reason string, finished bool) error {
	var at any
	if finished {
		at = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE discovery_jobs SET current_depth=?,low_gain_streak=?,stop_reason=?,finished_at=? WHERE job_id=?`, depth, streak, reason, at, id)
	return err
}

// RecoverDiscoveryFrontier makes a host interrupted between its state change
// and the completion transaction eligible for deterministic retry.
func (s *Store) RecoverDiscoveryFrontier(ctx context.Context, job string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE discovery_hosts SET state='QUEUED' WHERE job_id=? AND state='EXPANDING'`, job)
	return err
}

func (s *Store) UpsertDiscoveryHost(ctx context.Context, job string, h DiscoveryHost) (bool, error) {
	now := h.LastSeen
	if now.IsZero() {
		now = time.Now().UTC()
	}
	first := h.FirstSeen
	if first.IsZero() {
		first = now
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO discovery_hosts(job_id,host,registrable_domain,depth,parent_host,state,attempts,seed_derived,first_seen,last_seen) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(job_id,host) DO NOTHING`, job, h.Host, h.RegistrableDomain, h.Depth, h.ParentHost, h.State, h.Attempts, h.SeedDerived, first.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		_, err = s.db.ExecContext(ctx, `UPDATE discovery_hosts SET last_seen=? WHERE job_id=? AND host=?`, now.Format(time.RFC3339Nano), job, h.Host)
	}
	return n == 1, err
}

func (s *Store) AddDiscoveryEdge(ctx context.Context, job string, e DiscoveryEdge) error {
	if e.FirstSeen.IsZero() {
		e.FirstSeen = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO discovery_edges(job_id,parent_host,child_host,query,provider,provider_instance,source_engine,first_seen) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, job, e.ParentHost, e.ChildHost, e.Query, e.Provider, e.ProviderInstance, e.SourceEngine, e.FirstSeen.Format(time.RFC3339Nano))
	return err
}

func (s *Store) DiscoveryFrontier(ctx context.Context, job string, depth, limit int) ([]DiscoveryHost, error) {
	q := `SELECT host,registrable_domain,depth,parent_host,state,attempts,seed_derived,first_seen,last_seen FROM discovery_hosts WHERE job_id=? AND depth=? AND state IN ('QUEUED','FAILED_RETRYABLE') ORDER BY host`
	args := []any{job, depth}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DiscoveryHost
	for rows.Next() {
		var h DiscoveryHost
		var seed int
		var first, last string
		if err = rows.Scan(&h.Host, &h.RegistrableDomain, &h.Depth, &h.ParentHost, &h.State, &h.Attempts, &seed, &first, &last); err != nil {
			return nil, err
		}
		h.SeedDerived = seed != 0
		h.FirstSeen, _ = time.Parse(time.RFC3339Nano, first)
		h.LastSeen, _ = time.Parse(time.RFC3339Nano, last)
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) SetDiscoveryHostsState(ctx context.Context, job string, hosts []string, state string, increment bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, h := range hosts {
		q := `UPDATE discovery_hosts SET state=?,last_seen=? WHERE job_id=? AND host=?`
		if increment {
			q = `UPDATE discovery_hosts SET state=?,attempts=attempts+1,last_seen=? WHERE job_id=? AND host=?`
		}
		if _, err = tx.ExecContext(ctx, q, state, time.Now().UTC().Format(time.RFC3339Nano), job, h); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SaveDiscoveryDepth(ctx context.Context, job string, d DiscoveryDepthStat) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO discovery_depth_stats(job_id,depth,accepted_urls,new_urls,hosts_observed,new_hosts,roots_observed,new_roots,queries_executed,provider_failures,rejected_results,duplicate_results,gain_ratio,trustworthy) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(job_id,depth) DO UPDATE SET accepted_urls=excluded.accepted_urls,new_urls=excluded.new_urls,hosts_observed=excluded.hosts_observed,new_hosts=excluded.new_hosts,roots_observed=excluded.roots_observed,new_roots=excluded.new_roots,queries_executed=excluded.queries_executed,provider_failures=excluded.provider_failures,rejected_results=excluded.rejected_results,duplicate_results=excluded.duplicate_results,gain_ratio=excluded.gain_ratio,trustworthy=excluded.trustworthy`, job, d.Depth, d.AcceptedURLs, d.NewURLs, d.HostsObserved, d.NewHosts, d.RootsObserved, d.NewRoots, d.QueriesExecuted, d.ProviderFailures, d.RejectedResults, d.DuplicateResults, d.GainRatio, d.Trustworthy)
	return err
}

func (s *Store) DiscoveryDepths(ctx context.Context, job string) ([]DiscoveryDepthStat, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT depth,accepted_urls,new_urls,hosts_observed,new_hosts,roots_observed,new_roots,queries_executed,provider_failures,rejected_results,duplicate_results,gain_ratio,trustworthy FROM discovery_depth_stats WHERE job_id=? ORDER BY depth`, job)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DiscoveryDepthStat
	for rows.Next() {
		var d DiscoveryDepthStat
		var trust int
		if err = rows.Scan(&d.Depth, &d.AcceptedURLs, &d.NewURLs, &d.HostsObserved, &d.NewHosts, &d.RootsObserved, &d.NewRoots, &d.QueriesExecuted, &d.ProviderFailures, &d.RejectedResults, &d.DuplicateResults, &d.GainRatio, &trust); err != nil {
			return nil, err
		}
		d.Trustworthy = trust != 0
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) DiscoveryCounts(ctx context.Context, job string) (hosts, roots int, error error) {
	error = s.db.QueryRowContext(ctx, `SELECT count(*),count(DISTINCT NULLIF(registrable_domain,'')) FROM discovery_hosts WHERE job_id=?`, job).Scan(&hosts, &roots)
	return
}

func (s *Store) DiscoveryHosts(ctx context.Context, job string) ([]DiscoveryHost, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT host,registrable_domain,depth,parent_host,state,attempts,seed_derived,first_seen,last_seen FROM discovery_hosts WHERE job_id=? ORDER BY host`, job)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DiscoveryHost
	for rows.Next() {
		var h DiscoveryHost
		var seed int
		var first, last string
		if err = rows.Scan(&h.Host, &h.RegistrableDomain, &h.Depth, &h.ParentHost, &h.State, &h.Attempts, &seed, &first, &last); err != nil {
			return nil, err
		}
		h.SeedDerived = seed != 0
		h.FirstSeen, _ = time.Parse(time.RFC3339Nano, first)
		h.LastSeen, _ = time.Parse(time.RFC3339Nano, last)
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) DiscoveryEdges(ctx context.Context, job string) ([]DiscoveryEdge, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT parent_host,child_host,query,provider,provider_instance,source_engine,first_seen FROM discovery_edges WHERE job_id=? ORDER BY child_host,parent_host,query,provider,provider_instance,source_engine`, job)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DiscoveryEdge
	for rows.Next() {
		var e DiscoveryEdge
		var seen string
		if err = rows.Scan(&e.ParentHost, &e.ChildHost, &e.Query, &e.Provider, &e.ProviderInstance, &e.SourceEngine, &seen); err != nil {
			return nil, err
		}
		e.FirstSeen, _ = time.Parse(time.RFC3339Nano, seen)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) ResultsForOriginalQuery(ctx context.Context, job, query string) ([]model.Result, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.normalized_url,u.domain,d.provider,d.provider_instance,d.source_engines,d.original_query,d.query_variant,d.discovered_at FROM urls u JOIN discoveries d ON d.url_id=u.id WHERE d.job_id=? AND d.original_query=? ORDER BY u.normalized_url,d.provider,d.provider_instance`, job, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Result
	for rows.Next() {
		var r model.Result
		var engines, at string
		if err = rows.Scan(&r.NormalizedURL, &r.Domain, &r.Provider, &r.ProviderInstance, &engines, &r.OriginalQuery, &r.QueryVariant, &at); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(engines), &r.SourceEngines)
		r.DiscoveredAt, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) DiscoveryJobExists(ctx context.Context, id string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM discovery_jobs WHERE job_id=?`, id).Scan(&n)
	return n == 1, err
}
