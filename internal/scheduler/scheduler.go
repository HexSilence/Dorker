package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HexSilence/dorker/internal/collector"
	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/provider"
	rl "github.com/HexSilence/dorker/internal/ratelimit"
	"github.com/HexSilence/dorker/internal/store"
	"golang.org/x/time/rate"
)

type Options struct {
	Workers, MaxPages, MaxResults, MaxResultsPerQuery, PageSize int
	Retry                                                       rl.RetryPolicy
	SaturationEnabled                                           bool
	ConsecutiveZeroYieldPages                                   int
	WaitForRecovery                                             bool
	MaxRecoveryWait                                             time.Duration
}
type Runtime struct {
	Provider provider.Provider
	Limiter  *rate.Limiter
}
type Scheduler struct {
	Store     *store.Store
	Collector *collector.Collector
	Log       *slog.Logger
	Options   Options
	total     atomic.Int64
	collectMu sync.Mutex
	queryMu   sync.Mutex
	perQuery  map[string]int
	reportMu  sync.Mutex
	report    Report
}
type PageOutcome string

const (
	UsefulPage    PageOutcome = "USEFUL_PAGE"
	CleanEmpty    PageOutcome = "CLEAN_EMPTY"
	DegradedEmpty PageOutcome = "DEGRADED_EMPTY"
	DuplicateOnly PageOutcome = "DUPLICATE_ONLY"
	RejectedOnly  PageOutcome = "REJECTED_ONLY"
	ErrorPage     PageOutcome = "ERROR"
)

type Report struct {
	UsefulPages, CleanEmptyPages, DegradedEmptyPages, DuplicateOnlyPages, RejectedOnlyPages, ErrorPages int
	ProviderFailures                                                                                    int
	Saturated, Degraded, Partial, DegradedEarlyStop                                                     bool
	SaturationReason                                                                                    string
}
type task struct {
	rt Runtime
	v  model.QueryVariant
	p  store.Progress
}

func New(st *store.Store, c *collector.Collector, l *slog.Logger, o Options) *Scheduler {
	if o.ConsecutiveZeroYieldPages < 1 {
		o.ConsecutiveZeroYieldPages = 2
	}
	return &Scheduler{Store: st, Collector: c, Log: l, Options: o, perQuery: map[string]int{}}
}
func (s *Scheduler) Report() Report { s.reportMu.Lock(); defer s.reportMu.Unlock(); return s.report }
func (s *Scheduler) record(outcome PageOutcome) {
	s.reportMu.Lock()
	defer s.reportMu.Unlock()
	switch outcome {
	case UsefulPage:
		s.report.UsefulPages++
	case CleanEmpty:
		s.report.CleanEmptyPages++
	case DegradedEmpty:
		s.report.DegradedEmptyPages++
		s.report.Degraded = true
		s.report.Partial = true
	case DuplicateOnly:
		s.report.DuplicateOnlyPages++
	case RejectedOnly:
		s.report.RejectedOnlyPages++
	case ErrorPage:
		s.report.ErrorPages++
		s.report.Degraded = true
	}
}
func (s *Scheduler) setStopReason(reason string) {
	s.reportMu.Lock()
	s.report.SaturationReason = reason
	s.reportMu.Unlock()
}
func ClassifyPage(resp provider.SearchResponse, marginalUnique, accepted int, err error) PageOutcome {
	if err != nil {
		return ErrorPage
	}
	if marginalUnique > 0 {
		return UsefulPage
	}
	if accepted > 0 {
		return DuplicateOnly
	}
	// Provider noise rejected by local scope enforcement is not a duplicate.
	// Keep paginating within the configured hard limit: a later page can still
	// contain valid in-scope results.
	if len(resp.Results) > 0 {
		return RejectedOnly
	}
	if len(resp.Diagnostics) > 0 || len(resp.PartialErrors) > 0 {
		return DegradedEmpty
	}
	return CleanEmpty
}
func (s *Scheduler) Run(ctx context.Context, job string, variants []model.QueryVariant, runtimes []Runtime, resume []store.Progress) error {
	if s.Options.Workers < 1 {
		return errors.New("workers must be positive")
	}
	saved := map[string]store.Progress{}
	for _, p := range resume {
		saved[p.Provider+"\x00"+p.QueryVariant] = p
	}
	if counts, err := s.Store.UniqueByOriginalQuery(ctx, job); err == nil {
		s.queryMu.Lock()
		for q, n := range counts {
			s.perQuery[q] = n
		}
		s.queryMu.Unlock()
		if stats, statsErr := s.Store.Stats(ctx, job); statsErr == nil {
			s.total.Store(stats.Unique)
		} else {
			return statsErr
		}
	} else {
		return err
	}
	tasks := make(chan task, s.Options.Workers)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	worker := func() {
		defer wg.Done()
		for t := range tasks {
			if err := s.runTask(ctx, job, t); err != nil && !errors.Is(err, context.Canceled) {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
			}
		}
	}
	for i := 0; i < s.Options.Workers; i++ {
		wg.Add(1)
		go worker()
	}
enqueue:
	for _, v := range variants {
		for _, rt := range runtimes {
			select {
			case <-ctx.Done():
				break enqueue
			default:
			}
			key := rt.Provider.Name() + "\x00" + v.Variant
			p, ok := saved[key]
			if ok && (p.State == "COMPLETE" || p.State == "SATURATED" || p.State == "DEGRADED_COMPLETE") {
				continue
			}
			if !ok {
				p = store.Progress{JobID: job, Provider: rt.Provider.Name(), OriginalQuery: v.Original, QueryVariant: v.Variant, State: "READY"}
				if err := s.Store.SaveProgress(ctx, p); err != nil {
					firstErr = err
					break enqueue
				}
			}
			select {
			case tasks <- task{rt: rt, v: v, p: p}:
			case <-ctx.Done():
				break enqueue
			}
		}
	}
	close(tasks)
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return firstErr
}
func (s *Scheduler) runTask(ctx context.Context, job string, t task) error {
	p := t.p
	if !p.CooldownUntil.IsZero() && time.Now().Before(p.CooldownUntil) {
		if !s.Options.WaitForRecovery || time.Until(p.CooldownUntil) > s.Options.MaxRecoveryWait {
			s.reportMu.Lock()
			s.report.Partial = true
			s.report.ProviderFailures++
			s.reportMu.Unlock()
			return nil
		}
		if err := sleep(ctx, time.Until(p.CooldownUntil)); err != nil {
			return err
		}
	}
	waited := time.Duration(0)
	for {
		if s.Options.MaxPages > 0 && p.Page >= s.Options.MaxPages {
			p.State = "COMPLETE"
			p.ExhaustedReason = "maximum page limit reached"
			s.setStopReason(p.ExhaustedReason)
			return s.Store.SaveProgress(ctx, p)
		}
		if s.limitReached(t.v.Original) {
			p.State = "COMPLETE"
			p.ExhaustedReason = "result limit reached"
			s.setStopReason(p.ExhaustedReason)
			return s.Store.SaveProgress(ctx, p)
		}
		if err := t.rt.Limiter.Wait(ctx); err != nil {
			return err
		}
		req := provider.SearchRequest{Query: t.v.Variant, OriginalQuery: t.v.Original, Cursor: p.Cursor, Page: p.Page, PageSize: s.Options.PageSize}
		started := time.Now()
		requestAttrs := []any{"provider", p.Provider, "logical_page", p.Page, "cursor", p.Cursor, "requested_page_size", req.PageSize}
		var pagination provider.PaginationInfo
		hasPagination := false
		if describer, ok := t.rt.Provider.(provider.PaginationDescriber); ok {
			pagination, hasPagination = describer.Pagination(req), true
			requestAttrs = append(requestAttrs, "upstream_page", pagination.UpstreamPage, "page_size_applied", pagination.PageSizeApplied)
		}
		s.Log.Debug("provider request", requestAttrs...)
		resp, err := t.rt.Provider.Search(ctx, req)
		if err != nil {
			s.record(ErrorPage)
			status := responseStatus(err)
			responseAttrs := []any{"provider", p.Provider, "duration", time.Since(started), "http_status", status, "response_received", status != 0, "logical_page", p.Page, "cursor", p.Cursor, "results", 0, "retry_classification", provider.KindOf(err), "error", err.Error()}
			if hasPagination {
				responseAttrs = append(responseAttrs, "upstream_page", pagination.UpstreamPage)
			}
			s.Log.Debug("provider response", responseAttrs...)
			p.Attempts++
			p.LastError = err.Error()
			kind := provider.KindOf(err)
			p.LastErrorClass = string(kind)
			p.LastOutcome = string(ErrorPage)
			if kind == provider.KindQuota || kind == provider.KindUnauthorized || kind == provider.KindInvalidQuery {
				p.State = stringsForKind(kind)
				s.reportMu.Lock()
				s.report.Partial = true
				s.report.ProviderFailures++
				s.reportMu.Unlock()
				_ = s.Store.Event(ctx, job, p.Provider, string(kind), err.Error())
				return s.Store.SaveProgress(ctx, p)
			}
			if !provider.Retryable(err) || p.Attempts >= s.Options.Retry.MaxAttempts {
				p.State = "FAILED"
				s.reportMu.Lock()
				s.report.Partial = true
				s.report.ProviderFailures++
				s.reportMu.Unlock()
				eventKind := string(kind)
				if eventKind == "" {
					eventKind = "failure"
				}
				_ = s.Store.Event(ctx, job, p.Provider, eventKind, err.Error())
				if e := s.Store.SaveProgress(ctx, p); e != nil {
					return e
				}
				return nil
			}
			delay := s.Options.Retry.Delay(p.Attempts-1, provider.RetryDelay(err))
			p.State = "COOLDOWN"
			p.CooldownUntil = time.Now().Add(delay)
			_ = s.Store.Event(ctx, job, p.Provider, string(kind), err.Error())
			if e := s.Store.SaveProgress(ctx, p); e != nil {
				return e
			}
			s.Log.Warn("provider cooldown", "provider", p.Provider, "query", p.QueryVariant, "page", p.Page, "attempt", p.Attempts, "duration", delay)
			if !s.Options.WaitForRecovery || waited+delay > s.Options.MaxRecoveryWait {
				s.reportMu.Lock()
				s.report.Partial = true
				s.report.ProviderFailures++
				s.reportMu.Unlock()
				return nil
			}
			if e := sleep(ctx, delay); e != nil {
				return e
			}
			waited += delay
			p.State = "READY"
			continue
		}
		responseAttrs := []any{"provider", p.Provider, "duration", resp.Duration, "http_status", resp.HTTPStatus, "logical_page", p.Page, "cursor", p.Cursor, "results", len(resp.Results), "retry_classification", "none"}
		if hasPagination {
			responseAttrs = append(responseAttrs, "upstream_page", pagination.UpstreamPage)
		}
		s.Log.Debug("provider response", responseAttrs...)
		for _, partial := range resp.PartialErrors {
			s.reportMu.Lock()
			s.report.Degraded = true
			s.reportMu.Unlock()
			_ = s.Store.Event(ctx, job, p.Provider, "partial_failure", partial.Error())
			s.Log.Warn("provider instance failed; another continued", "provider", p.Provider, "error", partial)
		}
		for _, diagnostic := range resp.Diagnostics {
			s.reportMu.Lock()
			s.report.Degraded = true
			s.reportMu.Unlock()
			if e := s.Store.UpstreamDiagnostic(ctx, job, diagnostic); e != nil {
				return e
			}
		}
		p.Attempts = 0
		p.LastError = ""
		p.LastErrorClass = ""
		// Query provenance is scheduler-owned. Providers supply result content but
		// cannot broaden local scope by omitting or replacing the query metadata.
		for i := range resp.Results {
			resp.Results[i].OriginalQuery = req.OriginalQuery
			resp.Results[i].QueryVariant = req.Query
			if resp.Results[i].Provider == "" {
				resp.Results[i].Provider = p.Provider
			}
		}
		s.collectMu.Lock()
		rawBefore, acceptedBefore := s.Collector.Raw.Load(), s.Collector.Accepted.Load()
		rejectedBefore, unknownBefore := s.Collector.Rejected.Load(), s.Collector.UnknownKept.Load()
		duplicatesBefore, malformedBefore := s.Collector.Duplicates.Load(), s.Collector.Malformed.Load()
		batch := resp.Results
		if remaining := s.remaining(t.v.Original); remaining >= 0 && len(batch) > remaining {
			batch = batch[:remaining]
		}
		fresh, e := s.Collector.AddBatch(ctx, batch)
		if e != nil {
			s.collectMu.Unlock()
			return fmt.Errorf("collect %s: %w", p.Provider, e)
		}
		if fresh > 0 {
			s.total.Add(int64(fresh))
			s.queryMu.Lock()
			s.perQuery[t.v.Original] += fresh
			s.queryMu.Unlock()
		}
		rawDelta := int(s.Collector.Raw.Load() - rawBefore)
		acceptedDelta := int(s.Collector.Accepted.Load() - acceptedBefore)
		rejectedDelta := int(s.Collector.Rejected.Load() - rejectedBefore)
		unknownDelta := int(s.Collector.UnknownKept.Load() - unknownBefore)
		duplicateDelta := int(s.Collector.Duplicates.Load() - duplicatesBefore)
		malformedDelta := int(s.Collector.Malformed.Load() - malformedBefore)
		s.collectMu.Unlock()
		outcome := ClassifyPage(resp, fresh, acceptedDelta, nil)
		s.record(outcome)
		p.LastOutcome = string(outcome)
		p.MarginalUniqueYield = fresh
		if outcome == UsefulPage {
			p.ZeroYieldStreak = 0
		} else if outcome == CleanEmpty || outcome == DuplicateOnly {
			p.ZeroYieldStreak++
		}
		if e := s.Store.SavePageOutcome(ctx, store.PageOutcome{JobID: job, Provider: p.Provider, QueryVariant: p.QueryVariant, Page: p.Page, Outcome: string(outcome), Raw: rawDelta, Accepted: acceptedDelta, Rejected: rejectedDelta, UnknownKept: unknownDelta, Duplicates: duplicateDelta, Unique: fresh}); e != nil {
			return e
		}
		p.Page++
		p.Cursor = resp.NextCursor
		p.State = "READY"
		p.CooldownUntil = time.Time{}
		if outcome == DegradedEmpty {
			p.Page-- // retry this inconclusive page on resume.
			p.State = "DEGRADED"
			s.reportMu.Lock()
			s.report.Partial = true
			s.reportMu.Unlock()
		} else if resp.Done || outcome == CleanEmpty {
			p.State = "COMPLETE"
			p.ExhaustedReason = "provider exhausted"
		} else if s.limitReached(t.v.Original) {
			p.State = "COMPLETE"
			p.ExhaustedReason = "result limit reached"
			s.setStopReason(p.ExhaustedReason)
		} else if capable, ok := t.rt.Provider.(provider.Capable); ok && (!capable.Capabilities().SupportsPagination || (capable.Capabilities().MaximumAccessibleResults > 0 && p.Page*s.Options.PageSize >= capable.Capabilities().MaximumAccessibleResults)) {
			p.State = "COMPLETE"
			p.ExhaustedReason = "provider pagination ceiling reached"
		} else if s.Options.SaturationEnabled {
			s.reportMu.Lock()
			degraded := s.report.Degraded
			threshold := s.Options.ConsecutiveZeroYieldPages
			if degraded {
				// Incomplete upstream coverage lowers confidence in a zero-yield
				// streak. Require twice the normal evidence, while retaining hard
				// page/result limits so degraded searches remain bounded.
				threshold *= 2
			}
			if p.ZeroYieldStreak >= threshold {
				if degraded {
					p.State = "DEGRADED_COMPLETE"
					p.ExhaustedReason = "low-yield threshold reached under degraded upstream coverage"
					s.report.Partial = true
					s.report.DegradedEarlyStop = true
				} else {
					p.State = "SATURATED"
					p.ExhaustedReason = "duplicate/zero-yield threshold reached"
					s.report.Saturated = true
				}
				s.report.SaturationReason = p.ExhaustedReason
			}
			s.reportMu.Unlock()
		}
		degraded := s.Report().Degraded
		pageAttrs := []any{"provider", p.Provider, "logical_page", req.Page}
		if hasPagination {
			pageAttrs = append(pageAttrs, "upstream_page", pagination.UpstreamPage)
		}
		pageAttrs = append(pageAttrs, "raw", rawDelta, "normalized", rawDelta-malformedDelta, "accepted", acceptedDelta, "rejected", rejectedDelta, "malformed", malformedDelta, "duplicates", duplicateDelta, "new_unique", fresh, "outcome", outcome, "zero_yield_streak", p.ZeroYieldStreak, "provider_done", resp.Done, "coverage", map[bool]string{true: "degraded", false: "trustworthy"}[degraded], "degraded", degraded, "state", p.State)
		s.Log.Debug("provider page processed", pageAttrs...)
		if e := s.Store.SaveProgress(ctx, p); e != nil {
			return e
		}
		if p.State == "COMPLETE" || p.State == "SATURATED" || p.State == "DEGRADED" || p.State == "DEGRADED_COMPLETE" {
			return nil
		}
	}
}

func (s *Scheduler) remaining(q string) int {
	remaining := -1
	if s.Options.MaxResults > 0 {
		remaining = s.Options.MaxResults - int(s.total.Load())
	}
	s.queryMu.Lock()
	if s.Options.MaxResultsPerQuery > 0 {
		perQuery := s.Options.MaxResultsPerQuery - s.perQuery[q]
		if remaining < 0 || perQuery < remaining {
			remaining = perQuery
		}
	}
	s.queryMu.Unlock()
	return remaining
}

func responseStatus(err error) int {
	var pe *provider.Error
	if errors.As(err, &pe) {
		return pe.HTTPStatus
	}
	return 0
}
func (s *Scheduler) limitReached(q string) bool {
	if s.Options.MaxResults > 0 && s.total.Load() >= int64(s.Options.MaxResults) {
		return true
	}
	s.queryMu.Lock()
	defer s.queryMu.Unlock()
	return s.Options.MaxResultsPerQuery > 0 && s.perQuery[q] >= s.Options.MaxResultsPerQuery
}
func sleep(ctx context.Context, d time.Duration) error {
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tm.C:
		return nil
	}
}
func stringsForKind(k provider.ErrorKind) string {
	switch k {
	case provider.KindQuota:
		return "QUOTA_EXHAUSTED"
	case provider.KindUnauthorized:
		return "DISABLED"
	default:
		return "FAILED"
	}
}
func NewRuntime(p provider.Provider, rps float64, burst int) Runtime {
	if rps <= 0 {
		rps = 1000000
	}
	if burst < 1 {
		burst = 1
	}
	return Runtime{Provider: p, Limiter: rate.NewLimiter(rate.Limit(rps), burst)}
}

var _ = strconv.Itoa
