package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/provider"
	"github.com/HexSilence/dorker/internal/query"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }
type Progress struct {
	JobID, Provider, OriginalQuery, QueryVariant, Cursor, State, LastError string
	LastOutcome, LastErrorClass, ExhaustedReason                           string
	Page, Attempts, ZeroYieldStreak, MarginalUniqueYield                   int
	CooldownUntil                                                          time.Time
}
type PageOutcome struct {
	JobID, Provider, QueryVariant, Outcome string
	Page, Raw, Accepted, Rejected          int
	UnknownKept, Duplicates, Unique        int
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.init(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Flush(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)")
	return err
}

func (s *Store) init(ctx context.Context) error {
	const schema = `
PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS jobs(id TEXT PRIMARY KEY, status TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, options_json TEXT NOT NULL DEFAULT '{}', run_owner TEXT NOT NULL DEFAULT '', lease_until TEXT);
CREATE TABLE IF NOT EXISTS queries(id INTEGER PRIMARY KEY, job_id TEXT NOT NULL REFERENCES jobs(id), original TEXT NOT NULL, UNIQUE(job_id,original));
CREATE TABLE IF NOT EXISTS query_variants(id INTEGER PRIMARY KEY, query_id INTEGER NOT NULL REFERENCES queries(id), variant TEXT NOT NULL, ordinal INTEGER NOT NULL, UNIQUE(query_id,variant));
CREATE TABLE IF NOT EXISTS urls(id INTEGER PRIMARY KEY, normalized_url TEXT NOT NULL UNIQUE, domain TEXT NOT NULL, first_seen TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS discoveries(id INTEGER PRIMARY KEY, job_id TEXT NOT NULL REFERENCES jobs(id), url_id INTEGER NOT NULL REFERENCES urls(id), original_url TEXT NOT NULL, title TEXT, snippet TEXT, provider TEXT NOT NULL, provider_instance TEXT NOT NULL DEFAULT '', source_engines TEXT NOT NULL DEFAULT '[]', original_query TEXT NOT NULL, query_variant TEXT NOT NULL, rank INTEGER, page TEXT, discovered_at TEXT NOT NULL, UNIQUE(job_id,url_id,provider,provider_instance,query_variant,page,rank));
CREATE TABLE IF NOT EXISTS provider_progress(job_id TEXT NOT NULL REFERENCES jobs(id), provider TEXT NOT NULL, original_query TEXT NOT NULL, query_variant TEXT NOT NULL, cursor TEXT NOT NULL DEFAULT '', page INTEGER NOT NULL DEFAULT 0, state TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, cooldown_until TEXT, last_error TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL, PRIMARY KEY(job_id,provider,query_variant));
CREATE TABLE IF NOT EXISTS provider_events(id INTEGER PRIMARY KEY, job_id TEXT NOT NULL, provider TEXT NOT NULL, kind TEXT NOT NULL, message TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS job_metrics(job_id TEXT PRIMARY KEY REFERENCES jobs(id), raw INTEGER NOT NULL DEFAULT 0, accepted INTEGER NOT NULL DEFAULT 0, rejected INTEGER NOT NULL DEFAULT 0, unknown_kept INTEGER NOT NULL DEFAULT 0, duplicates INTEGER NOT NULL DEFAULT 0, malformed INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS validation_reasons(job_id TEXT NOT NULL REFERENCES jobs(id), reason TEXT NOT NULL, count INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(job_id,reason));
CREATE TABLE IF NOT EXISTS upstream_engine_events(job_id TEXT NOT NULL REFERENCES jobs(id), provider TEXT NOT NULL, provider_instance TEXT NOT NULL DEFAULT '', engine TEXT NOT NULL, classification TEXT NOT NULL, message TEXT NOT NULL, count INTEGER NOT NULL DEFAULT 1, first_seen TEXT NOT NULL, last_seen TEXT NOT NULL, last_query TEXT NOT NULL DEFAULT '', last_page INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(job_id,provider,provider_instance,engine,classification,message,last_query,last_page));
CREATE TABLE IF NOT EXISTS page_outcomes(job_id TEXT NOT NULL REFERENCES jobs(id), provider TEXT NOT NULL, query_variant TEXT NOT NULL, page INTEGER NOT NULL, outcome TEXT NOT NULL, raw INTEGER NOT NULL DEFAULT 0, accepted INTEGER NOT NULL DEFAULT 0, rejected INTEGER NOT NULL DEFAULT 0, unknown_kept INTEGER NOT NULL DEFAULT 0, duplicates INTEGER NOT NULL DEFAULT 0, unique_yield INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, PRIMARY KEY(job_id,provider,query_variant,page));
CREATE TABLE IF NOT EXISTS discovery_jobs(job_id TEXT PRIMARY KEY REFERENCES jobs(id), seed_query TEXT NOT NULL, options_json TEXT NOT NULL, current_depth INTEGER NOT NULL DEFAULT 0, low_gain_streak INTEGER NOT NULL DEFAULT 0, stop_reason TEXT NOT NULL DEFAULT '', output_path TEXT NOT NULL DEFAULT '', started_at TEXT NOT NULL, finished_at TEXT);
CREATE TABLE IF NOT EXISTS discovery_hosts(job_id TEXT NOT NULL REFERENCES jobs(id), host TEXT NOT NULL, registrable_domain TEXT NOT NULL DEFAULT '', depth INTEGER NOT NULL, parent_host TEXT NOT NULL DEFAULT '', state TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, seed_derived INTEGER NOT NULL DEFAULT 0, first_seen TEXT NOT NULL, last_seen TEXT NOT NULL, PRIMARY KEY(job_id,host));
CREATE TABLE IF NOT EXISTS discovery_edges(job_id TEXT NOT NULL REFERENCES jobs(id), parent_host TEXT NOT NULL DEFAULT '', child_host TEXT NOT NULL, query TEXT NOT NULL, provider TEXT NOT NULL DEFAULT '', provider_instance TEXT NOT NULL DEFAULT '', source_engine TEXT NOT NULL DEFAULT '', first_seen TEXT NOT NULL, PRIMARY KEY(job_id,parent_host,child_host,query,provider,provider_instance,source_engine));
CREATE TABLE IF NOT EXISTS discovery_depth_stats(job_id TEXT NOT NULL REFERENCES jobs(id), depth INTEGER NOT NULL, accepted_urls INTEGER NOT NULL DEFAULT 0, new_urls INTEGER NOT NULL DEFAULT 0, hosts_observed INTEGER NOT NULL DEFAULT 0, new_hosts INTEGER NOT NULL DEFAULT 0, roots_observed INTEGER NOT NULL DEFAULT 0, new_roots INTEGER NOT NULL DEFAULT 0, queries_executed INTEGER NOT NULL DEFAULT 0, provider_failures INTEGER NOT NULL DEFAULT 0, rejected_results INTEGER NOT NULL DEFAULT 0, duplicate_results INTEGER NOT NULL DEFAULT 0, gain_ratio REAL NOT NULL DEFAULT 0, trustworthy INTEGER NOT NULL DEFAULT 1, PRIMARY KEY(job_id,depth));
CREATE INDEX IF NOT EXISTS idx_discovery_hosts_frontier ON discovery_hosts(job_id,state,depth,host);
CREATE INDEX IF NOT EXISTS idx_discovery_hosts_root ON discovery_hosts(job_id,registrable_domain);
CREATE INDEX IF NOT EXISTS idx_discovery_edges_child ON discovery_edges(job_id,child_host);
CREATE INDEX IF NOT EXISTS idx_urls_domain ON urls(domain); CREATE INDEX IF NOT EXISTS idx_discoveries_job ON discoveries(job_id); CREATE INDEX IF NOT EXISTS idx_discoveries_job_query ON discoveries(job_id,original_query); CREATE INDEX IF NOT EXISTS idx_discoveries_provider ON discoveries(provider); CREATE INDEX IF NOT EXISTS idx_progress_job ON provider_progress(job_id);`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return err
	}
	// Additive migration for databases created before SearXNG provenance.
	if err := s.ensureColumn(ctx, "discoveries", "provider_instance", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "discoveries", "source_engines", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}
	for _, col := range []struct{ name, def string }{{"accepted", "INTEGER NOT NULL DEFAULT 0"}, {"rejected", "INTEGER NOT NULL DEFAULT 0"}, {"unknown_kept", "INTEGER NOT NULL DEFAULT 0"}} {
		if err := s.ensureColumn(ctx, "job_metrics", col.name, col.def); err != nil {
			return err
		}
	}
	for _, col := range []struct{ name, def string }{
		{"zero_yield_streak", "INTEGER NOT NULL DEFAULT 0"},
		{"marginal_unique_yield", "INTEGER NOT NULL DEFAULT 0"},
		{"last_outcome", "TEXT NOT NULL DEFAULT ''"},
		{"last_error_class", "TEXT NOT NULL DEFAULT ''"},
		{"exhausted_reason", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureColumn(ctx, "provider_progress", col.name, col.def); err != nil {
			return err
		}
	}
	for _, col := range []struct{ name, def string }{
		{"run_owner", "TEXT NOT NULL DEFAULT ''"},
		{"lease_until", "TEXT"},
	} {
		if err := s.ensureColumn(ctx, "jobs", col.name, col.def); err != nil {
			return err
		}
	}
	if err := s.ensureColumn(ctx, "discovery_jobs", "output_path", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return s.migrateDiscoveryIdentity(ctx)
}

func (s *Store) migrateDiscoveryIdentity(ctx context.Context) error {
	var sqlText string
	if err := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='discoveries'`).Scan(&sqlText); err != nil {
		return err
	}
	compact := strings.NewReplacer(" ", "", "\n", "", "\t", "").Replace(sqlText)
	if strings.Contains(compact, "provider,provider_instance,query_variant,page,rank") {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`ALTER TABLE discoveries RENAME TO discoveries_v1`,
		`CREATE TABLE discoveries(id INTEGER PRIMARY KEY, job_id TEXT NOT NULL REFERENCES jobs(id), url_id INTEGER NOT NULL REFERENCES urls(id), original_url TEXT NOT NULL, title TEXT, snippet TEXT, provider TEXT NOT NULL, provider_instance TEXT NOT NULL DEFAULT '', source_engines TEXT NOT NULL DEFAULT '[]', original_query TEXT NOT NULL, query_variant TEXT NOT NULL, rank INTEGER, page TEXT, discovered_at TEXT NOT NULL, UNIQUE(job_id,url_id,provider,provider_instance,query_variant,page,rank))`,
		`INSERT INTO discoveries(id,job_id,url_id,original_url,title,snippet,provider,provider_instance,source_engines,original_query,query_variant,rank,page,discovered_at) SELECT id,job_id,url_id,original_url,title,snippet,provider,provider_instance,source_engines,original_query,query_variant,rank,page,discovered_at FROM discoveries_v1`,
		`DROP TABLE discoveries_v1`,
		`CREATE INDEX IF NOT EXISTS idx_discoveries_job ON discoveries(job_id)`,
		`CREATE INDEX IF NOT EXISTS idx_discoveries_job_query ON discoveries(job_id,original_query)`,
		`CREATE INDEX IF NOT EXISTS idx_discoveries_provider ON discoveries(provider)`,
	}
	for _, q := range statements {
		if _, err = tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ensureColumn(ctx context.Context, table, column, definition string) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt any
		if err = rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		found = found || name == column
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = s.db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+column+" "+definition)
	return err
}

func (s *Store) CreateJob(ctx context.Context, id, options string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `INSERT INTO jobs(id,status,created_at,updated_at,options_json) VALUES(?,?,?,?,?)`, id, "PENDING", now, now, options)
	return err
}
func (s *Store) SetJobStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?,updated_at=? WHERE id=?`, status, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}
func (s *Store) AddVariants(ctx context.Context, job string, vs []model.QueryVariant) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range vs {
		r, err := tx.ExecContext(ctx, `INSERT INTO queries(job_id,original) VALUES(?,?) ON CONFLICT DO NOTHING`, job, v.Original)
		if err != nil {
			return err
		}
		_ = r
		var qid int64
		if err = tx.QueryRowContext(ctx, `SELECT id FROM queries WHERE job_id=? AND original=?`, job, v.Original).Scan(&qid); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO query_variants(query_id,variant,ordinal) VALUES(?,?,?) ON CONFLICT DO NOTHING`, qid, v.Variant, v.Ordinal); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) InsertResult(ctx context.Context, job string, r model.Result) (bool, error) {
	fresh, err := s.InsertResults(ctx, job, []model.Result{r})
	if err != nil {
		return false, err
	}
	return fresh[0], nil
}

// InsertResults persists one provider page in a single transaction. The return
// slice reports whether each URL was new to this job (provenance is retained
// even when a URL was already discovered by another provider).
func (s *Store) InsertResults(ctx context.Context, job string, results []model.Result) ([]bool, error) {
	fresh := make([]bool, len(results))
	if len(results) == 0 {
		return fresh, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for i, r := range results {
		now := r.DiscoveredAt.UTC().Format(time.RFC3339Nano)
		res, e := tx.ExecContext(ctx, `INSERT INTO urls(normalized_url,domain,first_seen) VALUES(?,?,?) ON CONFLICT(normalized_url) DO NOTHING`, r.NormalizedURL, r.Domain, now)
		if e != nil {
			return nil, e
		}
		inserted, e := res.RowsAffected()
		if e != nil {
			return nil, e
		}
		var uid int64
		if e = tx.QueryRowContext(ctx, `SELECT id FROM urls WHERE normalized_url=?`, r.NormalizedURL).Scan(&uid); e != nil {
			return nil, e
		}
		var existed int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM discoveries WHERE job_id=? AND url_id=?`, job, uid).Scan(&existed); e != nil {
			return nil, e
		}
		engines, _ := json.Marshal(r.SourceEngines)
		_, e = tx.ExecContext(ctx, `INSERT INTO discoveries(job_id,url_id,original_url,title,snippet,provider,provider_instance,source_engines,original_query,query_variant,rank,page,discovered_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, job, uid, r.URL, r.Title, r.Snippet, r.Provider, r.ProviderInstance, string(engines), r.OriginalQuery, r.QueryVariant, r.Rank, r.Page, now)
		if e != nil {
			return nil, e
		}
		fresh[i] = existed == 0 && inserted >= 0
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return fresh, nil
}

func (s *Store) SaveProgress(ctx context.Context, p Progress) error {
	var cd any
	if !p.CooldownUntil.IsZero() {
		cd = p.CooldownUntil.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO provider_progress(job_id,provider,original_query,query_variant,cursor,page,state,attempts,cooldown_until,last_error,zero_yield_streak,marginal_unique_yield,last_outcome,last_error_class,exhausted_reason,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(job_id,provider,query_variant) DO UPDATE SET cursor=excluded.cursor,page=excluded.page,state=excluded.state,attempts=excluded.attempts,cooldown_until=excluded.cooldown_until,last_error=excluded.last_error,zero_yield_streak=excluded.zero_yield_streak,marginal_unique_yield=excluded.marginal_unique_yield,last_outcome=excluded.last_outcome,last_error_class=excluded.last_error_class,exhausted_reason=excluded.exhausted_reason,updated_at=excluded.updated_at`, p.JobID, p.Provider, p.OriginalQuery, p.QueryVariant, p.Cursor, p.Page, p.State, p.Attempts, cd, p.LastError, p.ZeroYieldStreak, p.MarginalUniqueYield, p.LastOutcome, p.LastErrorClass, p.ExhaustedReason, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) LoadProgress(ctx context.Context, job string) ([]Progress, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT job_id,provider,original_query,query_variant,cursor,page,state,attempts,COALESCE(cooldown_until,''),last_error,zero_yield_streak,marginal_unique_yield,last_outcome,last_error_class,exhausted_reason FROM provider_progress WHERE job_id=?`, job)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Progress
	for rows.Next() {
		var p Progress
		var cd string
		if err := rows.Scan(&p.JobID, &p.Provider, &p.OriginalQuery, &p.QueryVariant, &p.Cursor, &p.Page, &p.State, &p.Attempts, &cd, &p.LastError, &p.ZeroYieldStreak, &p.MarginalUniqueYield, &p.LastOutcome, &p.LastErrorClass, &p.ExhaustedReason); err != nil {
			return nil, err
		}
		if cd != "" {
			p.CooldownUntil, _ = time.Parse(time.RFC3339Nano, cd)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) LatestResumableJob(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM jobs WHERE status IN ('running','interrupted','RUNNING','INTERRUPTED','PARTIAL') ORDER BY updated_at DESC LIMIT 1`).Scan(&id)
	return id, err
}

func (s *Store) SavePageOutcome(ctx context.Context, p PageOutcome) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO page_outcomes(job_id,provider,query_variant,page,outcome,raw,accepted,rejected,unknown_kept,duplicates,unique_yield,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(job_id,provider,query_variant,page) DO NOTHING`, p.JobID, p.Provider, p.QueryVariant, p.Page, p.Outcome, p.Raw, p.Accepted, p.Rejected, p.UnknownKept, p.Duplicates, p.Unique, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) JobExists(ctx context.Context, id string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE id=?`, id).Scan(&n)
	return n == 1, err
}
func (s *Store) JobOptions(ctx context.Context, id string) (string, error) {
	var options string
	err := s.db.QueryRowContext(ctx, `SELECT options_json FROM jobs WHERE id=?`, id).Scan(&options)
	return options, err
}

func (s *Store) Jobs(ctx context.Context) ([]model.Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT j.id,j.status,j.created_at,j.updated_at,(SELECT count(*) FROM queries q WHERE q.job_id=j.id),(SELECT count(DISTINCT d.url_id) FROM discoveries d WHERE d.job_id=j.id) FROM jobs j ORDER BY j.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Job
	for rows.Next() {
		var j model.Job
		var c, u string
		if err := rows.Scan(&j.ID, &j.Status, &c, &u, &j.Queries, &j.Unique); err != nil {
			return nil, err
		}
		j.CreatedAt, _ = time.Parse(time.RFC3339Nano, c)
		j.UpdatedAt, _ = time.Parse(time.RFC3339Nano, u)
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) Stats(ctx context.Context, job string) (model.Stats, error) {
	var st model.Stats
	st.ByProvider = map[string]int64{}
	st.BySourceEngine = map[string]int64{}
	st.RejectionReasons = map[string]int64{}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*),count(DISTINCT url_id) FROM discoveries WHERE job_id=?`, job).Scan(&st.Raw, &st.Unique); err != nil {
		return st, err
	}
	st.Duplicates = st.Raw - st.Unique
	var raw, accepted, rejected, unknown, dup, malformed int64
	err := s.db.QueryRowContext(ctx, `SELECT raw,accepted,rejected,unknown_kept,duplicates,malformed FROM job_metrics WHERE job_id=?`, job).Scan(&raw, &accepted, &rejected, &unknown, &dup, &malformed)
	if err == nil {
		st.Raw = raw
		st.Accepted, st.Rejected, st.UnknownKept = accepted, rejected, unknown
		if st.Accepted == 0 && st.Rejected == 0 && st.Raw > 0 {
			st.Accepted = st.Raw - st.Malformed
		}
		st.Duplicates = dup
		st.Malformed = malformed
	} else if !errors.Is(err, sql.ErrNoRows) {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM provider_events WHERE job_id=?`, job).Scan(&st.Errors); err != nil {
		return st, err
	}
	frows, err := s.db.QueryContext(ctx, `SELECT provider,kind,count(*),message FROM provider_events WHERE job_id=? GROUP BY provider,kind,message ORDER BY provider,kind,message`, job)
	if err != nil {
		return st, err
	}
	for frows.Next() {
		var f model.ProviderFailure
		if err = frows.Scan(&f.Provider, &f.Kind, &f.Count, &f.Message); err != nil {
			frows.Close()
			return st, err
		}
		st.ProviderFailures = append(st.ProviderFailures, f)
	}
	if err = frows.Close(); err != nil {
		return st, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT provider,count(*) FROM discoveries WHERE job_id=? GROUP BY provider`, job)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		var c int64
		if err := rows.Scan(&n, &c); err != nil {
			return st, err
		}
		st.ByProvider[n] = c
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	erows, err := s.db.QueryContext(ctx, `SELECT source_engines FROM discoveries WHERE job_id=? AND provider='searxng'`, job)
	if err != nil {
		return st, err
	}
	defer erows.Close()
	for erows.Next() {
		var raw string
		if err := erows.Scan(&raw); err != nil {
			return st, err
		}
		var es []string
		if json.Unmarshal([]byte(raw), &es) == nil {
			for _, e := range es {
				st.BySourceEngine[e]++
			}
		}
	}
	if err := erows.Err(); err != nil {
		return st, err
	}
	rrows, err := s.db.QueryContext(ctx, `SELECT reason,count FROM validation_reasons WHERE job_id=?`, job)
	if err != nil {
		return st, err
	}
	for rrows.Next() {
		var n string
		var c int64
		if err = rrows.Scan(&n, &c); err != nil {
			rrows.Close()
			return st, err
		}
		st.RejectionReasons[n] = c
	}
	if err = rrows.Close(); err != nil {
		return st, err
	}
	urows, err := s.db.QueryContext(ctx, `SELECT provider,provider_instance,engine,classification,sum(count),max(last_seen),message FROM upstream_engine_events WHERE job_id=? GROUP BY provider,provider_instance,engine,classification ORDER BY engine,classification`, job)
	if err != nil {
		return st, err
	}
	defer urows.Close()
	for urows.Next() {
		var x model.UpstreamEngineStat
		var seen string
		if err = urows.Scan(&x.Provider, &x.ProviderInstance, &x.Engine, &x.Classification, &x.Count, &seen, &x.LastMessage); err != nil {
			return st, err
		}
		x.LastSeen, _ = time.Parse(time.RFC3339Nano, seen)
		st.UpstreamDegradation = append(st.UpstreamDegradation, x)
	}
	return st, urows.Err()
}

func (s *Store) AddMetrics(ctx context.Context, job string, raw, duplicates, malformed int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO job_metrics(job_id,raw,duplicates,malformed) VALUES(?,?,?,?) ON CONFLICT(job_id) DO UPDATE SET raw=raw+excluded.raw,duplicates=duplicates+excluded.duplicates,malformed=malformed+excluded.malformed`, job, raw, duplicates, malformed)
	return err
}

func (s *Store) AddValidationMetrics(ctx context.Context, job string, accepted, rejected, unknown int64, reasons map[query.ValidationReason]int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO job_metrics(job_id,accepted,rejected,unknown_kept) VALUES(?,?,?,?) ON CONFLICT(job_id) DO UPDATE SET accepted=accepted+excluded.accepted,rejected=rejected+excluded.rejected,unknown_kept=unknown_kept+excluded.unknown_kept`, job, accepted, rejected, unknown); err != nil {
		return err
	}
	for reason, count := range reasons {
		if _, err = tx.ExecContext(ctx, `INSERT INTO validation_reasons(job_id,reason,count) VALUES(?,?,?) ON CONFLICT(job_id,reason) DO UPDATE SET count=count+excluded.count`, job, string(reason), count); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpstreamDiagnostic(ctx context.Context, job string, d provider.UpstreamDiagnostic) error {
	when := d.Timestamp
	if when.IsZero() {
		when = time.Now().UTC()
	}
	ts := when.UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `INSERT INTO upstream_engine_events(job_id,provider,provider_instance,engine,classification,message,count,first_seen,last_seen,last_query,last_page) VALUES(?,?,?,?,?,?,1,?,?,?,?) ON CONFLICT(job_id,provider,provider_instance,engine,classification,message,last_query,last_page) DO UPDATE SET last_seen=excluded.last_seen`, job, d.Provider, d.Instance, d.Engine, string(d.Classification), d.Message, ts, ts, d.Query, d.Page)
	return err
}

func (s *Store) UniqueByOriginalQuery(ctx context.Context, job string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT original_query,count(DISTINCT url_id) FROM discoveries WHERE job_id=? GROUP BY original_query`, job)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var q string
		var n int
		if err := rows.Scan(&q, &n); err != nil {
			return nil, err
		}
		out[q] = n
	}
	return out, rows.Err()
}

func (s *Store) Results(ctx context.Context, job string) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, `SELECT u.normalized_url,u.domain,COALESCE(d.title,''),COALESCE(d.snippet,''),d.provider,d.original_query,d.query_variant,d.rank,d.page,d.discovered_at,d.original_url FROM urls u JOIN discoveries d ON d.url_id=u.id WHERE d.job_id=? AND d.id=(SELECT min(d2.id) FROM discoveries d2 WHERE d2.job_id=d.job_id AND d2.url_id=d.url_id) ORDER BY u.normalized_url`, job)
}

func (s *Store) Variants(ctx context.Context, job string) ([]model.QueryVariant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT q.original,v.variant,v.ordinal FROM query_variants v JOIN queries q ON q.id=v.query_id WHERE q.job_id=? ORDER BY q.id,v.ordinal`, job)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.QueryVariant
	for rows.Next() {
		var v model.QueryVariant
		if err := rows.Scan(&v.Original, &v.Variant, &v.Ordinal); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) Event(ctx context.Context, job, provider, kind, msg string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO provider_events(job_id,provider,kind,message,created_at) VALUES(?,?,?,?,?)`, job, provider, kind, msg, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
