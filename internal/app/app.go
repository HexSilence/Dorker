package app

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/HexSilence/dorker/internal/buildinfo"
	"github.com/HexSilence/dorker/internal/collector"
	"github.com/HexSilence/dorker/internal/config"
	exporter "github.com/HexSilence/dorker/internal/export"
	"github.com/HexSilence/dorker/internal/ghdb"
	"github.com/HexSilence/dorker/internal/httpclient"
	"github.com/HexSilence/dorker/internal/logging"
	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/normalize"
	"github.com/HexSilence/dorker/internal/provider"
	"github.com/HexSilence/dorker/internal/provider/brave"
	"github.com/HexSilence/dorker/internal/provider/googleapi"
	"github.com/HexSilence/dorker/internal/provider/mock"
	"github.com/HexSilence/dorker/internal/provider/searxng"
	"github.com/HexSilence/dorker/internal/provider/serpapi"
	"github.com/HexSilence/dorker/internal/query"
	rl "github.com/HexSilence/dorker/internal/ratelimit"
	"github.com/HexSilence/dorker/internal/scheduler"
	"github.com/HexSilence/dorker/internal/store"
	"github.com/HexSilence/dorker/internal/ui"
	"gopkg.in/yaml.v3"
)

type IO struct {
	In              io.Reader
	Out, Err        io.Writer
	Quiet, NoBanner bool
	Interactive     *bool
	Color           string
}
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }
func Run(args []string, streams IO) int {
	for len(args) > 0 && (args[0] == "--quiet" || args[0] == "--no-banner" || args[0] == "--color") {
		if args[0] == "--color" {
			if len(args) < 2 || (args[1] != "auto" && args[1] != "always" && args[1] != "never") {
				return fail(streams.Err, 2, errors.New("--color requires auto, always, or never"))
			}
			streams.Color, args = args[1], args[2:]
			continue
		}
		if args[0] == "--quiet" {
			streams.Quiet = true
		} else {
			streams.NoBanner = true
		}
		args = args[1:]
	}
	if len(args) == 0 {
		cfg, err := config.Load("")
		if err != nil {
			cfg = config.Default()
		}
		r := renderer(streams, cfg, streams.Quiet, streams.NoBanner)
		r.Banner()
		r.Startup()
		return 0
	}
	if args[0] == "--help" || args[0] == "-h" {
		ui.New(streams.Out, ui.Options{Color: streamColor(streams, ui.ColorAuto), Interactive: ui.IsTerminal(streams.Out) || streams.Color == "always"}).Help()
		return 0
	}
	if args[0] == "--version" {
		fmt.Fprintf(streams.Out, "%s %s (commit %s, built %s)\n", buildinfo.Name, buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return 0
	}
	if args[0] == "--resume" {
		return runSearch(args[1:], streams, "", true, searchOrigin{})
	}
	cmd := args[0]
	if strings.HasPrefix(cmd, "-") {
		cmd = "search"
	} else {
		args = args[1:]
	}
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") && cmd != "search" && cmd != "resume" {
		if ui.CommandHelp(streams.Out, cmd, config.DefaultPath()) {
			return 0
		}
	}
	if cmd == "providers" && len(args) > 1 && args[0] == "test" && (args[1] == "--help" || args[1] == "-h") {
		ui.CommandHelp(streams.Out, "providers test", config.DefaultPath())
		return 0
	}
	switch cmd {
	case "search":
		return runSearch(args, streams, "", false, searchOrigin{})
	case "resume":
		id := ""
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			id = args[0]
			args = args[1:]
		}
		return runSearch(args, streams, id, true, searchOrigin{})
	case "providers":
		return runProviders(args, streams)
	case "ghdb":
		return runGHDB(args, streams)
	case "jobs":
		return runJobs(args, streams)
	case "stats":
		return runStats(args, streams)
	case "export":
		return runExport(args, streams)
	case "config":
		return runConfig(args, streams)
	case "doctor":
		return runDoctor(args, streams)
	case "setup":
		return runSetup(args, streams)
	case "query":
		return runQuery(args, streams)
	case "version":
		fmt.Fprintf(streams.Out, "%s %s (commit %s, built %s)\n", buildinfo.Name, buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return 0
	case "help", "--help", "-h":
		ui.Help(streams.Out)
		return 0
	default:
		fmt.Fprintf(streams.Err, "unknown command %q\n\n", cmd)
		ui.Help(streams.Err)
		return 2
	}
}

func runGHDB(args []string, ioo IO) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		ui.CommandHelp(ioo.Out, "ghdb", config.DefaultPath())
		return 0
	}
	entries := ghdb.All()
	category, rest, err := takeOption(args, "--category")
	if err != nil {
		return fail(ioo.Err, 2, err)
	}
	if category != "" {
		entries = ghdb.Category(category)
		if len(entries) == 0 {
			return fail(ioo.Err, 2, fmt.Errorf("no GHDB entries in category %q", category))
		}
	}
	r := ui.New(ioo.Out, ui.Options{Color: streamColor(ioo, ui.ColorAuto), Interactive: ui.IsTerminal(ioo.Out) || ioo.Color == "always"})
	if len(rest) == 0 {
		if !inputTerminal(ioo) {
			return fail(ioo.Err, 2, errors.New("interactive GHDB needs a terminal\ntry: dorker ghdb list"))
		}
		return runGHDBInteractive(entries, r, ioo)
	}
	switch rest[0] {
	case "list":
		if len(rest) != 1 {
			return fail(ioo.Err, 2, errors.New("ghdb list accepts only --category CATEGORY"))
		}
		r.GHDBList(toUIEntries(entries), false)
		return 0
	case "show":
		if len(rest) != 2 {
			return fail(ioo.Err, 2, errors.New("ghdb show requires an ID"))
		}
		e, err := ghdbEntry(rest[1])
		if err != nil {
			return fail(ioo.Err, 2, err)
		}
		r.GHDBShow(toUIEntry(e, e.QueryTemplate), true)
		return 0
	case "find":
		if len(rest) < 2 {
			return fail(ioo.Err, 2, errors.New("ghdb find requires a search term"))
		}
		found := ghdb.Find(strings.Join(rest[1:], " "))
		if category != "" {
			found = filterGHDBSet(found, entries)
		}
		r.GHDBList(toUIEntries(found), false)
		return 0
	case "run":
		if len(rest) < 2 {
			return fail(ioo.Err, 2, errors.New("ghdb run requires an ID"))
		}
		e, err := ghdbEntry(rest[1])
		if err != nil {
			return fail(ioo.Err, 2, err)
		}
		domain, searchArgs, err := takeOption(rest[2:], "--domain")
		if err != nil {
			return fail(ioo.Err, 2, err)
		}
		queryText, err := ghdb.Expand(e, domain)
		if err != nil {
			return fail(ioo.Err, 2, err)
		}
		return runSearch(append([]string{"-q", queryText}, searchArgs...), ioo, "", false, searchOrigin{GHDBTitle: e.Title, Domain: domain})
	default:
		return fail(ioo.Err, 2, fmt.Errorf("unknown ghdb command %q", rest[0]))
	}
}

func runGHDBInteractive(entries []ghdb.Entry, r *ui.Renderer, ioo IO) int {
	r.GHDBList(toUIEntries(entries), true)
	reader := bufio.NewReader(ioo.In)
	fmt.Fprintf(ioo.Out, "\nSelect [%d-%d], or q to quit: ", entries[0].ID, entries[len(entries)-1].ID)
	line, err := readPromptLine(reader)
	if err == io.EOF {
		fmt.Fprintln(ioo.Out, "\nCancelled.")
		return 0
	}
	if err != nil {
		return fail(ioo.Err, 1, err)
	}
	if strings.EqualFold(line, "q") {
		fmt.Fprintln(ioo.Out, "Cancelled.")
		return 0
	}
	id, err := strconv.Atoi(line)
	if err != nil {
		return fail(ioo.Err, 2, errors.New("select a listed GHDB number or q"))
	}
	var selected ghdb.Entry
	found := false
	for _, e := range entries {
		if e.ID == id {
			selected, found = e, true
			break
		}
	}
	if !found {
		return fail(ioo.Err, 2, fmt.Errorf("GHDB entry %d is not in this list", id))
	}
	base, _ := ghdb.Expand(selected, "")
	fmt.Fprintln(ioo.Out)
	r.GHDBShow(toUIEntry(selected, base), false)
	fmt.Fprint(ioo.Out, "\nTarget domain (optional)\n  Leave empty to search globally.\n\n> ")
	domain, err := readPromptLine(reader)
	if err == io.EOF {
		fmt.Fprintln(ioo.Out, "\nCancelled.")
		return 0
	}
	if err != nil {
		return fail(ioo.Err, 1, err)
	}
	queryText, err := ghdb.Expand(selected, domain)
	if err != nil {
		return fail(ioo.Err, 2, err)
	}
	fmt.Fprintln(ioo.Out)
	r.GHDBReady(selected.Title, queryText)
	fmt.Fprint(ioo.Out, "\nRun search? [Y/n]: ")
	answer, err := readPromptLine(reader)
	if err == io.EOF {
		fmt.Fprintln(ioo.Out, "\nCancelled.")
		return 0
	}
	if err != nil {
		return fail(ioo.Err, 1, err)
	}
	if answer != "" && !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
		fmt.Fprintln(ioo.Out, "Cancelled.")
		return 0
	}
	return runSearch([]string{"-q", queryText}, ioo, "", false, searchOrigin{GHDBTitle: selected.Title, Domain: domain})
}

func readPromptLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	line = strings.TrimSpace(line)
	for _, ch := range line {
		if ch < 0x20 || ch == 0x7f {
			return "", errors.New("input contains control characters")
		}
	}
	return line, err
}

func inputTerminal(ioo IO) bool {
	if ioo.Interactive != nil {
		return *ioo.Interactive
	}
	f, ok := ioo.In.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func takeOption(args []string, name string) (string, []string, error) {
	var value string
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] != name {
			rest = append(rest, args[i])
			continue
		}
		if value != "" || i+1 >= len(args) {
			return "", nil, fmt.Errorf("%s requires one value", name)
		}
		value, i = args[i+1], i+1
	}
	return value, rest, nil
}

func ghdbEntry(raw string) (ghdb.Entry, error) {
	id, err := strconv.Atoi(raw)
	if err != nil {
		return ghdb.Entry{}, fmt.Errorf("invalid GHDB ID %q", raw)
	}
	e, ok := ghdb.ByID(id)
	if !ok {
		return ghdb.Entry{}, fmt.Errorf("GHDB entry %d not found", id)
	}
	return e, nil
}
func toUIEntry(e ghdb.Entry, queryText string) ui.GHDBEntry {
	return ui.GHDBEntry{ID: e.ID, Title: e.Title, Description: e.Description, Category: e.Category, Query: queryText}
}
func toUIEntries(entries []ghdb.Entry) []ui.GHDBEntry {
	out := make([]ui.GHDBEntry, 0, len(entries))
	for _, e := range entries {
		q, _ := ghdb.Expand(e, "")
		out = append(out, toUIEntry(e, q))
	}
	return out
}
func filterGHDBSet(found, allowed []ghdb.Entry) []ghdb.Entry {
	ids := map[int]bool{}
	for _, e := range allowed {
		ids[e.ID] = true
	}
	var out []ghdb.Entry
	for _, e := range found {
		if ids[e.ID] {
			out = append(out, e)
		}
	}
	return out
}

type searchOptions struct {
	Queries, Files, Providers                                              multiFlag
	Config, DB, Mode, Format, Output, OutputDir, LogLevel, JobID, Collapse string
	MaxVariants, Workers, MaxResults, MaxPerQuery, MaxPages, PageSize      int
	KeepTracking, Overwrite, Stream, DryRun                                bool
	IncludeDomains, ExcludeDomains                                         multiFlag
	IncludeRegex, ExcludeRegex                                             string
	SearXNGEngines, SearXNGStrategy                                        string
	Validation                                                             string
	Quiet, NoBanner, WaitForRecovery                                       bool
	MaxRecoveryWait                                                        time.Duration
	SaturationEnabled                                                      bool
	SaturationPages                                                        int
}

func searchFlags(name string, errw io.Writer, color ui.ColorMode, interactive bool) (*flag.FlagSet, *searchOptions) {
	o := &searchOptions{}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(errw)
	fs.Usage = func() { ui.SearchHelpColor(errw, config.DefaultPath(), color, interactive) }
	fs.Var(&o.Queries, "q", "search query (repeatable)")
	fs.Var(&o.Files, "f", "query file, one query per line (repeatable)")
	fs.Var(&o.Providers, "provider", "provider name (repeatable; default: all enabled/configured)")
	fs.StringVar(&o.Config, "config", config.DefaultPath(), "YAML configuration path")
	fs.StringVar(&o.DB, "db", "", "SQLite database path override")
	fs.StringVar(&o.Mode, "query-mode", "exact", "exact or conservative")
	fs.IntVar(&o.MaxVariants, "max-query-variants", 2, "maximum variants per original query")
	fs.IntVar(&o.Workers, "workers", 0, "bounded worker count (config default)")
	fs.IntVar(&o.MaxResults, "max-results", 1000, "maximum unique results for the job (0 unlimited)")
	fs.IntVar(&o.MaxPerQuery, "max-results-per-query", 100, "maximum unique results per query (0 unlimited)")
	fs.IntVar(&o.MaxPages, "max-pages", 10, "maximum pages per provider/query (0 unlimited)")
	fs.IntVar(&o.PageSize, "page-size", 10, "requested page size")
	fs.BoolVar(&o.KeepTracking, "keep-tracking-params", false, "preserve common tracking parameters")
	fs.Var(&o.IncludeDomains, "include-domain", "include domain and subdomains (repeatable)")
	fs.Var(&o.ExcludeDomains, "exclude-domain", "exclude domain and subdomains (repeatable)")
	fs.StringVar(&o.IncludeRegex, "include-regex", "", "include normalized URLs matching RE2 regex")
	fs.StringVar(&o.ExcludeRegex, "exclude-regex", "", "exclude normalized URLs matching RE2 regex")
	fs.StringVar(&o.Format, "format", "txt", "export format: txt, jsonl, csv")
	fs.StringVar(&o.Output, "output", "", "export destination (- for stdout)")
	fs.StringVar(&o.Output, "o", "", "export destination (shorthand)")
	fs.StringVar(&o.OutputDir, "output-dir", "", "automatic output root (config default)")
	fs.BoolVar(&o.Overwrite, "overwrite", false, "atomically replace an existing output file")
	fs.BoolVar(&o.Stream, "stream", false, "write each newly discovered normalized URL to stdout")
	fs.StringVar(&o.Collapse, "collapse", "", "collapse output: none, host, or domain")
	fs.BoolVar(&o.DryRun, "dry-run", false, "show plan without network requests or database writes")
	fs.StringVar(&o.LogLevel, "log-level", "off", "debug, info, warn, or error; default hides internal logs")
	fs.StringVar(&o.JobID, "job-id", "", "job ID used with --resume")
	fs.StringVar(&o.SearXNGEngines, "searxng-engines", "", "comma-separated SearXNG engines (only sent when set)")
	fs.StringVar(&o.SearXNGStrategy, "searxng-strategy", "", "SearXNG instance strategy: balanced or coverage")
	fs.StringVar(&o.Validation, "validation", "", "local dork validation: off, conservative, or strict (config default)")
	fs.BoolVar(&o.Quiet, "quiet", false, "suppress banner, progress, and informational messages")
	fs.BoolVar(&o.NoBanner, "no-banner", false, "disable the interactive banner")
	fs.BoolVar(&o.WaitForRecovery, "wait-for-recovery", false, "wait a bounded time for temporary provider cooldowns")
	fs.DurationVar(&o.MaxRecoveryWait, "max-recovery-wait", 0, "maximum total recovery wait per provider/query (config default)")
	return fs, o
}

type searchOrigin struct{ GHDBTitle, Domain string }

func runSearch(args []string, ioo IO, resumeID string, resuming bool, origin searchOrigin) int {
	fs, o := searchFlags("search", ioo.Err, streamColor(ioo, ui.ColorAuto), ui.IsTerminal(ioo.Err) || ioo.Color == "always")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(ioo.Err, "unexpected positional arguments:", strings.Join(fs.Args(), " "))
		return 2
	}
	cfg, err := config.Load(o.Config)
	if err != nil {
		return fail(ioo.Err, 2, err)
	}
	o.Quiet = o.Quiet || ioo.Quiet
	o.NoBanner = o.NoBanner || ioo.NoBanner
	stdoutTTY := ui.IsTerminal(ioo.Out)
	if ioo.Interactive != nil {
		stdoutTTY = *ioo.Interactive
	}
	autoStdout := !stdoutTTY && o.Output == "" && !o.Stream && !o.DryRun
	autoFile := stdoutTTY && o.Output == "" && !o.Stream && !o.DryRun && !resuming
	if o.DB != "" {
		cfg.Database.Path = o.DB
	}
	if o.Workers > 0 {
		cfg.Scheduler.Workers = o.Workers
	}
	spc := cfg.Providers["searxng"]
	if o.SearXNGEngines != "" {
		spc.Engines = strings.Split(o.SearXNGEngines, ",")
	}
	if o.SearXNGStrategy != "" {
		spc.Strategy = o.SearXNGStrategy
	}
	cfg.Providers["searxng"] = spc
	if o.Validation == "" {
		o.Validation = cfg.Validation.Mode
	}
	if o.Collapse == "" {
		o.Collapse = cfg.Output.Collapse
	}
	if o.OutputDir == "" {
		o.OutputDir = cfg.Output.Directory
	}
	collapseMode, err := exporter.ParseCollapseMode(o.Collapse)
	if err != nil {
		return fail(ioo.Err, 2, err)
	}
	if o.MaxRecoveryWait == 0 {
		o.MaxRecoveryWait = cfg.Scheduler.Recovery.MaxWait
	}
	if !o.WaitForRecovery {
		o.WaitForRecovery = cfg.Scheduler.Recovery.Wait
	}
	o.SaturationEnabled = cfg.Scheduler.Saturation.Enabled
	o.SaturationPages = cfg.Scheduler.Saturation.ConsecutiveZeroYieldPages
	validationMode, err := query.ParseValidationMode(o.Validation)
	if err != nil {
		return fail(ioo.Err, 2, err)
	}
	if err := cfg.Validate(); err != nil {
		return fail(ioo.Err, 2, err)
	}
	if o.Quiet {
		o.LogLevel = "off"
	}
	log, err := logging.New(ioo.Err, o.LogLevel)
	if err != nil {
		return fail(ioo.Err, 2, err)
	}
	filter, err := collector.CompileFilter(o.IncludeDomains, o.ExcludeDomains, o.IncludeRegex, o.ExcludeRegex)
	if err != nil {
		return fail(ioo.Err, 2, err)
	}
	if o.Format != "txt" && o.Format != "jsonl" && o.Format != "csv" {
		return fail(ioo.Err, 2, fmt.Errorf("format must be txt, jsonl, or csv"))
	}
	if o.PageSize < 1 || o.PageSize > 100 || o.MaxPages < 0 || o.MaxResults < 0 || o.MaxPerQuery < 0 {
		return fail(ioo.Err, 2, errors.New("invalid search limits"))
	}
	client := httpclient.New(cfg.HTTP)
	runtimes, statuses := providers(cfg, client, o.Providers)
	if len(runtimes) == 0 && !o.DryRun {
		for _, name := range o.Providers {
			if name == "searxng" {
				return fail(ioo.Err, 3, errors.New("no SearXNG instance is available right now"))
			}
		}
		return fail(ioo.Err, 3, errors.New("no configured provider is available right now"))
	}
	stdin := false
	if f, ok := ioo.In.(*os.File); ok {
		if st, e := f.Stat(); e == nil {
			stdin = st.Mode()&os.ModeCharDevice == 0
		}
	} else {
		stdin = len(o.Queries) == 0 && len(o.Files) == 0
	}
	queries, err := query.Load(o.Queries, o.Files, ioo.In, stdin && !resuming)
	if resuming {
		queries = nil
		err = nil
	}
	if err != nil {
		return fail(ioo.Err, 2, err)
	}
	vs, err := query.Plan(queries, o.Mode, o.MaxVariants)
	if err != nil && !resuming {
		return fail(ioo.Err, 2, err)
	}
	if !resuming {
		for _, raw := range queries {
			if _, e := query.ExtractConstraints(raw); e != nil {
				return fail(ioo.Err, 2, e)
			}
		}
	}
	if o.DryRun {
		fmt.Fprintf(ioo.Out, "Queries: %d\nVariants: %d\nProviders:\n", len(queries), len(vs))
		for _, s := range statuses {
			if s.Selected {
				fmt.Fprintf(ioo.Out, "  %s (%s)\n", s.Name, s.State)
			}
		}
		fmt.Fprintf(ioo.Out, "Limits: results=%d per-query=%d pages=%d workers=%d\n", o.MaxResults, o.MaxPerQuery, o.MaxPages, cfg.Scheduler.Workers)
		for _, v := range vs {
			fmt.Fprintf(ioo.Out, "  %q -> %q\n", v.Original, v.Variant)
		}
		return 0
	}
	st, err := store.Open(config.ExpandPath(cfg.Database.Path))
	if err != nil {
		return fail(ioo.Err, 1, err)
	}
	defer st.Close()
	job := resumeID
	if o.JobID != "" {
		job = o.JobID
	}
	var progress []store.Progress
	if resuming {
		if job == "" {
			job, err = st.LatestResumableJob(context.Background())
			if err != nil {
				return fail(ioo.Err, 1, errors.New("no resumable job found"))
			}
		}
		ok, e := st.JobExists(context.Background(), job)
		if e != nil || !ok {
			return fail(ioo.Err, 1, fmt.Errorf("job %q not found", job))
		}
		vs, err = st.Variants(context.Background(), job)
		if err != nil {
			return fail(ioo.Err, 1, err)
		}
		progress, err = st.LoadProgress(context.Background(), job)
		if err != nil {
			return fail(ioo.Err, 1, err)
		}
		if raw, e := st.JobOptions(context.Background(), job); e == nil {
			var saved searchOptions
			if json.Unmarshal([]byte(raw), &saved) == nil {
				// Jobs created before Phase 4 had no local validation. Preserve that
				// behavior instead of silently applying today's conservative default.
				if saved.Validation == "" {
					saved.Validation = string(query.ValidationOff)
				}
				o.Validation = saved.Validation
				validationMode, e = query.ParseValidationMode(o.Validation)
				if e != nil {
					return fail(ioo.Err, 1, fmt.Errorf("stored validation mode: %w", e))
				}
				if saved.SaturationPages > 0 {
					o.SaturationEnabled = saved.SaturationEnabled
					o.SaturationPages = saved.SaturationPages
				}
			}
		}
	} else {
		job = newID()
		opt, _ := json.Marshal(o)
		if err = st.CreateJob(context.Background(), job, string(opt)); err != nil {
			return fail(ioo.Err, 1, err)
		}
		if err = st.AddVariants(context.Background(), job, vs); err != nil {
			return fail(ioo.Err, 1, err)
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	col := &collector.Collector{Store: st, JobID: job, Normalizer: normalize.New(normalize.Options{KeepTracking: o.KeepTracking}), Filter: filter, ValidationMode: validationMode}
	col.OnRejected = func(r model.Result, vr query.ValidationResult) {
		reason := "constraint_mismatch"
		if len(vr.Reasons) > 0 {
			reason = string(vr.Reasons[0])
		}
		log.Debug("validation rejected", "provider", r.Provider, "url", r.URL, "reason", reason)
	}
	if o.Stream && collapseMode == exporter.CollapseNone {
		col.OnUnique = func(u string) { fmt.Fprintln(ioo.Out, u) }
	}
	presenter := renderer(ioo, cfg, o.Quiet, o.NoBanner)
	providerNames := make([]string, 0, len(statuses))
	for _, s := range statuses {
		if s.Selected {
			providerNames = append(providerNames, s.Name)
		}
	}
	queryText := "resume"
	if len(vs) > 0 {
		queryText = vs[0].Original
	}
	presenter.SearchStart(ui.SearchStart{Provider: strings.Join(providerNames, ","), Query: queryText, Queries: len(queries), Validation: o.Validation, Limit: o.MaxResults})
	stopProgress := startProgress(ctx, st, job, ioo.Err, col, o.Quiet)
	sched := scheduler.New(st, col, log, scheduler.Options{Workers: cfg.Scheduler.Workers, MaxPages: o.MaxPages, MaxResults: o.MaxResults, MaxResultsPerQuery: o.MaxPerQuery, PageSize: o.PageSize, Retry: rl.RetryPolicy{MaxAttempts: cfg.Retry.MaxAttempts, BaseDelay: cfg.Retry.BaseDelay, MaxDelay: cfg.Retry.MaxDelay, Jitter: cfg.Retry.Jitter}, SaturationEnabled: o.SaturationEnabled, ConsecutiveZeroYieldPages: o.SaturationPages, WaitForRecovery: o.WaitForRecovery, MaxRecoveryWait: o.MaxRecoveryWait})
	log.Info("job started", "job", job, "queries", len(vs), "providers", len(runtimes))
	err = sched.Run(ctx, job, vs, runtimes, progress)
	stopProgress()
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cleanupCancel()
	report := sched.Report()
	if finalProgress, e := st.LoadProgress(cleanupCtx, job); e == nil {
		for _, p := range finalProgress {
			switch p.State {
			case "SATURATED":
				report.Saturated = true
				if report.SaturationReason == "" {
					report.SaturationReason = p.ExhaustedReason
				}
			case "COOLDOWN", "DEGRADED", "FAILED", "UNAVAILABLE", "QUOTA_EXHAUSTED":
				report.Partial = true
			}
		}
	}
	preStats, _ := st.Stats(cleanupCtx, job)
	status, code := finalJobStatus(err, report, preStats.Unique)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Error("job failed", "error", err)
	}
	if e := st.SetJobStatus(cleanupCtx, job, status); e != nil && code == 0 {
		err = e
		code = 1
	}
	_ = st.Flush(cleanupCtx)
	if e := st.AddMetrics(cleanupCtx, job, col.Raw.Load(), col.Duplicates.Load(), col.Malformed.Load()); e != nil && code == 0 {
		log.Error("save metrics", "error", e)
		code = 1
	}
	if e := st.AddValidationMetrics(cleanupCtx, job, col.Accepted.Load(), col.Rejected.Load(), col.UnknownKept.Load(), col.RejectionReasons()); e != nil && code == 0 {
		log.Error("save validation metrics", "error", e)
		code = 1
	}
	exportDestination := o.Output
	if autoStdout {
		exportDestination = "-"
	}
	if o.Stream && collapseMode != exporter.CollapseNone && exportDestination == "" {
		exportDestination = "-"
	}
	outputCount := int(statsCount(preStats.Unique))
	if code == 0 && autoFile && preStats.Unique > 0 {
		components := []string{"search", exporter.Slug(queryText)}
		if origin.GHDBTitle != "" {
			components = []string{"ghdb", origin.GHDBTitle}
			if origin.Domain != "" {
				components = []string{origin.Domain, origin.GHDBTitle}
			}
		}
		exportDestination, err = exporter.AutomaticPath(config.ExpandPath(o.OutputDir), components, o.Format)
		if err != nil {
			return fail(ioo.Err, 1, fmt.Errorf("automatic output: %w", err))
		}
	}
	if code == 0 && exportDestination != "" {
		if o.Stream && o.Output == "-" {
			return fail(ioo.Err, 2, errors.New("--stream and --output - would duplicate stdout"))
		}
		var e error
		outputCount, e = exporter.WriteCollapsed(cleanupCtx, st, job, o.Format, exportDestination, o.Overwrite, ioo.Out, collapseMode)
		if e != nil {
			return fail(ioo.Err, 1, e)
		}
	} else if code == 0 && collapseMode != exporter.CollapseNone {
		// Interactive searches may have no destination, but the human summary still
		// needs the deterministic collapsed selection count.
		outputCount, err = exporter.WriteCollapsed(cleanupCtx, st, job, o.Format, "-", false, io.Discard, collapseMode)
		if err != nil {
			return fail(ioo.Err, 1, err)
		}
	}
	stats, _ := st.Stats(cleanupCtx, job)
	presenter.SummaryDetails(ui.Summary{Job: job, Status: status, Output: exportDestination, SaturationReason: report.SaturationReason, Stats: stats, Collapse: o.Collapse, OutputCount: int64(outputCount)})
	log.Info("job finished", "job", job, "status", status, "raw", stats.Raw, "accepted", stats.Accepted, "rejected", stats.Rejected, "unknown_kept", stats.UnknownKept, "unique", stats.Unique, "duplicates", stats.Duplicates, "malformed", stats.Malformed, "errors", stats.Errors)
	if stats.Errors > 0 && !o.Quiet {
		ui.PrintWarning(ioo.Err, fmt.Sprintf("%d provider error(s); other providers kept going", stats.Errors))
	}
	if code == 130 {
		fmt.Fprintf(ioo.Err, "job %s interrupted; resume with: %s resume %s\n", job, buildinfo.Name, job)
	}
	return code
}

func finalJobStatus(err error, report scheduler.Report, unique int64) (string, int) {
	if errors.Is(err, context.Canceled) {
		return "INTERRUPTED", 130
	}
	if err != nil || (report.Partial && report.ErrorPages > 0 && unique == 0) {
		return "FAILED", 1
	}
	if report.Partial {
		return "PARTIAL", 0
	}
	if report.Saturated {
		return "SATURATED", 0
	}
	if report.Degraded {
		return "COMPLETE_DEGRADED", 0
	}
	return "COMPLETE", 0
}

type providerStatus struct {
	Name                          string
	Enabled, Configured, Selected bool
	State                         provider.State
	Capabilities                  provider.Capabilities
	Instances                     []instanceStatus
}
type instanceStatus struct {
	Name   string
	State  provider.State
	Health provider.HealthStatus
}

func providers(c config.Config, client *http.Client, selected []string) ([]scheduler.Runtime, []providerStatus) {
	chosen := map[string]bool{}
	for _, n := range selected {
		chosen[n] = true
	}
	explicit := len(chosen) > 0
	type candidate struct {
		name       string
		configured bool
		p          provider.Provider
	}
	var all []candidate
	spc := c.Providers["searxng"]
	instanceCfgs := append([]config.SearXNGInstanceConfig(nil), spc.Instances...)
	if envURL := strings.TrimSpace(os.Getenv("SEARXNG_URL")); envURL != "" && len(instanceCfgs) == 0 {
		instanceCfgs = []config.SearXNGInstanceConfig{{Name: "local", URL: envURL, TokenEnv: "SEARXNG_TOKEN", RequestsPerSecond: spc.RequestsPerSecond, Burst: spc.Burst}}
	}
	var sxClients []*searxng.Client
	for _, in := range instanceCfgs {
		rps, burst := in.RequestsPerSecond, in.Burst
		if rps <= 0 {
			rps = spc.RequestsPerSecond
		}
		if burst <= 0 {
			burst = spc.Burst
		}
		token := ""
		if in.TokenEnv != "" {
			token = os.Getenv(in.TokenEnv)
		}
		sx, err := searxng.New(client, searxng.Options{Name: in.Name, URL: in.URL, Token: token, Engines: spc.Engines, Categories: spc.Categories, Language: spc.Language, SafeSearch: spc.SafeSearch, TimeRange: spc.TimeRange, RequestsPerSecond: rps, Burst: burst, MaxBodyBytes: c.HTTP.MaxBodyBytes})
		if err == nil {
			sxClients = append(sxClients, sx)
		}
	}
	var sx provider.Provider = &unconfiguredProvider{name: "searxng"}
	if len(sxClients) > 0 {
		if cluster, err := searxng.NewCluster(sxClients, spc.Strategy); err == nil {
			sx = cluster
		}
	}
	all = append(all, candidate{"searxng", len(sxClients) > 0, sx})
	googleKey, googleCSE := strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")), strings.TrimSpace(os.Getenv("GOOGLE_CSE_ID"))
	braveKey, serpAPIKey := strings.TrimSpace(os.Getenv("BRAVE_API_KEY")), strings.TrimSpace(os.Getenv("SERPAPI_API_KEY"))
	all = append(all, candidate{"google-api", googleKey != "" && googleCSE != "", googleapi.New(client, googleKey, googleCSE, "", c.HTTP.MaxBodyBytes)})
	all = append(all, candidate{"brave", braveKey != "", brave.New(client, braveKey, "", c.HTTP.MaxBodyBytes)})
	all = append(all, candidate{"serpapi", serpAPIKey != "", serpapi.New(client, serpAPIKey, "", c.HTTP.MaxBodyBytes)})
	mp := mock.New(mock.Config{Total: envInt("DORKER_MOCK_RESULTS", 100), PageSize: 10, RateLimitPage: envInt("DORKER_MOCK_RATE_LIMIT_PAGE", -1), TemporaryPage: envInt("DORKER_MOCK_TEMPORARY_PAGE", -1), QuotaPage: envInt("DORKER_MOCK_QUOTA_PAGE", -1), Delay: time.Duration(envInt("DORKER_MOCK_DELAY_MS", 0)) * time.Millisecond})
	all = append(all, candidate{"mock", true, mp})
	var out []scheduler.Runtime
	var statuses []providerStatus
	for _, x := range all {
		pc := c.Providers[x.name]
		enabled := pc.Enabled
		if explicit {
			enabled = chosen[x.name]
		}
		sel := enabled && x.configured
		state := x.p.Health().State
		if !enabled {
			state = provider.Disabled
		} else if !x.configured {
			state = provider.State("UNAVAILABLE")
		}
		var capabilities provider.Capabilities
		if capable, ok := x.p.(provider.Capable); ok {
			capabilities = capable.Capabilities()
		}
		statuses = append(statuses, providerStatus{Name: x.name, Enabled: enabled, Configured: x.configured, Selected: sel, State: state, Capabilities: capabilities})
		if x.name == "searxng" {
			for _, in := range sxClients {
				h := in.Health()
				statuses[len(statuses)-1].Instances = append(statuses[len(statuses)-1].Instances, instanceStatus{Name: in.Instance(), State: h.State, Health: h})
			}
		}
		if sel {
			rps, burst := pc.RequestsPerSecond, pc.Burst
			if x.name == "searxng" {
				rps = 1000000
				burst = 1
			}
			out = append(out, scheduler.NewRuntime(x.p, rps, burst))
		}
	}
	return out, statuses
}

type unconfiguredProvider struct{ name string }

func (p *unconfiguredProvider) Name() string { return p.name }
func (p *unconfiguredProvider) Health() provider.HealthStatus {
	return provider.HealthStatus{State: provider.Disabled}
}
func (p *unconfiguredProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{SupportsPagination: p.name == "searxng"}
}
func (p *unconfiguredProvider) Search(context.Context, provider.SearchRequest) (provider.SearchResponse, error) {
	return provider.SearchResponse{}, &provider.Error{Provider: p.name, Kind: provider.KindUnauthorized, Message: "not configured"}
}
func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return def
}
func loadCommon(args []string, errw io.Writer) (config.Config, string, error) {
	fs := flag.NewFlagSet("common", flag.ContinueOnError)
	fs.SetOutput(errw)
	path := fs.String("config", config.DefaultPath(), "YAML configuration path")
	db := fs.String("db", "", "SQLite database path override")
	if err := fs.Parse(args); err != nil {
		return config.Config{}, "", err
	}
	if fs.NArg() != 0 {
		return config.Config{}, "", fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	c, err := config.Load(*path)
	if err != nil {
		return c, "", err
	}
	if *db != "" {
		c.Database.Path = *db
	}
	return c, config.ExpandPath(c.Database.Path), nil
}
func runProviders(args []string, ioo IO) int {
	if len(args) > 0 && args[0] == "test" {
		return runProviderTest(args[1:], ioo)
	}
	c, _, err := loadCommon(args, ioo.Err)
	if err != nil {
		return fail(ioo.Err, 2, err)
	}
	_, ss := providers(c, httpclient.New(c.HTTP), nil)
	rows := make([]ui.ProviderRow, 0, len(ss))
	for _, s := range ss {
		row := ui.ProviderRow{Name: s.Name, State: string(s.State), Enabled: s.Enabled, Configured: s.Configured, PageSize: s.Capabilities.PageSizeMaximum, MaximumResults: s.Capabilities.MaximumAccessibleResults, Pagination: s.Capabilities.SupportsPagination}
		for _, in := range s.Instances {
			row.Instances = append(row.Instances, ui.ProviderInstance{Name: in.Name, State: string(in.State)})
		}
		rows = append(rows, row)
	}
	ui.New(ioo.Out, ui.Options{Color: streamColor(ioo, ui.ColorMode(c.UI.Color)), Interactive: ui.IsTerminal(ioo.Out) || ioo.Color == "always"}).Providers(rows)
	return 0
}

func runProviderTest(args []string, ioo IO) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fail(ioo.Err, 2, errors.New("providers test requires searxng, google-api, brave, or serpapi"))
	}
	name := args[0]
	if name == "mock" {
		return fail(ioo.Err, 2, errors.New("providers test is for real providers"))
	}
	fs := flag.NewFlagSet("providers test", flag.ContinueOnError)
	fs.SetOutput(ioo.Err)
	queryText := fs.String("q", "example.com", "single harmless test query")
	configPath := fs.String("config", config.DefaultPath(), "YAML configuration path")
	instance := fs.String("instance", "", "SearXNG instance name")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return fail(ioo.Err, 2, errors.New("unexpected arguments"))
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fail(ioo.Err, 2, err)
	}
	runtimes, statuses := providers(cfg, httpclient.New(cfg.HTTP), []string{name})
	known, configured := false, false
	for _, s := range statuses {
		if s.Name == name {
			known, configured = true, s.Configured
		}
	}
	if !known {
		return fail(ioo.Err, 2, fmt.Errorf("unknown provider %q", name))
	}
	if !configured || len(runtimes) != 1 {
		if name == "searxng" {
			return fail(ioo.Err, 3, errors.New("SearXNG isn't configured"))
		}
		return fail(ioo.Err, 3, fmt.Errorf("%s isn't configured or its credentials are missing", name))
	}
	if name == "searxng" && *instance != "" {
		cluster, ok := runtimes[0].Provider.(*searxng.Cluster)
		if !ok {
			return fail(ioo.Err, 3, errors.New("searxng unavailable"))
		}
		found := false
		for _, in := range cluster.Instances() {
			if in.Instance() == *instance {
				runtimes[0].Provider = in
				found = true
				break
			}
		}
		if !found {
			return fail(ioo.Err, 2, fmt.Errorf("unknown searxng instance %q", *instance))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.RequestTimeout)
	defer cancel()
	started := time.Now()
	resp, err := runtimes[0].Provider.Search(ctx, provider.SearchRequest{Query: *queryText, OriginalQuery: *queryText, PageSize: 1})
	latency := time.Since(started)
	if err != nil {
		ui.New(ioo.Out, ui.Options{Color: ui.ColorMode(cfg.UI.Color), Interactive: ui.IsTerminal(ioo.Out)}).ProviderTest(ui.ProviderTest{Name: name, State: "FAILED", Latency: latency})
		fmt.Fprintf(ioo.Err, "error: %s\n", err)
		return 1
	}
	result := ui.ProviderTest{Name: name, State: "READY", Latency: latency, Results: len(resp.Results)}
	if name == "searxng" {
		instanceName := *instance
		if instanceName == "" && len(resp.Results) > 0 {
			instanceName = resp.Results[0].ProviderInstance
		}
		if instanceName != "" {
			result.Instance = instanceName
		}
		responsive := map[string]bool{}
		for _, r := range resp.Results {
			for _, e := range r.SourceEngines {
				responsive[e] = true
			}
		}
		if len(resp.Diagnostics) > 0 || len(responsive) > 0 {
			names := make([]string, 0, len(responsive))
			for e := range responsive {
				names = append(names, e)
			}
			sort.Strings(names)
			for _, e := range names {
				result.Engines = append(result.Engines, ui.EngineStatus{Name: e, State: "responsive"})
			}
			for _, d := range resp.Diagnostics {
				result.Engines = append(result.Engines, ui.EngineStatus{Name: d.Engine, State: string(d.Classification), Message: d.Message})
			}
		}
	}
	ui.New(ioo.Out, ui.Options{Color: streamColor(ioo, ui.ColorMode(cfg.UI.Color)), Interactive: ui.IsTerminal(ioo.Out) || ioo.Color == "always"}).ProviderTest(result)
	return 0
}
func runJobs(args []string, ioo IO) int {
	_, path, err := loadCommon(args, ioo.Err)
	if err != nil {
		return fail(ioo.Err, 2, err)
	}
	st, err := store.Open(path)
	if err != nil {
		return fail(ioo.Err, 1, err)
	}
	defer st.Close()
	jobs, err := st.Jobs(context.Background())
	if err != nil {
		return fail(ioo.Err, 1, err)
	}
	rows := make([]ui.JobRow, 0, len(jobs))
	for _, j := range jobs {
		rows = append(rows, ui.JobRow{ID: j.ID, Status: j.Status, Queries: j.Queries, Unique: j.Unique, Started: j.CreatedAt})
	}
	ui.New(ioo.Out, ui.Options{Color: ui.ColorAuto, Interactive: ui.IsTerminal(ioo.Out)}).Jobs(rows)
	return 0
}
func runStats(args []string, ioo IO) int {
	job := ""
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		job = args[0]
		rest = args[1:]
	}
	_, path, err := loadCommon(rest, ioo.Err)
	if err != nil {
		return fail(ioo.Err, 2, err)
	}
	st, err := store.Open(path)
	if err != nil {
		return fail(ioo.Err, 1, err)
	}
	defer st.Close()
	if job == "" {
		js, e := st.Jobs(context.Background())
		if e != nil || len(js) == 0 {
			return fail(ioo.Err, 1, errors.New("no jobs found"))
		}
		job = js[0].ID
	}
	x, err := st.Stats(context.Background(), job)
	if err != nil {
		return fail(ioo.Err, 1, err)
	}
	fmt.Fprintf(ioo.Out, "Job: %s\nResults\n  raw: %d\n  accepted: %d\n  rejected: %d\n  unknown kept: %d\n  duplicates: %d\n  unique: %d\n  malformed: %d\n  provider errors: %d\n", job, x.Raw, x.Accepted, x.Rejected, x.UnknownKept, x.Duplicates, x.Unique, x.Malformed, x.Errors)
	if len(x.RejectionReasons) > 0 {
		fmt.Fprintln(ioo.Out, "Validation rejection reasons")
		names := make([]string, 0, len(x.RejectionReasons))
		for n := range x.RejectionReasons {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(ioo.Out, "  %-24s %d\n", n, x.RejectionReasons[n])
		}
	}
	names := make([]string, 0, len(x.ByProvider))
	for n := range x.ByProvider {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) > 0 {
		fmt.Fprintln(ioo.Out, "Provider discoveries:")
	}
	for _, n := range names {
		fmt.Fprintf(ioo.Out, "%-14s %d\n", n, x.ByProvider[n])
	}
	if len(x.BySourceEngine) > 0 {
		fmt.Fprintln(ioo.Out, "SearXNG source engines (discoveries):")
		names = names[:0]
		for n := range x.BySourceEngine {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(ioo.Out, "%-14s %d\n", n, x.BySourceEngine[n])
		}
	}
	if len(x.UpstreamDegradation) > 0 {
		fmt.Fprintln(ioo.Out, "SearXNG upstream degradation:")
		for _, u := range x.UpstreamDegradation {
			fmt.Fprintf(ioo.Out, "%-18s %-14s %d  %s\n", u.Engine, u.Classification, u.Count, u.LastMessage)
		}
	}
	return 0
}
func runExport(args []string, ioo IO) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fail(ioo.Err, 2, errors.New("export requires a job ID"))
	}
	job := args[0]
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(ioo.Err)
	out := fs.String("output", "", "destination file (- for stdout)")
	fs.StringVar(out, "o", "", "destination file (shorthand)")
	format := fs.String("format", "txt", "txt, jsonl, or csv")
	overwrite := fs.Bool("overwrite", false, "replace existing output atomically")
	collapse := fs.String("collapse", "", "collapse output: none, host, or domain")
	cfgPath := fs.String("config", config.DefaultPath(), "YAML configuration path")
	db := fs.String("db", "", "SQLite database path override")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *out == "" {
		return fail(ioo.Err, 2, errors.New("export requires --output"))
	}
	c, err := config.Load(*cfgPath)
	if err != nil {
		return fail(ioo.Err, 2, err)
	}
	if *db != "" {
		c.Database.Path = *db
	}
	if *collapse == "" {
		*collapse = c.Output.Collapse
	}
	mode, err := exporter.ParseCollapseMode(*collapse)
	if err != nil {
		return fail(ioo.Err, 2, err)
	}
	st, err := store.Open(config.ExpandPath(c.Database.Path))
	if err != nil {
		return fail(ioo.Err, 1, err)
	}
	defer st.Close()
	if _, err = exporter.WriteCollapsed(context.Background(), st, job, *format, *out, *overwrite, ioo.Out, mode); err != nil {
		return fail(ioo.Err, 1, err)
	}
	return 0
}

func statsCount(n int64) int64 { return n }
func runConfig(args []string, ioo IO) int {
	if len(args) > 0 && args[0] == "init" {
		return runConfigInit(args[1:], ioo)
	}
	if len(args) > 0 && args[0] == "check" {
		return runConfigCheck(args[1:], ioo)
	}
	if len(args) != 0 {
		return fail(ioo.Err, 2, errors.New("config accepts init or check"))
	}
	b, err := yaml.Marshal(config.Default())
	if err != nil {
		return fail(ioo.Err, 1, err)
	}
	fmt.Fprintf(ioo.Out, "# Default path: %s\n%s", config.DefaultPath(), b)
	return 0
}

func runConfigInit(args []string, ioo IO) int {
	fs := flag.NewFlagSet("config init", flag.ContinueOnError)
	fs.SetOutput(ioo.Err)
	path := fs.String("config", config.DefaultPath(), "configuration path")
	force := fs.Bool("force", false, "replace an existing configuration")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return fail(ioo.Err, 2, errors.New("unexpected arguments"))
	}
	return writeStarterConfig(*path, *force, ioo)
}

func writeStarterConfig(path string, force bool, ioo IO) int {
	path = config.ExpandPath(path)
	if _, err := os.Stat(path); err == nil && !force {
		return fail(ioo.Err, 2, fmt.Errorf("config already exists\n%s", path))
	} else if err != nil && !os.IsNotExist(err) {
		return fail(ioo.Err, 1, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fail(ioo.Err, 1, err)
	}
	b, err := yaml.Marshal(config.Default())
	if err != nil {
		return fail(ioo.Err, 1, err)
	}
	if err = os.WriteFile(path, b, 0600); err != nil {
		return fail(ioo.Err, 1, err)
	}
	fmt.Fprintf(ioo.Out, "Config created\n  %s\n", path)
	return 0
}

func runDoctor(args []string, ioo IO) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(ioo.Err)
	path := fs.String("config", config.DefaultPath(), "configuration path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return fail(ioo.Err, 2, errors.New("unexpected arguments"))
	}
	fmt.Fprintln(ioo.Out, "Dorker Doctor")
	fmt.Fprintln(ioo.Out)
	fmt.Fprintf(ioo.Out, "  %-14s Ready · %s/%s\n", "Platform", runtime.GOOS, runtime.GOARCH)
	if v, err := exec.Command("go", "version").Output(); err == nil {
		fields := strings.Fields(string(v))
		version := "available"
		if len(fields) >= 3 {
			version = strings.TrimPrefix(fields[2], "go")
		}
		fmt.Fprintf(ioo.Out, "  %-14s Ready · %s\n", "Go", version)
	} else {
		fmt.Fprintf(ioo.Out, "  %-14s Not found · only needed to build/install\n", "Go")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintf(ioo.Out, "  %-14s Problem · %s\n", "Config", err)
		return 1
	}
	if _, err = os.Stat(config.ExpandPath(*path)); err == nil {
		fmt.Fprintf(ioo.Out, "  %-14s Ready\n", "Config")
	} else {
		fmt.Fprintf(ioo.Out, "  %-14s Not created · run dorker setup\n", "Config")
	}
	if err = os.MkdirAll(filepath.Dir(config.ExpandPath(cfg.Database.Path)), 0700); err == nil {
		fmt.Fprintf(ioo.Out, "  %-14s Ready\n", "Database")
	} else {
		fmt.Fprintf(ioo.Out, "  %-14s Problem\n", "Database")
	}
	if err = os.MkdirAll(config.ExpandPath(cfg.Output.Directory), 0755); err == nil {
		fmt.Fprintf(ioo.Out, "  %-14s Ready · %s\n", "Output dir", config.ExpandPath(cfg.Output.Directory))
	} else {
		fmt.Fprintf(ioo.Out, "  %-14s Problem\n", "Output dir")
	}
	for _, name := range []string{"podman", "docker"} {
		if _, e := exec.LookPath(name); e == nil {
			fmt.Fprintf(ioo.Out, "  %-14s Ready\n", strings.Title(name))
		} else {
			fmt.Fprintf(ioo.Out, "  %-14s Not found · optional\n", strings.Title(name))
		}
	}
	_, statuses := providers(cfg, httpclient.New(cfg.HTTP), nil)
	available := 0
	for _, s := range statuses {
		if s.Configured && s.Enabled {
			available++
		}
	}
	fmt.Fprintf(ioo.Out, "  %-14s %d available\n", "Providers", available)
	if available == 0 {
		fmt.Fprintln(ioo.Out, "\nNo search provider is configured. Run `dorker setup`.")
		return 1
	}
	fmt.Fprintln(ioo.Out, "\nEverything looks good.")
	return 0
}

func runSetup(args []string, ioo IO) int {
	if len(args) > 0 && args[0] == "searxng" {
		return setupSearXNG(args[1:], ioo)
	}
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(ioo.Err)
	path := fs.String("config", config.DefaultPath(), "configuration path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return fail(ioo.Err, 2, errors.New("unexpected arguments"))
	}
	fmt.Fprintln(ioo.Out, "Dorker Setup")
	if _, err := os.Stat(config.ExpandPath(*path)); os.IsNotExist(err) {
		if code := writeStarterConfig(*path, false, ioo); code != 0 {
			return code
		}
	} else {
		fmt.Fprintf(ioo.Out, "\nConfig\n  Ready · %s\n", config.ExpandPath(*path))
	}
	fmt.Fprintln(ioo.Out, "\nSearch provider\n  Local SearXNG: dorker setup searxng\n  Existing SearXNG: set SEARXNG_URL or edit the generated config\n  Brave: set BRAVE_API_KEY\n  Google: set GOOGLE_API_KEY and GOOGLE_CSE_ID\n  SerpAPI: set SERPAPI_API_KEY\n\nCredentials are read from the environment and are never printed.")
	return 0
}

const searxImage = "ghcr.io/searxng/searxng:2026.10.4-d48c4b555@sha256:b0497f6e93b5e98b1a15c66b333afed176266af34f479a46ed87514f4a441eda"

func setupSearXNG(args []string, ioo IO) int {
	fs := flag.NewFlagSet("setup searxng", flag.ContinueOnError)
	fs.SetOutput(ioo.Err)
	yes := fs.Bool("yes", false, "confirm container creation")
	configPath := fs.String("config", config.DefaultPath(), "configuration path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	runtimeName := ""
	for _, n := range []string{"podman", "docker"} {
		if _, e := exec.LookPath(n); e == nil {
			runtimeName = n
			break
		}
	}
	if runtimeName == "" {
		fmt.Fprintln(ioo.Err, "Podman isn't installed (Docker is also supported).\n\nArch: sudo pacman -S podman\nDebian/Kali/Ubuntu: sudo apt install podman\nFedora: sudo dnf install podman\n\nRun `dorker setup searxng` again.")
		return 3
	}
	if !*yes {
		fmt.Fprintf(ioo.Out, "This creates dorker-searxng, binds only 127.0.0.1:8080, and uses %s.\nRun again with --yes to confirm.\n", searxImage)
		return 0
	}
	base := filepath.Join(filepath.Dir(config.ExpandPath(config.DefaultPath())), "searxng")
	if err := os.MkdirAll(base, 0700); err != nil {
		return fail(ioo.Err, 1, err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return fail(ioo.Err, 1, err)
	}
	settings := fmt.Sprintf("use_default_settings: true\nserver:\n  bind_address: 0.0.0.0\n  port: 8080\n  secret_key: %s\nsearch:\n  formats:\n    - html\n    - json\n", hex.EncodeToString(secret))
	settingsPath := filepath.Join(base, "settings.yml")
	if err := os.WriteFile(settingsPath, []byte(settings), 0600); err != nil {
		return fail(ioo.Err, 1, err)
	}
	mount := settingsPath + ":/etc/searxng/settings.yml:ro"
	if runtimeName == "podman" {
		mount += ",Z"
	}
	cmd := exec.Command(runtimeName, "run", "-d", "--name", "dorker-searxng", "--restart", "unless-stopped", "-p", "127.0.0.1:8080:8080", "-v", mount, searxImage)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fail(ioo.Err, 1, fmt.Errorf("start SearXNG: %s", strings.TrimSpace(string(out))))
	}
	ready := false
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, requestErr := client.Get("http://127.0.0.1:8080/search?q=dorker&format=json")
		if requestErr == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !ready {
		return fail(ioo.Err, 1, errors.New("SearXNG started but did not become ready within 30 seconds"))
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fail(ioo.Err, 1, err)
	}
	pc := cfg.Providers["searxng"]
	pc.Enabled, pc.Strategy = true, "balanced"
	pc.Instances = []config.SearXNGInstanceConfig{{Name: "local", URL: "http://127.0.0.1:8080", RequestsPerSecond: 1, Burst: 1}}
	cfg.Providers["searxng"] = pc
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return fail(ioo.Err, 1, err)
	}
	path := config.ExpandPath(*configPath)
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fail(ioo.Err, 1, err)
	}
	if err = os.WriteFile(path, b, 0600); err != nil {
		return fail(ioo.Err, 1, err)
	}
	fmt.Fprintf(ioo.Out, "SearXNG is ready.\n\n  URL      http://127.0.0.1:8080\n  Runtime  %s\n  Config   %s\n", runtimeName, settingsPath)
	return 0
}

func runConfigCheck(args []string, ioo IO) int {
	c, path, err := loadCommon(args, ioo.Err)
	if err != nil {
		return fail(ioo.Err, 2, err)
	}
	fmt.Fprintln(ioo.Out, "[OK] configuration valid")
	fmt.Fprintf(ioo.Out, "[OK] validation mode %s\n", c.Validation.Mode)
	fmt.Fprintf(ioo.Out, "[OK] UI banner=%s color=%s\n", yesno(c.UI.Banner), c.UI.Color)
	st, dbErr := store.Open(path)
	if dbErr != nil {
		fmt.Fprintln(ioo.Out, "[FAIL] database is not writable")
	} else {
		_ = st.Close()
		fmt.Fprintln(ioo.Out, "[OK] database writable")
	}
	_, statuses := providers(c, httpclient.New(c.HTTP), nil)
	for _, s := range statuses {
		if !s.Enabled {
			fmt.Fprintf(ioo.Out, "[INFO] %s disabled\n", s.Name)
			continue
		}
		if s.Name == "searxng" && s.Configured {
			for _, in := range s.Instances {
				fmt.Fprintf(ioo.Out, "[OK] searxng %s configured\n[OK] searxng %s URL valid\n", in.Name, in.Name)
			}
			continue
		}
		if s.Configured {
			fmt.Fprintf(ioo.Out, "[OK] %s credentials present\n", s.Name)
		} else {
			fmt.Fprintf(ioo.Out, "[INFO] %s configuration or credentials missing\n", s.Name)
		}
	}
	outputDir := config.ExpandPath(c.Output.Directory)
	if e := os.MkdirAll(outputDir, 0755); e != nil {
		fmt.Fprintln(ioo.Out, "[FAIL] output directory is not writable")
		if dbErr == nil {
			return 1
		}
	} else if f, e := os.CreateTemp(outputDir, ".dorker-write-check-"); e != nil {
		fmt.Fprintln(ioo.Out, "[FAIL] output directory is not writable")
		if dbErr == nil {
			return 1
		}
	} else {
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
		fmt.Fprintln(ioo.Out, "[OK] output directory usable")
	}
	if dbErr != nil {
		return 1
	}
	return 0
}

func runQuery(args []string, ioo IO) int {
	if len(args) == 0 {
		return fail(ioo.Err, 2, errors.New("query requires explain or validate"))
	}
	switch args[0] {
	case "explain":
		if len(args) != 2 {
			return fail(ioo.Err, 2, errors.New("query explain requires exactly one quoted query argument"))
		}
		c, err := query.ExtractConstraints(args[1])
		if err != nil {
			return fail(ioo.Err, 2, err)
		}
		explanation := ui.QueryExplanation{Query: args[1], ProviderText: strings.Join(c.ProviderOnly, " ")}
		for _, s := range c.Sites {
			explanation.Local = append(explanation.Local, ui.Constraint{Name: "site", Value: s.String()})
		}
		for _, s := range c.ExcludedSites {
			explanation.Local = append(explanation.Local, ui.Constraint{Name: "exclude site", Value: s.String()})
		}
		for _, x := range c.Extensions {
			explanation.Local = append(explanation.Local, ui.Constraint{Name: "extension", Value: x})
		}
		for _, x := range c.ExcludedExts {
			explanation.Local = append(explanation.Local, ui.Constraint{Name: "exclude ext", Value: x})
		}
		for _, x := range c.URLContains {
			explanation.Local = append(explanation.Local, ui.Constraint{Name: "URL contains", Value: x})
		}
		for _, x := range c.URLExcludes {
			explanation.Local = append(explanation.Local, ui.Constraint{Name: "URL excludes", Value: x})
		}
		ui.New(ioo.Out, ui.Options{Color: streamColor(ioo, ui.ColorAuto), Interactive: ui.IsTerminal(ioo.Out) || ioo.Color == "always"}).QueryExplain(explanation)
		return 0
	case "validate":
		fs := flag.NewFlagSet("query validate", flag.ContinueOnError)
		fs.SetOutput(ioo.Err)
		q := fs.String("q", "", "query")
		u := fs.String("u", "", "result URL")
		modeText := fs.String("validation", "conservative", "off, conservative, or strict")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *q == "" || *u == "" {
			return fail(ioo.Err, 2, errors.New("query validate requires -q and -u"))
		}
		mode, err := query.ParseValidationMode(*modeText)
		if err != nil {
			return fail(ioo.Err, 2, err)
		}
		normalized, _, err := normalize.New(normalize.Options{}).Normalize(*u)
		if err != nil {
			return fail(ioo.Err, 2, err)
		}
		c, err := query.ExtractConstraints(*q)
		if err != nil {
			return fail(ioo.Err, 2, err)
		}
		r := c.Evaluate(normalized, mode)
		result := ui.QueryValidation{Query: *q, URL: normalized, Result: validationResultLabel(r.State)}
		for _, e := range r.Evaluations {
			result.Checks = append(result.Checks, ui.ValidationCheck{Constraint: e.Constraint, State: string(e.State), Reason: string(e.Reason)})
		}
		result.Reason = validationReason(r)
		ui.New(ioo.Out, ui.Options{Color: streamColor(ioo, ui.ColorAuto), Interactive: ui.IsTerminal(ioo.Out) || ioo.Color == "always"}).QueryValidate(result)
		return 0
	default:
		return fail(ioo.Err, 2, fmt.Errorf("unknown query command %q", args[0]))
	}
}
func newID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
func yesno(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
func validationResultLabel(state query.MatchState) string {
	switch state {
	case query.Match:
		return "MATCH"
	case query.Mismatch:
		return "MISMATCH"
	default:
		return "UNKNOWN"
	}
}

func validationReason(result query.ValidationResult) string {
	for _, reason := range result.Reasons {
		switch reason {
		case query.ReasonSiteMismatch:
			return "The URL isn't on the expected site."
		case query.ReasonExcludedSite:
			return "The URL is on an excluded site."
		case query.ReasonExtensionMismatch:
			return "The URL has a different file extension."
		case query.ReasonExtensionUnknown:
			return "The URL doesn't expose enough information to verify the file type."
		case query.ReasonExcludedExtension:
			return "The URL has an excluded file extension."
		case query.ReasonInURLMismatch:
			return "The URL doesn't contain the expected text."
		case query.ReasonExcludedInURL:
			return "The URL contains excluded text."
		}
	}
	return ""
}

func fail(w io.Writer, code int, err error) int {
	message := strings.ReplaceAll(err.Error(), "\nvalid modes:", "\nexpected:")
	message = strings.ReplaceAll(message, "\n", "\n       ")
	ui.PrintError(w, message)
	return code
}

func renderer(ioo IO, c config.Config, quiet, noBanner bool) *ui.Renderer {
	interactive := interactiveIO(ioo)
	return ui.New(ioo.Err, ui.Options{Banner: c.UI.Banner, Color: streamColor(ioo, ui.ColorMode(c.UI.Color)), Interactive: interactive || ioo.Color == "always", Quiet: quiet, NoBanner: noBanner})
}
func streamColor(ioo IO, fallback ui.ColorMode) ui.ColorMode {
	if ioo.Color != "" {
		return ui.ColorMode(ioo.Color)
	}
	return fallback
}
func interactiveIO(ioo IO) bool {
	interactive := ui.IsTerminal(ioo.Out) && ui.IsTerminal(ioo.Err)
	if ioo.Interactive != nil {
		interactive = *ioo.Interactive
	}
	return interactive
}

func startProgress(ctx context.Context, st *store.Store, job string, w io.Writer, col *collector.Collector, quiet bool) func() {
	if quiet {
		return func() {}
	}
	f, ok := w.(*os.File)
	if !ok {
		return func() {}
	}
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return func() {}
	}
	pctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	wrote := make(chan bool, 1)
	go func() {
		defer close(done)
		shown, width := false, 0
		var lastUnique, lastRejected, lastDuplicates int64 = -1, -1, -1
		write := func(unique, rejected, duplicates int64) {
			if unique == lastUnique && rejected == lastRejected && duplicates == lastDuplicates {
				return
			}
			line := progressLine(unique, rejected, duplicates)
			padding := ""
			if len(line) < width {
				padding = strings.Repeat(" ", width-len(line))
			}
			fmt.Fprintf(w, "\r%s%s", line, padding)
			shown = true
			width = len(line)
			lastUnique, lastRejected, lastDuplicates = unique, rejected, duplicates
		}
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		defer func() { wrote <- shown }()
		for {
			select {
			case <-pctx.Done():
				return
			case <-ticker.C:
				s, e := st.Stats(pctx, job)
				if e == nil && (s.Unique > 0 || col.Rejected.Load() > 0 || col.Duplicates.Load() > 0) {
					write(s.Unique, col.Rejected.Load(), col.Duplicates.Load())
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
		if <-wrote {
			fmt.Fprintln(w)
		}
	}
}

func progressLine(unique, rejected, duplicates int64) string {
	line := fmt.Sprintf("Searching... %d unique", unique)
	if rejected > 0 {
		line += fmt.Sprintf(" · %d rejected", rejected)
	}
	if duplicates > 0 {
		line += fmt.Sprintf(" · %d duplicates", duplicates)
	}
	return line
}
