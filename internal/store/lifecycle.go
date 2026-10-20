package store

import (
	"context"
	"errors"
	"time"
)

var ErrJobActive = errors.New("job is already active")

// AcquireJob establishes process ownership before a job enters RUNNING. An
// expired lease, or a legacy RUNNING row without ownership metadata, can be
// claimed explicitly by resume. A live lease can never be stolen.
func (s *Store) AcquireJob(ctx context.Context, id, owner string, now time.Time, ttl time.Duration) error {
	stamp := now.UTC().Format(time.RFC3339Nano)
	until := now.UTC().Add(ttl).Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='RUNNING',run_owner=?,lease_until=?,updated_at=?
		WHERE id=? AND (status<>'RUNNING' OR run_owner='' OR lease_until IS NULL OR lease_until='' OR lease_until<=? OR run_owner=?)`, owner, until, stamp, id, stamp, owner)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrJobActive
	}
	return nil
}

func (s *Store) HeartbeatJob(ctx context.Context, id, owner string, now time.Time, ttl time.Duration) error {
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET lease_until=?,updated_at=? WHERE id=? AND status='RUNNING' AND run_owner=?`, now.UTC().Add(ttl).Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), id, owner)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrJobActive
	}
	return nil
}

func (s *Store) FinishJob(ctx context.Context, id, owner, status string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?,run_owner='',lease_until=NULL,updated_at=? WHERE id=? AND run_owner=?`, status, time.Now().UTC().Format(time.RFC3339Nano), id, owner)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrJobActive
	}
	return nil
}

// RecoverStaleJobs only recovers jobs created by lease-aware Dorker versions.
// Legacy ownerless RUNNING rows are left untouched because their activity
// cannot be determined safely; explicit resume can claim them.
func (s *Store) RecoverStaleJobs(ctx context.Context, now time.Time) (int64, error) {
	stamp := now.UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='INTERRUPTED',run_owner='',lease_until=NULL,updated_at=? WHERE status='RUNNING' AND run_owner<>'' AND lease_until IS NOT NULL AND lease_until<>'' AND lease_until<=?`, stamp, stamp)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetJobStatusIfInactive handles pre-run failures without overriding a live
// lease. Ownerless legacy rows are eligible because an explicit resume is the
// recovery action for those jobs.
func (s *Store) SetJobStatusIfInactive(ctx context.Context, id, status string, now time.Time) error {
	stamp := now.UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?,run_owner='',lease_until=NULL,updated_at=? WHERE id=? AND (status<>'RUNNING' OR run_owner='' OR lease_until IS NULL OR lease_until='' OR lease_until<=?)`, status, stamp, id, stamp)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrJobActive
	}
	return nil
}
