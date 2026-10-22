package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/HexSilence/dorker/internal/collector"
	"github.com/HexSilence/dorker/internal/config"
	"github.com/HexSilence/dorker/internal/discovery"
	exporter "github.com/HexSilence/dorker/internal/export"
	"github.com/HexSilence/dorker/internal/httpclient"
	"github.com/HexSilence/dorker/internal/logging"
	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/normalize"
	"github.com/HexSilence/dorker/internal/query"
	rl "github.com/HexSilence/dorker/internal/ratelimit"
	"github.com/HexSilence/dorker/internal/scheduler"
	"github.com/HexSilence/dorker/internal/store"
	"github.com/HexSilence/dorker/internal/ui"
)

type discoverOptions struct {
	Query, Config, DB, Output, OutputDir, Validation, LogLevel                           string
	Providers                                                                            multiFlag
	MaxDepth, MaxHosts, MaxQueries, MaxResults, MaxPerQuery, MaxPages, PageSize, Workers int
	MaxDuration                                                                          time.Duration
	SaturationThreshold                                                                  float64
	SaturationDepths                                                                     int
	HostsOnly, RootsOnly, DryRun, Quiet, NoBanner                                        bool
}

func discoverFlags(errw io.Writer) (*flag.FlagSet, *discoverOptions) {
	o := &discoverOptions{}
	fs := flag.NewFlagSet("discover", flag.ContinueOnError)
	fs.SetOutput(errw)
	fs.Usage = func() { ui.CommandHelp(errw, "discover", config.DefaultPath()) }
	fs.StringVar(&o.Query, "q", "", "seed query")
	fs.Var(&o.Providers, "provider", "provider name (repeatable)")
	fs.StringVar(&o.Config, "config", config.DefaultPath(), "YAML configuration path")
	fs.StringVar(&o.DB, "db", "", "SQLite database path override")
	fs.IntVar(&o.MaxDepth, "max-depth", -1, "maximum recursive expansion depth")
	fs.IntVar(&o.MaxHosts, "max-hosts", 0, "maximum discovered hosts")
	fs.IntVar(&o.MaxQueries, "max-queries", 0, "maximum provider queries")
	fs.IntVar(&o.MaxResults, "max-results", 100000, "maximum unique URLs")
	fs.IntVar(&o.MaxPerQuery, "max-results-per-query", 100, "maximum unique URLs per expansion query")
	fs.IntVar(&o.MaxPages, "max-pages", 10, "maximum pages per provider/query")
	fs.IntVar(&o.PageSize, "page-size", 10, "requested page size")
	fs.IntVar(&o.Workers, "workers", 0, "bounded worker count")
	fs.DurationVar(&o.MaxDuration, "max-duration", 0, "maximum elapsed discovery time")
	fs.Float64Var(&o.SaturationThreshold, "saturation-threshold", -1, "low host-gain ratio")
	fs.IntVar(&o.SaturationDepths, "saturation-consecutive-depths", 0, "consecutive trustworthy low-gain depths")
	fs.BoolVar(&o.HostsOnly, "hosts-only", false, "emit hosts instead of URLs")
	fs.BoolVar(&o.RootsOnly, "roots-only", false, "emit registrable domains instead of URLs")
	fs.StringVar(&o.Output, "output", "", "artifact directory")
	fs.StringVar(&o.Output, "o", "", "artifact directory (shorthand)")
	fs.StringVar(&o.OutputDir, "output-dir", "", "automatic output root")
	fs.StringVar(&o.Validation, "validation", "", "local validation mode")
	fs.StringVar(&o.LogLevel, "log-level", "off", "debug, info, warn, error, or off")
	fs.BoolVar(&o.DryRun, "dry-run", false, "show the discovery plan only")
	fs.BoolVar(&o.Quiet, "quiet", false, "suppress progress and informational messages")
	fs.BoolVar(&o.NoBanner, "no-banner", false, "disable the banner")
	return fs, o
}

func runDiscover(args []string, ioo IO, resumeID string, resuming bool) int {
	fs, o := discoverFlags(ioo.Err)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		return fail(ioo.Err, 2, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " ")))
	}
	explicitMaxDepth, explicitSaturationThreshold := false, false
	fs.Visit(func(f *flag.Flag) {
		explicitMaxDepth = explicitMaxDepth || f.Name == "max-depth"
		explicitSaturationThreshold = explicitSaturationThreshold || f.Name == "saturation-threshold"
	})
	if explicitMaxDepth && o.MaxDepth < 0 {
		return fail(ioo.Err, 2, errors.New("max-depth cannot be negative"))
	}
	if explicitSaturationThreshold && o.SaturationThreshold < 0 {
		return fail(ioo.Err, 2, errors.New("saturation-threshold cannot be negative"))
	}
	cfg, err := config.Load(o.Config)
	if err != nil {
		return fail(ioo.Err, 2, err)
	}
	if o.DB != "" {
		cfg.Database.Path = o.DB
	}
	if o.Workers > 0 {
		cfg.Scheduler.Workers = o.Workers
	}
	if o.Validation == "" {
		o.Validation = cfg.Validation.Mode
	}
	if o.OutputDir == "" {
		o.OutputDir = cfg.Output.Directory
	}
	if o.MaxDepth < 0 {
		o.MaxDepth = cfg.Discovery.MaxDepth
	}
	if o.MaxHosts == 0 {
		o.MaxHosts = cfg.Discovery.MaxHosts
	}
	if o.MaxQueries == 0 {
		o.MaxQueries = cfg.Discovery.MaxQueries
	}
	if o.MaxDuration == 0 {
		o.MaxDuration = cfg.Discovery.MaxDuration
	}
	if o.SaturationThreshold < 0 {
		o.SaturationThreshold = cfg.Discovery.SaturationThreshold
	}
	if o.SaturationDepths == 0 {
		o.SaturationDepths = cfg.Discovery.SaturationConsecutiveDepths
	}
	o.Quiet = o.Quiet || ioo.Quiet
	o.NoBanner = o.NoBanner || ioo.NoBanner
	if o.HostsOnly && o.RootsOnly {
		return fail(ioo.Err, 2, errors.New("--hosts-only and --roots-only are mutually exclusive"))
	}
	for _, name := range o.Providers {
		if !knownProvider(name) {
			return fail(ioo.Err, 2, fmt.Errorf("unknown provider %q", name))
		}
	}
	if o.PageSize < 1 || o.PageSize > 100 || o.Workers < 0 || o.Workers > 256 {
		return fail(ioo.Err, 2, errors.New("page-size must be between 1 and 100 and workers between 0 and 256"))
	}
	mode, err := query.ParseValidationMode(o.Validation)
	if err != nil {
		return fail(ioo.Err, 2, err)
	}
	if !resuming {
		if strings.TrimSpace(o.Query) == "" {
			return fail(ioo.Err, 2, errors.New("discover requires -q QUERY"))
		}
		c, er := query.ExtractConstraints(o.Query)
		if er != nil {
			return fail(ioo.Err, 2, er)
		}
		if len(c.Sites) == 0 {
			return fail(ioo.Err, 2, errors.New("discover requires a positive site: scope"))
		}
	}
	if _, err = logging.New(io.Discard, o.LogLevel); err != nil {
		return fail(ioo.Err, 2, err)
	}
	dopts := discovery.Options{MaxDepth: o.MaxDepth, MaxHosts: o.MaxHosts, MaxQueries: o.MaxQueries, MaxResults: o.MaxResults, MaxResultsPerQuery: o.MaxPerQuery, MaxPages: o.MaxPages, MaxDuration: o.MaxDuration, SaturationThreshold: o.SaturationThreshold, SaturationConsecutiveDepths: o.SaturationDepths, MinSaturationDepths: 2}
	if err = dopts.Validate(); err != nil {
		return fail(ioo.Err, 2, err)
	}
	if o.DryRun {
		fmt.Fprintf(ioo.Out, "Seed: %s\nScope validation: fail-closed\nLimits: depth=%d hosts=%d queries=%d results=%d duration=%s\nSaturation: threshold=%.2f consecutive=%d\n", o.Query, o.MaxDepth, o.MaxHosts, o.MaxQueries, o.MaxResults, o.MaxDuration, o.SaturationThreshold, o.SaturationDepths)
		return 0
	}
	st, err := store.Open(config.ExpandPath(cfg.Database.Path))
	if err != nil {
		return fail(ioo.Err, 1, err)
	}
	defer st.Close()
	job := resumeID
	var dj store.DiscoveryJob
	newJob := !resuming
	if resuming {
		dj, err = st.DiscoveryJob(context.Background(), job)
		if err != nil {
			return fail(ioo.Err, 1, fmt.Errorf("discovery job %q not found", job))
		}
		o.Query = dj.SeedQuery
		if raw, er := st.JobOptions(context.Background(), job); er == nil {
			var saved discoverOptions
			if json.Unmarshal([]byte(raw), &saved) == nil {
				saved.Config = o.Config
				saved.DB = o.DB
				o = &saved
				mode, _ = query.ParseValidationMode(o.Validation)
			}
		}
		if err = json.Unmarshal([]byte(dj.OptionsJSON), &dopts); err != nil {
			return fail(ioo.Err, 1, err)
		}
	} else {
		job = newID()
		raw, _ := json.Marshal(o)
		if err = st.CreateJob(context.Background(), job, string(raw)); err != nil {
			return fail(ioo.Err, 1, err)
		}
		if err = st.CreateDiscoveryJob(context.Background(), store.DiscoveryJob{JobID: job, SeedQuery: o.Query, OptionsJSON: discovery.EncodeOptions(dopts), StartedAt: time.Now().UTC()}); err != nil {
			_ = st.SetJobStatus(context.Background(), job, "FAILED")
			return fail(ioo.Err, 1, err)
		}
		dj, _ = st.DiscoveryJob(context.Background(), job)
	}
	interactive := ui.IsTerminal(ioo.Out)
	if ioo.Interactive != nil {
		interactive = *ioo.Interactive
	}
	outputPath := ""
	if o.Output != "" {
		outputPath, err = exporter.ResolveDiscoveryDirectory(config.ExpandPath(o.OutputDir), o.Query, job, dj.OutputPath, o.Output)
		if err != nil {
			if newJob {
				_ = st.SetJobStatus(context.Background(), job, "FAILED")
			}
			return fail(ioo.Err, 1, err)
		}
		if dj.OutputPath == "" {
			if err = st.SetDiscoveryOutputPath(context.Background(), job, outputPath); err != nil {
				if newJob {
					_ = st.SetJobStatus(context.Background(), job, "FAILED")
				}
				return fail(ioo.Err, 1, err)
			}
		}
	}
	runtimes, _ := providers(cfg, httpclient.New(cfg.HTTP), o.Providers)
	if len(runtimes) == 0 {
		if newJob {
			_ = st.SetJobStatus(context.Background(), job, "FAILED")
		} else {
			// Explicit resume is sufficient evidence that an ownerless legacy
			// RUNNING row is not being driven by its original process. Keep its
			// frontier recoverable when providers are temporarily unavailable.
			_ = st.SetJobStatusIfInactive(context.Background(), job, "INTERRUPTED", time.Now())
		}
		return fail(ioo.Err, 3, errors.New("no configured provider is available right now\nrun `dorker setup`, then check readiness with `dorker providers`"))
	}
	log, err := logging.New(ioo.Err, o.LogLevel)
	if err != nil {
		if newJob {
			_ = st.SetJobStatus(context.Background(), job, "FAILED")
		}
		return fail(ioo.Err, 2, err)
	}
	parent, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, owner, lease, err := acquireJobLease(parent, st, job)
	if err != nil {
		return fail(ioo.Err, 1, err)
	}
	scopeConstraints, err := query.ExtractConstraints(o.Query)
	if err != nil {
		_ = lease.Stop()
		_ = st.FinishJob(context.Background(), job, owner, "FAILED")
		return fail(ioo.Err, 2, err)
	}
	col := &collector.Collector{Store: st, JobID: job, Normalizer: normalize.New(normalize.Options{}), ValidationMode: mode, ScopeConstraints: &scopeConstraints}
	runner := discovery.RunnerFunc(func(runctx context.Context, vs []model.QueryVariant) (discovery.RunReport, error) {
		beforeRaw, beforeAccepted := col.Raw.Load(), col.Accepted.Load()
		beforeRejected, beforeDuplicates := col.Rejected.Load(), col.Duplicates.Load()
		beforeMalformed := col.Malformed.Load()
		beforeReasons := col.RejectionReasons()
		if err := st.AddVariants(runctx, job, vs); err != nil {
			return discovery.RunReport{}, err
		}
		progress, err := st.LoadProgress(runctx, job)
		if err != nil {
			return discovery.RunReport{}, err
		}
		jobStats, _ := st.Stats(runctx, job)
		remaining := dopts.MaxResults - int(jobStats.Unique)
		if remaining <= 0 {
			return discovery.RunReport{}, nil
		}
		sched := scheduler.New(st, col, log, scheduler.Options{Workers: cfg.Scheduler.Workers, MaxPages: dopts.MaxPages, MaxResults: remaining, MaxResultsPerQuery: dopts.MaxResultsPerQuery, PageSize: o.PageSize, Retry: rl.RetryPolicy{MaxAttempts: cfg.Retry.MaxAttempts, BaseDelay: cfg.Retry.BaseDelay, MaxDelay: cfg.Retry.MaxDelay, Jitter: cfg.Retry.Jitter}, SaturationEnabled: cfg.Scheduler.Saturation.Enabled, ConsecutiveZeroYieldPages: cfg.Scheduler.Saturation.ConsecutiveZeroYieldPages, WaitForRecovery: cfg.Scheduler.Recovery.Wait, MaxRecoveryWait: cfg.Scheduler.Recovery.MaxWait})
		err = sched.Run(runctx, job, vs, runtimes, progress)
		r := sched.Report()
		reasons := map[string]int{}
		for reason, count := range col.RejectionReasons() {
			if delta := count - beforeReasons[reason]; delta > 0 {
				reasons[string(reason)] = int(delta)
			}
		}
		raw := int(col.Raw.Load() - beforeRaw)
		malformed := int(col.Malformed.Load() - beforeMalformed)
		return discovery.RunReport{Partial: r.Partial, Degraded: r.Degraded, ProviderFailures: r.ProviderFailures, Raw: raw, Normalized: raw - malformed, Accepted: int(col.Accepted.Load() - beforeAccepted), Malformed: malformed, Rejected: int(col.Rejected.Load() - beforeRejected), Duplicates: int(col.Duplicates.Load() - beforeDuplicates), RejectionReasons: reasons}, err
	})
	outUI := ui.New(ioo.Out, ui.Options{Color: streamColor(ioo, ui.ColorMode(cfg.UI.Color)), Interactive: interactive, Quiet: o.Quiet, NoBanner: true})
	errUI := ui.New(ioo.Err, ui.Options{Color: streamColor(ioo, ui.ColorMode(cfg.UI.Color)), Interactive: ui.IsTerminal(ioo.Err), Quiet: o.Quiet, NoBanner: true})
	if interactive && !o.Quiet {
		fmt.Fprintln(ioo.Out, outUI.Heading("DORKER / DISCOVER"))
		fmt.Fprintf(ioo.Out, "%s  %s\n", outUI.Muted("Seed"), outUI.Value(o.Query))
	}
	engine := &discovery.Engine{Store: st, Runner: runner}
	engine.OnProcessing = func(s discovery.ProcessingSummary) {
		attrs := []any{"depth", s.Depth, "raw", s.Raw, "normalized", s.Normalized, "accepted", s.Accepted, "rejected", s.Rejected, "malformed", s.Malformed, "duplicate_urls", s.DuplicateURLs, "hosts_extracted", s.HostsExtracted, "duplicate_hosts", s.DuplicateHosts, "frontier_added", s.FrontierAdded}
		keys := make([]string, 0, len(s.RejectionReasons))
		for reason := range s.RejectionReasons {
			keys = append(keys, reason)
		}
		sort.Strings(keys)
		for _, reason := range keys {
			attrs = append(attrs, "rejected_"+reason, s.RejectionReasons[reason])
		}
		log.Debug("discovery seed processing", attrs...)
	}
	engine.OnDepth = func(s store.DiscoveryDepthStat) {
		if interactive && !o.Quiet {
			fmt.Fprintf(ioo.Err, "%s %s · %s %s · %s %s\n", errUI.Muted("Depth"), errUI.Value(fmt.Sprint(s.Depth)), errUI.Muted("New hosts"), errUI.Value(fmt.Sprint(s.NewHosts)), errUI.Muted("Gain"), errUI.Value(fmt.Sprintf("%.2f%%", s.GainRatio*100)))
		}
	}
	result, runErr := engine.Run(ctx, job)
	leaseErr := lease.Stop()
	cleanup, cancelCleanup := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCleanup()
	status := result.Status
	if leaseErr != nil {
		runErr = leaseErr
		status = "FAILED"
	} else if status == "" {
		if errors.Is(runErr, context.Canceled) {
			status = "INTERRUPTED"
		} else if runErr != nil {
			status = "FAILED"
		} else {
			status = "COMPLETE"
		}
	}
	if err = st.FinishJob(cleanup, job, owner, status); err != nil && runErr == nil {
		runErr = err
	}
	_ = st.AddMetrics(cleanup, job, col.Raw.Load(), col.Duplicates.Load(), col.Malformed.Load())
	_ = st.AddValidationMetrics(cleanup, job, col.Accepted.Load(), col.Rejected.Load(), col.UnknownKept.Load(), col.RejectionReasons())
	_ = st.Flush(cleanup)
	jobStats, _ := st.Stats(cleanup, job)
	coverageStatus, terminalEvidence, terminalDetail, historicalDegradation, responsiveSources, degradedSources := discoveryCoverageSummary(result, jobStats)
	if result.Status == "PARTIAL" && coverageStatus == "DEGRADED" && !o.Quiet {
		writeDiscoveryCoverageDiagnostics(ioo.Err, coverageStatus, terminalDetail, responsiveSources, degradedSources)
	}
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return fail(ioo.Err, 1, runErr)
	}
	if !interactive && o.Output == "" {
		kind := "urls"
		if o.HostsOnly {
			kind = "hosts"
		} else if o.RootsOnly {
			kind = "roots"
		}
		if kind == "urls" {
			_, err = exporter.WriteCollapsed(cleanup, st, job, "txt", "-", false, ioo.Out, exporter.CollapseNone)
		} else {
			err = exporter.WriteDiscoverySelection(cleanup, st, job, kind, ioo.Out)
		}
		if err != nil {
			return fail(ioo.Err, 1, err)
		}
	} else if o.Output != "" || mustUnique(st, cleanup, job) > 0 {
		dir := outputPath
		if dir == "" {
			dir, err = exporter.ResolveDiscoveryDirectory(config.ExpandPath(o.OutputDir), o.Query, job, dj.OutputPath, o.Output)
			if err != nil {
				return fail(ioo.Err, 1, err)
			}
			if err = st.SetDiscoveryOutputPath(cleanup, job, dir); err != nil {
				return fail(ioo.Err, 1, err)
			}
		}
		depths, _ := st.DiscoveryDepths(cleanup, job)
		dj, _ := st.DiscoveryJob(cleanup, job)
		started, finished := dj.StartedAt.Format(time.RFC3339), ""
		if !dj.FinishedAt.IsZero() {
			finished = dj.FinishedAt.Format(time.RFC3339)
		}
		var health []exporter.ProviderHealthSummary
		for _, h := range jobStats.UpstreamDegradation {
			health = append(health, exporter.ProviderHealthSummary{Provider: h.Provider, ProviderInstance: h.ProviderInstance, Engine: h.Engine, Classification: h.Classification, Count: h.Count, Observations: h.Count})
		}
		paths, er := exporter.WriteDiscovery(cleanup, st, job, dir, exporter.DiscoveryReport{Schema: "dorker.discovery/v1", JobID: job, SeedQuery: o.Query, StartedAt: started, FinishedAt: finished, Status: result.Status, StopReason: result.StopReason, CoverageStatus: coverageStatus, HistoricalDegradation: historicalDegradation, TerminalEvidenceStatus: terminalEvidence, TerminalDetail: terminalDetail, Validation: o.Validation, Limits: dopts, Depths: depths, ProviderHealth: health, HealthCountSemantics: "distinct_query_page_message_observations", ResponsiveSources: responsiveSources})
		if er != nil {
			return fail(ioo.Err, 1, fmt.Errorf("write discovery output %q: %w\nchoose a writable directory with --output or --output-dir", dir, er))
		}
		if interactive && !o.Quiet {
			fmt.Fprintf(ioo.Out, "\nDiscovery %s\nURLs/hosts/roots  %d/%d/%d\nDepth/queries      %d/%d\nStop reason        %s\nSaved              %s\n", strings.ToLower(result.Status), mustUnique(st, cleanup, job), result.Hosts, result.Roots, result.Depth, result.Queries, result.StopReason, paths["report"])
		}
	}
	if errors.Is(runErr, context.Canceled) {
		fmt.Fprintf(ioo.Err, "job %s interrupted; resume with: dorker resume %s\n", job, job)
		return 130
	}
	return 0
}

func discoveryCoverageSummary(result discovery.Result, stats model.Stats) (status, terminalEvidence, detail string, historical bool, responsive, degraded []string) {
	status = "TRUSTWORTHY"
	if result.Status == "PARTIAL" {
		status = "INCOMPLETE"
	}
	for _, depth := range result.Stats {
		if !depth.Trustworthy {
			status = "DEGRADED"
			historical = true
		}
	}
	switch result.StopReason {
	case discovery.StopFrontierDegraded:
		terminalEvidence = "INSUFFICIENT"
		detail = "frontier exhausted without trustworthy saturation evidence"
	case discovery.StopSaturated:
		if result.Status == "SATURATED" {
			terminalEvidence = "SUFFICIENT"
		} else {
			terminalEvidence = "INSUFFICIENT"
		}
		detail = "trustworthy low-gain saturation evidence reached"
	case discovery.StopFrontierExhausted:
		detail = "frontier exhausted after all queued hosts were expanded"
	case discovery.StopMaxDepth:
		detail = "maximum discovery depth reached"
	case discovery.StopMaxHosts:
		detail = "maximum host count reached"
	case discovery.StopMaxQueries:
		detail = "maximum query count reached"
	case discovery.StopMaxResults:
		detail = "maximum result count reached"
	case discovery.StopMaxDuration:
		detail = "maximum duration reached"
	case discovery.StopInterrupted:
		detail = "interrupted before completion"
	case discovery.StopPartial:
		detail = "coverage was incomplete"
	}
	if terminalEvidence == "" {
		terminalEvidence = "NOT_APPLICABLE"
	}
	for source := range stats.BySourceEngine {
		if strings.TrimSpace(source) != "" {
			responsive = append(responsive, source)
		}
	}
	seenDegraded := map[string]bool{}
	for _, item := range stats.UpstreamDegradation {
		if item.Engine != "" && !seenDegraded[item.Engine] {
			seenDegraded[item.Engine] = true
			degraded = append(degraded, item.Engine)
		}
	}
	sort.Strings(responsive)
	sort.Strings(degraded)
	return status, terminalEvidence, detail, historical, responsive, degraded
}

func writeDiscoveryCoverageDiagnostics(w io.Writer, status, detail string, responsive, degraded []string) {
	fmt.Fprintf(w, "Coverage          %s\n", strings.ToLower(status))
	if len(responsive) > 0 {
		fmt.Fprintf(w, "Responsive sources observed  %s\n", strings.Join(responsive, ", "))
	}
	if len(degraded) > 0 {
		fmt.Fprintf(w, "Degraded sources  %s\n", strings.Join(degraded, ", "))
	}
	if detail != "" {
		fmt.Fprintf(w, "Reason            %s\n", detail)
	}
}

func mustUnique(st *store.Store, ctx context.Context, job string) int64 {
	s, err := st.Stats(ctx, job)
	if err != nil {
		return 0
	}
	return s.Unique
}

func isDiscoveryJob(args []string, id string) bool {
	cfgPath := config.DefaultPath()
	dbPath := ""
	for i := 0; i < len(args); i++ {
		if v, ok := strings.CutPrefix(args[i], "--config="); ok {
			cfgPath = v
		} else if v, ok := strings.CutPrefix(args[i], "--db="); ok {
			dbPath = v
		} else if args[i] == "--config" && i+1 < len(args) {
			cfgPath = args[i+1]
			i++
		} else if args[i] == "--db" && i+1 < len(args) {
			dbPath = args[i+1]
			i++
		}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return false
	}
	if dbPath != "" {
		cfg.Database.Path = dbPath
	}
	st, err := store.Open(config.ExpandPath(cfg.Database.Path))
	if err != nil {
		return false
	}
	defer st.Close()
	ok, _ := st.DiscoveryJobExists(context.Background(), id)
	return ok
}
