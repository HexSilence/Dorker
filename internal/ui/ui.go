package ui

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/HexSilence/dorker/internal/model"
)

type ColorMode string

const (
	ColorAuto   ColorMode = "auto"
	ColorAlways ColorMode = "always"
	ColorNever  ColorMode = "never"
)

type Options struct {
	Banner                       bool
	Color                        ColorMode
	Interactive, Quiet, NoBanner bool
}
type Renderer struct {
	w     io.Writer
	opts  Options
	color bool
}

const (
	bold       = "\x1b[1m"
	cyan       = "\x1b[36m"
	brightCyan = "\x1b[1;96m"
	green      = "\x1b[1;92m"
	yellow     = "\x1b[1;93m"
	red        = "\x1b[1;91m"
	dim        = "\x1b[2m"
	dimCyan    = "\x1b[2;36m"
	white      = "\x1b[1;97m"
	reset      = "\x1b[0m"
)

func New(w io.Writer, o Options) *Renderer {
	_, noColor := os.LookupEnv("NO_COLOR")
	enabled := o.Color == ColorAlways || (o.Color == ColorAuto && o.Interactive && !noColor)
	if o.Color == ColorNever || noColor {
		enabled = false
	}
	return &Renderer{w: w, opts: o, color: enabled}
}
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
func (r *Renderer) Enabled() bool { return r.opts.Interactive && !r.opts.Quiet }
func (r *Renderer) paint(code, text string) string {
	if !r.color {
		return text
	}
	return code + text + reset
}
func (r *Renderer) Brand(text string) string   { return r.paint(brightCyan, text) }
func (r *Renderer) Heading(text string) string { return r.paint(brightCyan, text) }
func (r *Renderer) Command(text string) string { return r.paint("\x1b[1;36m", text) }
func (r *Renderer) Flag(text string) string    { return r.paint(yellow, text) }
func (r *Renderer) Muted(text string) string   { return r.paint(dim, text) }
func (r *Renderer) Border(text string) string  { return r.paint(dimCyan, text) }
func (r *Renderer) Value(text string) string   { return r.paint(white, text) }
func (r *Renderer) Error(text string) string   { return r.paint(red, text) }
func (r *Renderer) Warning(text string) string { return r.paint(yellow, text) }
func PrintError(w io.Writer, message string) {
	r := New(w, Options{Color: ColorAuto, Interactive: IsTerminal(w)})
	fmt.Fprintln(w, r.Error("error:"), message)
}
func PrintWarning(w io.Writer, message string) {
	r := New(w, Options{Color: ColorAuto, Interactive: IsTerminal(w)})
	fmt.Fprintln(w, r.Warning("warning:"), message)
}
func (r *Renderer) heading(text string) string { return r.Heading(text) }

func (r *Renderer) Banner() {
	if !r.Enabled() || !r.opts.Banner || r.opts.NoBanner {
		return
	}
	const width = 46
	lines := []string{"", "D O R K E R", "Advanced Search Intelligence", "& Reconnaissance Engine", "", "Developed & Engineered by", "Silence", "", "Built for Precision • Driven by Research", "© 2026 HexSilence", "Apache License 2.0"}
	fmt.Fprintln(r.w, r.Border("┌"+strings.Repeat("─", width)+"┐"))
	for _, line := range lines {
		rendered := center(line, width)
		if line == "D O R K E R" {
			rendered = r.Brand(rendered)
		} else if line == "Silence" {
			rendered = r.Command(rendered)
		} else if line == "Developed & Engineered by" || line == "Built for Precision • Driven by Research" || line == "© 2026 HexSilence" || line == "Apache License 2.0" {
			rendered = r.Muted(rendered)
		}
		fmt.Fprintf(r.w, "%s%s%s\n", r.Border("│"), rendered, r.Border("│"))
	}
	fmt.Fprintln(r.w, r.Border("└"+strings.Repeat("─", width)+"┘"))
	fmt.Fprintln(r.w)
}
func center(s string, width int) string {
	n := len([]rune(s))
	if n >= width {
		return s
	}
	left := (width - n) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", width-n-left)
}

func (r *Renderer) Startup() {
	if r.opts.Quiet {
		return
	}
	fmt.Fprintln(r.w, r.Heading("Quick start"))
	for _, x := range []string{"dorker setup", "dorker doctor", "dorker search -q 'site:example.com'"} {
		fmt.Fprintln(r.w, "  "+r.commandLine(x))
	}
	fmt.Fprintln(r.w, "\n"+r.Heading("Commands"))
	r.rows([]Row{{"search", "Find and collect URLs"}, {"discover", "Recursively expand in-scope hosts"}, {"ghdb", "Browse ready-made dorks"}, {"setup", "Create a working configuration"}, {"doctor", "Inspect the local environment"}, {"providers", "Check provider health"}, {"query", "Explain or validate a query"}, {"jobs", "Browse saved jobs"}, {"resume", "Continue a job"}, {"update", "Update to the latest stable release"}})
	fmt.Fprintln(r.w, "\n"+r.Heading("More"))
	fmt.Fprintln(r.w, "  "+r.Command("dorker --help"))
}

type Row struct{ Name, Description string }

func (r *Renderer) rows(rows []Row) {
	width := 0
	for _, row := range rows {
		if n := len(row.Name); n > width && n <= 24 {
			width = n
		}
	}
	for _, row := range rows {
		fmt.Fprintf(r.w, "  %s   %s\n", r.Command(fmt.Sprintf("%-*s", width, row.Name)), row.Description)
	}
}
func (r *Renderer) commandLine(s string) string {
	fields := strings.Fields(s)
	for i, f := range fields {
		if i < 2 {
			fields[i] = r.Command(f)
		} else if strings.HasPrefix(f, "-") {
			fields[i] = r.Flag(f)
		}
	}
	return strings.Join(fields, " ")
}

func Help(w io.Writer) {
	New(w, Options{Color: ColorAuto, Interactive: IsTerminal(w)}).Help()
}
func (r *Renderer) Help() {
	fmt.Fprint(r.w, `Dorker searches multiple sources, filters results,
removes duplicates, and keeps track of where each URL came from.
`)
	fmt.Fprintln(r.w, r.Heading("USAGE"))
	fmt.Fprintln(r.w, "  "+r.commandLine("dorker <command> [options]"))
	fmt.Fprintln(r.w, "\n"+r.Heading("COMMANDS"))
	r.rows([]Row{{"search", "Find and collect URLs"}, {"discover", "Recursively expand in-scope hosts"}, {"ghdb", "Browse ready-made dorks"}, {"setup", "Create a working configuration"}, {"doctor", "Inspect the local environment"}, {"providers", "Check provider health"}, {"query", "Explain or validate queries"}, {"jobs", "Browse saved jobs"}, {"resume", "Continue a job"}})
	fmt.Fprintln(r.w, "\n"+r.Heading("OTHER COMMANDS"))
	r.rows([]Row{{"stats", "Show job statistics"}, {"export", "Export a saved job"}, {"config", "Show or check configuration"}, {"version", "Show version info"}, {"update", "Update to the latest stable release"}})
	fmt.Fprintln(r.w, "\n"+r.Heading("EXAMPLES"))
	for _, x := range []string{"dorker search -q 'site:example.com'", "dorker discover -q 'site:example.com'", "dorker ghdb", "dorker search -q 'site:example.com ext:pdf' > results.txt", "dorker providers test searxng"} {
		fmt.Fprintln(r.w, "  "+r.commandLine(x))
	}
	fmt.Fprintln(r.w, "\nRun "+r.Command("dorker <command> --help")+" for command-specific options.")
}

func SearchHelp(w io.Writer, defaultConfig string) {
	SearchHelpColor(w, defaultConfig, ColorAuto, IsTerminal(w))
}
func SearchHelpColor(w io.Writer, defaultConfig string, color ColorMode, interactive bool) {
	r := New(w, Options{Color: color, Interactive: interactive})
	text := fmt.Sprintf(`Search for URLs, filter them, and remove duplicates.

Usage:
  dorker search [options]
  dorker [search options]

Input:
  -q QUERY                    Search query; repeatable.
  -f FILE                     Read queries from a file; repeatable.
  --query-mode MODE           Query planning mode: exact or conservative.
  --max-query-variants N      Maximum variants per query.

Providers:
  --provider NAME             Provider to use; repeatable.
  --searxng-engines LIST      Comma-separated SearXNG engines.
  --searxng-strategy MODE     SearXNG strategy: balanced or coverage.

Limits:
  --max-results N             Maximum unique results for the job; 0 is unlimited.
  --max-results-per-query N   Maximum unique results per query; 0 is unlimited.
  --max-pages N               Maximum pages per provider and query; 0 is unlimited.
  --page-size N               Results requested per provider page.
  --workers N                 Number of workers.

Validation:
  --validation MODE           Validation mode: off, conservative, or strict.
  --include-domain DOMAIN     Include a domain and its subdomains; repeatable.
  --exclude-domain DOMAIN     Exclude a domain and its subdomains; repeatable.
  --include-regex REGEX       Include normalized URLs matching this RE2 pattern.
  --exclude-regex REGEX       Exclude normalized URLs matching this RE2 pattern.
  --keep-tracking-params      Keep common tracking parameters.

Output:
  --format FORMAT             Output format: txt, jsonl, or csv.
  -o, --output FILE           Write results to a file; '-' writes to stdout.
  --output-dir DIRECTORY      Automatic output root (config default).
  --overwrite                 Replace an existing output file.
  --collapse MODE             Collapse output: none, host, or domain.
  --stream                    Stream new unique URLs to stdout.

Recovery:
  --wait-for-recovery         Wait for providers in cooldown.
  --max-recovery-wait DURATION
                              Maximum cooldown wait per provider and query.
  --job-id ID                 Job ID to continue with --resume.
  --resume                    Continue the latest interrupted job.

Display:
  --quiet                     Hide banner, progress, and info messages.
  --no-banner                 Hide the banner.
  --log-level LEVEL           Log level: debug, info, warn, error, or off.
  --dry-run                   Show the plan without making requests or writes.
  --db FILE                   Use a different SQLite database.
  --config FILE               Use a different config file (default %s).

Examples:
  dorker search -q 'site:example.com'
  dorker search -q 'site:example.com ext:pdf' --provider searxng
  dorker search -f queries.txt --format jsonl -o results.jsonl
  dorker search -q 'site:.ye' --collapse host
  dorker search -q 'site:.ye' --collapse domain > domains.txt
  dorker search -q 'site:example.com' --stream | httpx
`, defaultConfig)
	for _, heading := range []string{"Usage", "Input", "Providers", "Limits", "Validation", "Output", "Recovery", "Display", "Examples"} {
		text = strings.Replace(text, heading+":\n", r.Heading(strings.ToUpper(heading))+"\n", 1)
	}
	fmt.Fprint(w, text)
}

func CommandHelp(w io.Writer, command, _ string) bool {
	var text string
	switch command {
	case "discover":
		text = `Recursively search newly discovered in-scope hosts.

Usage:
  dorker discover -q 'site:example.com' [options]

Limits:
  --max-depth N              Maximum recursive expansion depth.
  --max-hosts N              Maximum unique hosts.
  --max-queries N            Maximum provider queries.
  --max-results N            Maximum unique URLs.
  --max-results-per-query N  Maximum URLs per expansion query.
  --max-pages N              Maximum pages per provider/query.
  --max-duration DURATION    Maximum elapsed time.

Saturation:
  --saturation-threshold RATIO
  --saturation-consecutive-depths N

Output:
  --hosts-only               Emit only hosts when redirected or piped.
  --roots-only               Emit only registrable domains.
  -o, --output DIRECTORY     Write discovery artifacts to a directory.
  --output-dir DIRECTORY     Automatic output root.

Other:
  --provider NAME            Provider to use; repeatable.
  --page-size N              Results requested per provider page.
  --workers N                Number of bounded workers.
  --validation MODE          off, conservative, or strict.
  --log-level LEVEL          debug, info, warn, error, or off.
  --quiet                    Hide progress and informational messages.
  --no-banner                Hide the interactive banner.
  --dry-run                  Show scope, limits, and strategy only.
  --db FILE                  Use a different SQLite database.
  --config FILE              Use a different configuration file.
`
	case "providers":
		text = `Check configured search providers.

Usage:
  dorker providers [--config FILE]
  dorker providers test NAME [options]

Options:
  --config FILE      Use a different config file.
  --db FILE          Use a different SQLite database.

Examples:
  dorker providers
  dorker providers test searxng
`
	case "providers test":
		text = `Make one small request to a provider.

Usage:
  dorker providers test NAME [options]

Options:
  -q QUERY           Test query (default example.com).
  --instance NAME    Test one SearXNG instance.
  --config FILE      Use a different config file.
`
	case "query":
		text = `Explain a query or check a URL against it.

Usage:
  dorker query explain 'QUERY'
  dorker query validate -q 'QUERY' -u URL [--validation MODE]
`
	case "ghdb":
		text = `Browse a local catalog of ready-made search dorks.

Usage:
  dorker ghdb
  dorker ghdb list [--category CATEGORY]
  dorker ghdb show ID
  dorker ghdb find TERM
  dorker ghdb run ID [--domain DOMAIN] [search options]

With no subcommand, GHDB opens an interactive browser.
The run command passes the selected query to the normal search pipeline.
`
	case "jobs":
		text = `View saved jobs.

Usage:
  dorker jobs [--db FILE] [--config FILE]
`
	case "stats":
		text = `Show detailed statistics for a saved job.

Usage:
  dorker stats [JOB-ID] [--db FILE] [--config FILE]
`
	case "export":
		text = `Export results from a saved job.

Usage:
	 dorker export JOB-ID --format FORMAT -o FILE [--overwrite] [--collapse MODE]

Options:
  --collapse MODE   Collapse output: none, host, or domain.
`
	case "config":
		text = `Show or check Dorker configuration.

Usage:
  dorker config
  dorker config init [--config FILE] [--force]
  dorker config check [--db FILE] [--config FILE]
`
	case "doctor":
		text = "Inspect configuration, paths, providers, and optional runtimes.\n\nUsage:\n  dorker doctor [--config FILE]\n"
	case "setup":
		text = "Create a starter configuration and show provider setup choices.\n\nUsage:\n  dorker setup [--config FILE]\n  dorker setup searxng [--config FILE] [--yes]\n"
	case "setup searxng":
		text = `Preview or create a private local SearXNG container.

Usage:
  dorker setup searxng [--config FILE] [--yes]

Options:
  --config FILE   Use a different configuration file.
  --yes           Confirm container creation or startup.

Without --yes, Dorker only previews the action. Podman is preferred;
Docker is used when Podman is unavailable.
`
	case "resume":
		text = `Continue a saved search or discovery job.

Usage:
  dorker resume [JOB-ID] [options]

With no job ID, Dorker continues the latest resumable search job.
Discovery jobs restore their saved frontier, limits, and owned output directory.
`
	case "version":
		text = `Show version info.

Usage:
  dorker version
  dorker --version
`
	case "update":
		text = `Check for and install the latest stable Dorker release.

Usage:
  dorker update

An available update requires interactive confirmation. Unknown or unsupported
installation methods receive an exact safe installation command.
`
	default:
		return false
	}
	fmt.Fprint(w, text)
	return true
}

type GHDBEntry struct {
	ID                                  int
	Title, Description, Category, Query string
}

func (r *Renderer) GHDBList(entries []GHDBEntry, choose bool) {
	fmt.Fprintln(r.w, r.Heading("GHDB"))
	if choose {
		fmt.Fprintln(r.w, r.Heading("Choose a dork"))
	}
	for _, e := range entries {
		fmt.Fprintf(r.w, "\n  %s %s\n     %s\n", r.Command(fmt.Sprintf("%d.", e.ID)), r.Value(e.Title), r.Muted(e.Description))
	}
}

func (r *Renderer) GHDBShow(e GHDBEntry, details bool) {
	fmt.Fprintln(r.w, r.Heading(e.Title))
	if details {
		fmt.Fprintf(r.w, "\n  %s  %d\n  %s  %s\n", r.Muted(fmt.Sprintf("%-10s", "ID")), e.ID, r.Muted(fmt.Sprintf("%-10s", "Category")), e.Category)
	}
	fmt.Fprintf(r.w, "\n%s\n  %s\n\n%s\n", r.Heading("Query"), e.Query, e.Description)
}

func (r *Renderer) GHDBReady(title, query string) {
	fmt.Fprintln(r.w, r.Heading("Ready"))
	fmt.Fprintf(r.w, "\n%s\n  %s\n\n%s\n  %s\n", r.Heading("Dork"), r.Value(title), r.Heading("Query"), query)
}

type SearchStart struct {
	Query, Provider, Validation string
	Queries, Limit              int
}

func (r *Renderer) SearchStart(x SearchStart) {
	if !r.Enabled() {
		return
	}
	fmt.Fprintln(r.w, r.Brand("DORKER / SEARCH"))
	fmt.Fprintln(r.w)
	if x.Queries > 1 {
		fmt.Fprintf(r.w, "  %s  %s\n", r.Muted(fmt.Sprintf("%-10s", "Queries")), r.Value(fmt.Sprint(x.Queries)))
	} else {
		fmt.Fprintf(r.w, "  %s  %s\n", r.Muted(fmt.Sprintf("%-10s", "Query")), r.Value(x.Query))
	}
	label := "Provider"
	if strings.Contains(x.Provider, ",") {
		label = "Providers"
	}
	fmt.Fprintf(r.w, "  %s  %s\n", r.Muted(fmt.Sprintf("%-10s", label)), r.Value(strings.ReplaceAll(x.Provider, ",", ", ")))
	fmt.Fprintf(r.w, "  %s  %s\n", r.Muted(fmt.Sprintf("%-10s", "Validation")), r.Value(x.Validation))
	if x.Limit > 0 {
		fmt.Fprintf(r.w, "  %s  %s\n", r.Muted(fmt.Sprintf("%-10s", "Limit")), r.Value(fmt.Sprint(x.Limit)))
	} else {
		fmt.Fprintf(r.w, "  %s  %s\n", r.Muted(fmt.Sprintf("%-10s", "Limit")), r.Value("unlimited"))
	}
	fmt.Fprintln(r.w)
}

type Summary struct {
	Job, Status, Output, SaturationReason string
	Stats                                 model.Stats
	Collapse                              string
	OutputCount                           int64
}

func (r *Renderer) Summary(status, output string, s model.Stats) {
	r.SummaryDetails(Summary{Status: status, Output: output, Stats: s})
}
func HumanStatus(status string) string {
	status = strings.TrimSpace(status)
	switch strings.ToUpper(status) {
	case "COMPLETE_DEGRADED":
		return "Complete · degraded"
	case "RATE_LIMITED":
		return "Rate limited"
	case "QUOTA_EXHAUSTED":
		return "Quota exhausted"
	}
	status = strings.ReplaceAll(strings.ToLower(status), "_", " ")
	if status == "" {
		return "Unknown"
	}
	return strings.ToUpper(status[:1]) + status[1:]
}
func statusColor(status string) string {
	switch strings.ToUpper(status) {
	case "READY", "COMPLETE", "MATCH", "RESPONSIVE":
		return green
	case "FAILED", "UNAVAILABLE", "UNHEALTHY", "MISMATCH", "ACCESS_DENIED", "UNAUTHORIZED", "TLS_ERROR", "MALFORMED_RESPONSE", "CONFIGURATION_ERROR":
		return red
	case "DEGRADED", "COMPLETE_DEGRADED", "SATURATED", "COOLDOWN", "RATE_LIMITED", "QUOTA_EXHAUSTED", "PARTIAL", "INTERRUPTED", "BUSY", "UNKNOWN", "CHALLENGE", "TIMEOUT", "NETWORK_ERROR", "DNS_ERROR", "CONNECTION_REFUSED", "SERVER_ERROR":
		return yellow
	default:
		return dim
	}
}
func (r *Renderer) status(status string) string {
	return r.paint(statusColor(status), HumanStatus(status))
}
func (r *Renderer) SummaryDetails(x Summary) {
	if !r.Enabled() {
		return
	}
	title := "Search complete"
	suffix := ""
	switch strings.ToUpper(x.Status) {
	case "COMPLETE_DEGRADED":
		suffix = " · " + r.paint(yellow, "degraded")
	case "SATURATED":
		title = "Search stopped"
		suffix = " · " + r.paint(yellow, "saturated")
	case "PARTIAL":
		title = "Search finished"
		suffix = " · " + r.paint(yellow, "partial")
	case "FAILED":
		title = "Search failed"
	case "INTERRUPTED":
		title = "Search interrupted"
	}
	titleStyle := green
	if strings.EqualFold(x.Status, "FAILED") {
		titleStyle = red
	} else if strings.EqualFold(x.Status, "INTERRUPTED") || strings.EqualFold(x.Status, "SATURATED") || strings.EqualFold(x.Status, "PARTIAL") {
		titleStyle = yellow
	}
	fmt.Fprintf(r.w, "\n%s%s\n\n", r.paint(titleStyle, title), suffix)
	fmt.Fprintln(r.w, r.heading("Results"))
	uniqueLabel := "Unique"
	if x.Collapse != "" && x.Collapse != "none" {
		uniqueLabel = "Unique URLs"
	}
	fmt.Fprintf(r.w, "  %s  %s\n  %s  %d\n", r.Muted(fmt.Sprintf("%-12s", uniqueLabel)), r.paint(green, fmt.Sprint(x.Stats.Unique)), r.Muted(fmt.Sprintf("%-12s", "Raw")), x.Stats.Raw)
	if x.Collapse != "" && x.Collapse != "none" {
		fmt.Fprintf(r.w, "  %s  %s\n  %s  %d\n", r.Muted(fmt.Sprintf("%-12s", "Output URLs")), r.paint(green, fmt.Sprint(x.OutputCount)), r.Muted(fmt.Sprintf("%-12s", "Collapsed")), x.Stats.Unique-x.OutputCount)
	}
	if x.Stats.Rejected > 0 {
		fmt.Fprintf(r.w, "  %s %s\n", r.Muted(fmt.Sprintf("%-12s", "Rejected")), r.paint(yellow, fmt.Sprint(x.Stats.Rejected)))
	}
	if x.Stats.Duplicates > 0 {
		fmt.Fprintf(r.w, "  %s %d\n", r.Muted(fmt.Sprintf("%-12s", "Duplicates")), x.Stats.Duplicates)
	}
	if x.Stats.UnknownKept > 0 {
		fmt.Fprintf(r.w, "  %s %d\n", r.Muted(fmt.Sprintf("%-12s", "Unknown kept")), x.Stats.UnknownKept)
	}
	if x.Stats.Malformed > 0 {
		fmt.Fprintf(r.w, "  %s %d\n", r.Muted(fmt.Sprintf("%-12s", "Malformed")), x.Stats.Malformed)
	}
	if x.Stats.Errors > 0 {
		fmt.Fprintf(r.w, "  %s %s\n", r.Muted(fmt.Sprintf("%-12s", "Errors")), r.paint(red, fmt.Sprint(x.Stats.Errors)))
	}
	if len(x.Stats.ProviderFailures) > 0 {
		fmt.Fprintln(r.w, "\n"+r.heading("Provider failures"))
		for _, failure := range x.Stats.ProviderFailures {
			message := failure.Message
			if message == "" {
				message = HumanStatus(failure.Kind)
			}
			fmt.Fprintf(r.w, "  %-12s %s · %s\n", failure.Provider, r.status(failure.Kind), message)
		}
	}
	if x.SaturationReason != "" {
		fmt.Fprintf(r.w, "\n%s\n  %s\n", r.heading("Stopped because"), HumanStatus(x.SaturationReason))
	}
	if len(x.Stats.UpstreamDegradation) > 0 {
		providers := map[string]bool{}
		for _, d := range x.Stats.UpstreamDegradation {
			providers[d.Provider] = true
		}
		names := make([]string, 0, len(providers))
		for name := range providers {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintln(r.w, "\n"+r.heading("Provider"))
		for _, name := range names {
			fmt.Fprintf(r.w, "  %-12s %s · %s\n", name, r.status("READY"), r.paint(yellow, "upstream degraded"))
		}
	}
	if x.Output != "" && x.Output != "-" {
		fmt.Fprintf(r.w, "\n%s\n  %s\n", r.heading("Saved"), r.Command(x.Output))
	}
	if x.Stats.Unique == 0 {
		switch {
		case x.Stats.Raw > 0 && x.Stats.Rejected > 0 && x.Stats.Accepted == 0:
			fmt.Fprintln(r.w, "\nNo usable results; all provider results were rejected by validation.")
		case x.Stats.Errors == 0:
			fmt.Fprintln(r.w, "\nNo results found.")
		}
	}
	fmt.Fprintln(r.w)
}

type ProviderRow struct {
	Name, State                     string
	Enabled, Configured, Pagination bool
	PageSize, MaximumResults        int
	Instances                       []ProviderInstance
}
type ProviderInstance struct{ Name, State string }

func (r *Renderer) Providers(rows []ProviderRow) {
	fmt.Fprintln(r.w, r.heading("Providers"))
	width := 0
	for _, row := range rows {
		if len(row.Name) > width {
			width = len(row.Name)
		}
	}
	for _, row := range rows {
		fmt.Fprintf(r.w, "  %-*s   %s\n", width, row.Name, r.status(row.State))
		for _, in := range row.Instances {
			fmt.Fprintf(r.w, "    %-*s %s · %s\n", width, "", in.Name, r.status(in.State))
		}
	}
}

type EngineStatus struct{ Name, State, Message string }
type ProviderTest struct {
	Name, State, Instance string
	Latency               time.Duration
	Results               int
	Engines               []EngineStatus
}

func (r *Renderer) ProviderTest(x ProviderTest) {
	fmt.Fprintln(r.w, r.heading(providerDisplayName(x.Name)))
	if x.Instance != "" {
		fmt.Fprintf(r.w, "\n  %s  %s\n", r.Muted(fmt.Sprintf("%-10s", "Instance")), x.Instance)
	}
	fmt.Fprintf(r.w, "  %s  %s\n  %s  %s\n  %s  %d\n", r.Muted(fmt.Sprintf("%-10s", "Status")), r.status(x.State), r.Muted(fmt.Sprintf("%-10s", "Latency")), x.Latency.Round(time.Millisecond), r.Muted(fmt.Sprintf("%-10s", "Results")), x.Results)
	if len(x.Engines) > 0 {
		fmt.Fprintln(r.w, "\n"+r.heading("Engines"))
		for _, e := range x.Engines {
			fmt.Fprintf(r.w, "    %-16s %s", e.Name, r.status(e.State))
			if e.Message != "" {
				fmt.Fprintf(r.w, " · %s", e.Message)
			}
			fmt.Fprintln(r.w)
		}
	}
}
func providerDisplayName(name string) string {
	if strings.EqualFold(name, "searxng") {
		return "SearXNG"
	}
	return name
}

type JobRow struct {
	ID, Status string
	Queries    int
	Unique     int64
	Started    time.Time
}

func (r *Renderer) Jobs(rows []JobRow) {
	fmt.Fprintln(r.w, "ID            STATUS                QUERIES  UNIQUE  STARTED")
	for _, row := range rows {
		status := fmt.Sprintf("%-20s", HumanStatus(row.Status))
		fmt.Fprintf(r.w, "%-12s  %s  %-7d  %-6d  %s\n", row.ID, r.paint(statusColor(row.Status), status), row.Queries, row.Unique, row.Started.Local().Format("2006-01-02 15:04"))
	}
}

type Constraint struct{ Name, Value string }
type QueryExplanation struct {
	Query, ProviderText string
	Local               []Constraint
	Scope               []string
}

func (r *Renderer) QueryExplain(x QueryExplanation) {
	fmt.Fprintf(r.w, "%s\n\n  %s\n\n%s\n\n", r.heading("Query"), x.Query, r.heading("Checked locally"))
	if len(x.Local) == 0 {
		fmt.Fprintln(r.w, "  none")
	}
	for _, c := range x.Local {
		fmt.Fprintf(r.w, "  %-12s %s\n", c.Name, c.Value)
	}
	if len(x.Scope) > 0 {
		fmt.Fprintln(r.w, "\n"+r.heading("Scope behavior")+"\n")
		for _, line := range x.Scope {
			fmt.Fprintln(r.w, "  "+line)
		}
	}
	fmt.Fprintln(r.w, "\n"+r.heading("Left to provider")+"\n")
	if strings.TrimSpace(x.ProviderText) == "" {
		fmt.Fprintln(r.w, "  none")
	} else {
		for _, term := range strings.Fields(x.ProviderText) {
			fmt.Fprintln(r.w, "  "+term)
		}
	}
}

type ValidationCheck struct{ Constraint, State, Reason string }
type QueryValidation struct {
	Query, URL, Result, Reason string
	Checks                     []ValidationCheck
}

func (r *Renderer) QueryValidate(x QueryValidation) {
	fmt.Fprintf(r.w, "%s\n  %s\n\n%s\n  %s\n\n%s\n  %s\n", r.heading("Query"), x.Query, r.heading("URL"), x.URL, r.heading("Result"), r.status(x.Result))
	if x.Reason != "" {
		fmt.Fprintf(r.w, "\n%s\n  %s\n", r.heading("Reason"), x.Reason)
	}
	if len(x.Checks) > 1 {
		fmt.Fprintln(r.w, "\n"+r.heading("Checks"))
		for _, c := range x.Checks {
			fmt.Fprintf(r.w, "  %-18s %s\n", c.Constraint, r.status(c.State))
		}
	}
}
func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
