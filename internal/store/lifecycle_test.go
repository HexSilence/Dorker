package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestJobLeaseDoesNotStealActiveJobAndRecoversStaleJob(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	if err = s.CreateJob(ctx, "job", "{}"); err != nil {
		t.Fatal(err)
	}
	if err = s.AcquireJob(ctx, "job", "owner-one", now, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = s.AcquireJob(ctx, "job", "owner-two", now.Add(30*time.Second), time.Minute); !errors.Is(err, ErrJobActive) {
		t.Fatalf("active lease: %v", err)
	}
	if n, er := s.RecoverStaleJobs(ctx, now.Add(30*time.Second)); er != nil || n != 0 {
		t.Fatalf("early recovery n=%d err=%v", n, er)
	}
	if n, er := s.RecoverStaleJobs(ctx, now.Add(2*time.Minute)); er != nil || n != 1 {
		t.Fatalf("stale recovery n=%d err=%v", n, er)
	}
	if err = s.AcquireJob(ctx, "job", "owner-two", now.Add(2*time.Minute), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishJob(ctx, "job", "owner-two", "COMPLETE"); err != nil {
		t.Fatal(err)
	}
	var status, owner string
	var lease any
	if err = s.db.QueryRow(`SELECT status,run_owner,lease_until FROM jobs WHERE id='job'`).Scan(&status, &owner, &lease); err != nil {
		t.Fatal(err)
	}
	if status != "COMPLETE" || owner != "" || lease != nil {
		t.Fatalf("status=%q owner=%q lease=%v", status, owner, lease)
	}
}

func TestLegacyOwnerlessRunningJobIsOnlyRecoveredByExplicitAcquire(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.CreateJob(ctx, "legacy", "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE jobs SET status='RUNNING' WHERE id='legacy'`); err != nil {
		t.Fatal(err)
	}
	if n, er := s.RecoverStaleJobs(ctx, time.Now().Add(24*time.Hour)); er != nil || n != 0 {
		t.Fatalf("recovered ambiguous legacy job n=%d err=%v", n, er)
	}
	if err = s.AcquireJob(ctx, "legacy", "resume-owner", time.Now(), time.Minute); err != nil {
		t.Fatal(err)
	}
}

func TestIndependentJobsHaveIndependentLeases(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"one", "two"} {
		if err = s.CreateJob(context.Background(), id, "{}"); err != nil {
			t.Fatal(err)
		}
		if err = s.AcquireJob(context.Background(), id, "owner-"+id, time.Now(), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiscoveryOutputPathMigrationAndPersistence(t *testing.T) {
	db := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`CREATE TABLE jobs(id TEXT PRIMARY KEY,status TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,options_json TEXT NOT NULL DEFAULT '{}'); CREATE TABLE discovery_jobs(job_id TEXT PRIMARY KEY REFERENCES jobs(id),seed_query TEXT NOT NULL,options_json TEXT NOT NULL,current_depth INTEGER NOT NULL DEFAULT 0,low_gain_streak INTEGER NOT NULL DEFAULT 0,stop_reason TEXT NOT NULL DEFAULT '',started_at TEXT NOT NULL,finished_at TEXT);`)
	if err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CreateJob(context.Background(), "j", "{}"); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateDiscoveryJob(context.Background(), DiscoveryJob{JobID: "j", SeedQuery: "site:.ye", OptionsJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetDiscoveryOutputPath(context.Background(), "j", "/portable/test"); err != nil {
		t.Fatal(err)
	}
	dj, err := s.DiscoveryJob(context.Background(), "j")
	if err != nil || dj.OutputPath != "/portable/test" {
		t.Fatalf("job=%+v err=%v", dj, err)
	}
}
