package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/normalize"
	"github.com/HexSilence/dorker/internal/store"
)

const (
	StopFrontierExhausted = "FRONTIER_EXHAUSTED"
	StopSaturated         = "SATURATED"
	StopMaxDepth          = "MAX_DEPTH"
	StopMaxHosts          = "MAX_HOSTS"
	StopMaxQueries        = "MAX_QUERIES"
	StopMaxResults        = "MAX_RESULTS"
	StopMaxDuration       = "MAX_DURATION"
	StopInterrupted       = "INTERRUPTED"
	StopPartial           = "PARTIAL"
	StopFrontierDegraded  = "FRONTIER_EXHAUSTED_DEGRADED"
)

type Options struct {
	MaxDepth                    int           `json:"max_depth"`
	MaxHosts                    int           `json:"max_hosts"`
	MaxQueries                  int           `json:"max_queries"`
	MaxResults                  int           `json:"max_results"`
	MaxResultsPerQuery          int           `json:"max_results_per_query"`
	MaxPages                    int           `json:"max_pages"`
	MaxDuration                 time.Duration `json:"max_duration_nanoseconds"`
	SaturationThreshold         float64       `json:"saturation_threshold"`
	SaturationConsecutiveDepths int           `json:"saturation_consecutive_depths"`
	MinSaturationDepths         int           `json:"minimum_saturation_depths"`
}

func DefaultOptions() Options {
	return Options{MaxDepth: 3, MaxHosts: 5000, MaxQueries: 10000, MaxResults: 100000, MaxResultsPerQuery: 100, MaxPages: 10, MaxDuration: 30 * time.Minute, SaturationThreshold: .03, SaturationConsecutiveDepths: 2, MinSaturationDepths: 2}
}

func (o Options) Validate() error {
	if o.MaxDepth < 0 || o.MaxHosts < 1 || o.MaxQueries < 1 || o.MaxResults < 1 || o.MaxResultsPerQuery < 1 || o.MaxPages < 1 || o.MaxDuration <= 0 {
		return errors.New("discovery limits must be positive (max-depth may be zero)")
	}
	if o.SaturationThreshold < 0 || o.SaturationThreshold > 1 {
		return errors.New("saturation threshold must be between 0 and 1")
	}
	if o.SaturationConsecutiveDepths < 1 || o.MinSaturationDepths < 1 {
		return errors.New("saturation depth counts must be positive")
	}
	return nil
}

type RunReport struct {
	Partial, Degraded bool
	// RecoveryProven is reserved for runners that can prove all coverage gaps
	// from earlier depths were re-executed successfully. A later clean response
	// alone is not recovery evidence.
	RecoveryProven       bool
	ProviderFailures     int
	Rejected, Duplicates int
}
type Runner interface {
	Run(context.Context, []model.QueryVariant) (RunReport, error)
}
type RunnerFunc func(context.Context, []model.QueryVariant) (RunReport, error)

func (f RunnerFunc) Run(c context.Context, v []model.QueryVariant) (RunReport, error) { return f(c, v) }

type Result struct {
	JobID, Status, StopReason    string
	Depth, Hosts, Roots, Queries int
	Stats                        []store.DiscoveryDepthStat
}
type Engine struct {
	Store   *store.Store
	Runner  Runner
	Now     func() time.Time
	OnDepth func(store.DiscoveryDepthStat)
}

func (e *Engine) Run(ctx context.Context, job string) (Result, error) {
	dj, err := e.Store.DiscoveryJob(ctx, job)
	if err != nil {
		return Result{}, err
	}
	var opts Options
	if err = decodeOptions(dj.OptionsJSON, &opts); err != nil {
		return Result{}, err
	}
	if err = opts.Validate(); err != nil {
		return Result{}, err
	}
	if e.Now == nil {
		e.Now = time.Now
	}
	if err = e.Store.RecoverDiscoveryFrontier(ctx, job); err != nil {
		return Result{}, err
	}
	stats, err := e.Store.DiscoveryDepths(ctx, job)
	if err != nil {
		return Result{}, err
	}
	queriesDone := 0
	unresolvedDegradation := false
	for _, s := range stats {
		queriesDone += s.QueriesExecuted
		if !s.Trustworthy {
			unresolvedDegradation = true
		}
	}
	depth := 0
	if len(stats) > 0 {
		depth = stats[len(stats)-1].Depth + 1
	}
	deadline := e.Now().Add(opts.MaxDuration)
	if !dj.StartedAt.IsZero() {
		deadline = dj.StartedAt.Add(opts.MaxDuration)
	}
	for {
		if err = ctx.Err(); err != nil {
			return e.finish(context.Background(), job, depth, queriesDone, StopInterrupted, "INTERRUPTED", err)
		}
		if !e.Now().Before(deadline) {
			return e.finish(ctx, job, depth, queriesDone, StopMaxDuration, "PARTIAL", nil)
		}
		var variants []model.QueryVariant
		parents := map[string]string{}
		var expanding []string
		if depth == 0 {
			variants = []model.QueryVariant{{Original: dj.SeedQuery, Variant: dj.SeedQuery}}
		} else {
			frontier, er := e.Store.DiscoveryFrontier(ctx, job, depth-1, 0)
			if er != nil {
				return Result{}, er
			}
			if len(frontier) == 0 {
				return e.finish(ctx, job, depth-1, queriesDone, StopFrontierExhausted, "COMPLETE", nil)
			}
			remaining := opts.MaxQueries - queriesDone
			if remaining <= 0 {
				return e.finish(ctx, job, depth-1, queriesDone, StopMaxQueries, "COMPLETE", nil)
			}
			if len(frontier) > remaining {
				frontier = frontier[:remaining]
			}
			for _, h := range frontier {
				q := "site:" + h.Host
				variants = append(variants, model.QueryVariant{Original: q, Variant: q})
				parents[q] = h.Host
				expanding = append(expanding, h.Host)
			}
			if er = e.Store.SetDiscoveryHostsState(ctx, job, expanding, "EXPANDING", true); er != nil {
				return Result{}, er
			}
		}
		beforeHosts, beforeRoots, er := e.Store.DiscoveryCounts(ctx, job)
		if er != nil {
			return Result{}, er
		}
		beforeJobStats, _ := e.Store.Stats(ctx, job)
		runCtx, cancelRun := context.WithTimeout(ctx, deadline.Sub(e.Now()))
		runReport, runErr := e.Runner.Run(runCtx, variants)
		deadlineHit := errors.Is(runErr, context.DeadlineExceeded) || runCtx.Err() == context.DeadlineExceeded
		cancelRun()
		queriesDone += len(variants)
		if runErr != nil {
			if len(expanding) > 0 {
				_ = e.Store.SetDiscoveryHostsState(context.Background(), job, expanding, "FAILED_RETRYABLE", false)
			}
			if deadlineHit {
				return e.finish(context.Background(), job, depth, queriesDone, StopMaxDuration, "PARTIAL", nil)
			}
			if errors.Is(runErr, context.Canceled) {
				return e.finish(context.Background(), job, depth, queriesDone, StopInterrupted, "INTERRUPTED", runErr)
			}
			return Result{}, runErr
		}
		seenURLs := map[string]struct{}{}
		observed := map[string]struct{}{}
		roots := map[string]struct{}{}
		newHosts := 0
		for _, v := range variants {
			rs, er := e.Store.ResultsForOriginalQuery(ctx, job, v.Original)
			if er != nil {
				return Result{}, er
			}
			for _, r := range rs {
				seenURLs[r.NormalizedURL] = struct{}{}
				h, er := normalize.ClassifyHost(r.Domain)
				if er != nil {
					continue
				}
				observed[h.Name] = struct{}{}
				if h.Registrable != "" {
					roots[h.Registrable] = struct{}{}
				}
				if beforeHosts+newHosts >= opts.MaxHosts {
					continue
				}
				fresh, er := e.Store.UpsertDiscoveryHost(ctx, job, store.DiscoveryHost{Host: h.Name, RegistrableDomain: h.Registrable, Depth: depth, ParentHost: parents[v.Original], State: "QUEUED", SeedDerived: depth == 0, FirstSeen: r.DiscoveredAt, LastSeen: r.DiscoveredAt})
				if er != nil {
					return Result{}, er
				}
				if fresh {
					newHosts++
				}
				sources := r.SourceEngines
				if len(sources) == 0 {
					sources = []string{""}
				}
				for _, source := range sources {
					if er = e.Store.AddDiscoveryEdge(ctx, job, store.DiscoveryEdge{ParentHost: parents[v.Original], ChildHost: h.Name, Query: v.Original, Provider: r.Provider, ProviderInstance: r.ProviderInstance, SourceEngine: source, FirstSeen: r.DiscoveredAt}); er != nil {
						return Result{}, er
					}
				}
			}
		}
		if len(expanding) > 0 {
			if er = e.Store.SetDiscoveryHostsState(ctx, job, expanding, "EXPANDED", false); er != nil {
				return Result{}, er
			}
		}
		afterHosts, afterRoots, er := e.Store.DiscoveryCounts(ctx, job)
		if er != nil {
			return Result{}, er
		}
		gain := 0.0
		if beforeHosts > 0 {
			gain = float64(newHosts) / float64(beforeHosts)
		} else if newHosts > 0 {
			gain = 1
		}
		currentCoverageClean := !runReport.Partial && !runReport.Degraded
		if currentCoverageClean && runReport.RecoveryProven {
			unresolvedDegradation = false
		}
		if !currentCoverageClean {
			unresolvedDegradation = true
		}
		trust := currentCoverageClean && !unresolvedDegradation
		afterJobStats, _ := e.Store.Stats(ctx, job)
		st := store.DiscoveryDepthStat{Depth: depth, AcceptedURLs: len(seenURLs), NewURLs: int(afterJobStats.Unique - beforeJobStats.Unique), HostsObserved: len(observed), NewHosts: newHosts, RootsObserved: len(roots), NewRoots: afterRoots - beforeRoots, QueriesExecuted: len(variants), ProviderFailures: runReport.ProviderFailures, RejectedResults: runReport.Rejected, DuplicateResults: runReport.Duplicates, GainRatio: gain, Trustworthy: trust}
		if er = e.Store.SaveDiscoveryDepth(ctx, job, st); er != nil {
			return Result{}, er
		}
		if e.OnDepth != nil {
			e.OnDepth(st)
		}
		stats = append(stats, st)
		streak := dj.LowGainStreak
		if trust && depth > 0 && gain < opts.SaturationThreshold {
			streak++
		} else if trust {
			streak = 0
		}
		dj.LowGainStreak = streak
		if er = e.Store.UpdateDiscoveryJob(ctx, job, depth, streak, "", false); er != nil {
			return Result{}, er
		}
		if afterHosts >= opts.MaxHosts {
			return e.finish(ctx, job, depth, queriesDone, StopMaxHosts, "COMPLETE", nil)
		}
		jobStats, er := e.Store.Stats(ctx, job)
		if er != nil {
			return Result{}, er
		}
		if int(jobStats.Unique) >= opts.MaxResults {
			return e.finish(ctx, job, depth, queriesDone, StopMaxResults, "COMPLETE", nil)
		}
		if trust && depth > 0 && newHosts == 0 {
			return e.finish(ctx, job, depth, queriesDone, StopSaturated, "SATURATED", nil)
		}
		if trust && depth >= opts.MinSaturationDepths && streak >= opts.SaturationConsecutiveDepths {
			return e.finish(ctx, job, depth, queriesDone, StopSaturated, "SATURATED", nil)
		}
		if depth >= opts.MaxDepth {
			return e.finish(ctx, job, depth, queriesDone, StopMaxDepth, "COMPLETE", nil)
		}
		if queriesDone >= opts.MaxQueries {
			return e.finish(ctx, job, depth, queriesDone, StopMaxQueries, "COMPLETE", nil)
		}
		if !trust && newHosts == 0 {
			return e.finish(ctx, job, depth, queriesDone, StopFrontierDegraded, "PARTIAL", nil)
		}
		depth++
	}
}

func (e *Engine) finish(ctx context.Context, job string, depth, queries int, reason, status string, cause error) (Result, error) {
	dj, _ := e.Store.DiscoveryJob(ctx, job)
	hosts, roots, _ := e.Store.DiscoveryCounts(ctx, job)
	stats, _ := e.Store.DiscoveryDepths(ctx, job)
	historicalDegradation := false
	for _, s := range stats {
		historicalDegradation = historicalDegradation || !s.Trustworthy
	}
	lastTrustworthy := len(stats) > 0 && stats[len(stats)-1].Trustworthy
	if reason == StopSaturated {
		if !lastTrustworthy {
			status, reason = "PARTIAL", StopFrontierDegraded
		} else {
			status = "SATURATED"
		}
	} else if historicalDegradation && (status == "COMPLETE" || status == "SATURATED") {
		status = "PARTIAL"
		if reason == StopFrontierExhausted {
			reason = StopFrontierDegraded
		}
	}
	_ = e.Store.UpdateDiscoveryJob(ctx, job, depth, dj.LowGainStreak, reason, status != "INTERRUPTED")
	return Result{JobID: job, Status: status, StopReason: reason, Depth: depth, Hosts: hosts, Roots: roots, Queries: queries, Stats: stats}, cause
}

// Encoding is kept here to make stored options stable and resumeable without
// coupling the engine to CLI flag structs.
func EncodeOptions(o Options) string { b, _ := json.Marshal(o); return string(b) }
func decodeOptions(s string, o *Options) error {
	*o = DefaultOptions()
	return json.Unmarshal([]byte(s), o)
}
