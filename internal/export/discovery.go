package export

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/HexSilence/dorker/internal/store"
)

type DiscoveryReport struct {
	Schema                 string                     `json:"schema"`
	JobID                  string                     `json:"job_id"`
	SeedQuery              string                     `json:"seed_query"`
	StartedAt              string                     `json:"started_at,omitempty"`
	FinishedAt             string                     `json:"finished_at,omitempty"`
	Status                 string                     `json:"status"`
	StopReason             string                     `json:"stop_reason"`
	CoverageStatus         string                     `json:"coverage_status,omitempty"`
	HistoricalDegradation  bool                       `json:"historical_degradation,omitempty"`
	TerminalEvidenceStatus string                     `json:"terminal_evidence_status,omitempty"`
	TerminalDetail         string                     `json:"terminal_reason_detail,omitempty"`
	Validation             string                     `json:"validation_mode"`
	Limits                 any                        `json:"limits"`
	Hosts                  int                        `json:"hosts"`
	Roots                  int                        `json:"registrable_domains"`
	Depths                 []store.DiscoveryDepthStat `json:"depths"`
	ProviderHealth         any                        `json:"provider_health_summary,omitempty"`
	HealthCountSemantics   string                     `json:"provider_health_count_semantics,omitempty"`
	ResponsiveSources      []string                   `json:"responsive_sources_observed,omitempty"`
	HostRecords            []store.DiscoveryHost      `json:"host_records"`
	Edges                  []store.DiscoveryEdge      `json:"discovery_edges"`
	Outputs                map[string]string          `json:"outputs"`
}
type ProviderHealthSummary struct {
	Provider         string `json:"provider"`
	ProviderInstance string `json:"provider_instance,omitempty"`
	Engine           string `json:"engine,omitempty"`
	Classification   string `json:"classification"`
	Count            int64  `json:"count"`
	Observations     int64  `json:"observations,omitempty"`
}

const discoveryOwnerFile = ".dorker-job"

// DiscoveryDirectory reserves a collision-safe directory for one job. The
// owner marker makes regeneration safe while preventing cross-job overwrite.
func DiscoveryDirectory(root, seed, job string) (string, error) {
	base := filepath.Join(filepath.Clean(root), "discover", Slug(seed))
	if err := os.MkdirAll(filepath.Dir(base), 0755); err != nil {
		return "", err
	}
	for n := 1; ; n++ {
		p := base
		if n > 1 {
			p = fmt.Sprintf("%s-%d", base, n)
		}
		if err := os.Mkdir(p, 0755); err == nil {
			if err = writeOwner(p, job); err != nil {
				_ = os.Remove(p)
				return "", err
			}
			return p, nil
		} else if os.IsExist(err) {
			continue
		} else if err != nil {
			return "", err
		}
	}
}

// ResolveDiscoveryDirectory reuses a persisted destination, claims an
// explicit destination, or recovers a legacy destination by report job ID.
func ResolveDiscoveryDirectory(root, seed, job, persisted, explicit string) (string, error) {
	if persisted != "" {
		if err := claimDiscoveryDirectory(persisted, job); err != nil {
			return "", err
		}
		return persisted, nil
	}
	if explicit != "" {
		if err := claimDiscoveryDirectory(explicit, job); err != nil {
			return "", err
		}
		return explicit, nil
	}
	base := filepath.Join(filepath.Clean(root), "discover", Slug(seed))
	entries, err := os.ReadDir(filepath.Dir(base))
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	baseName := filepath.Base(base)
	for _, entry := range entries {
		if !entry.IsDir() || (entry.Name() != baseName && !strings.HasPrefix(entry.Name(), baseName+"-")) {
			continue
		}
		p := filepath.Join(filepath.Dir(base), entry.Name())
		if ownedBy(p, job) {
			if err = claimDiscoveryDirectory(p, job); err != nil {
				return "", err
			}
			return p, nil
		}
	}
	return DiscoveryDirectory(root, seed, job)
}

func claimDiscoveryDirectory(dir, job string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	marker := filepath.Join(dir, discoveryOwnerFile)
	b, err := os.ReadFile(marker)
	if err == nil {
		if strings.TrimSpace(string(b)) != job {
			return fmt.Errorf("discovery output %q belongs to another job", dir)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) > 0 && !ownedBy(dir, job) {
		return fmt.Errorf("discovery output %q is not empty and is not owned by job %s", dir, job)
	}
	return writeOwner(dir, job)
}

func ownedBy(dir, job string) bool {
	if b, err := os.ReadFile(filepath.Join(dir, discoveryOwnerFile)); err == nil {
		return strings.TrimSpace(string(b)) == job
	}
	b, err := os.ReadFile(filepath.Join(dir, "discovery.json"))
	if err != nil {
		return false
	}
	var report struct {
		JobID string `json:"job_id"`
	}
	return json.Unmarshal(b, &report) == nil && report.JobID == job
}

func writeOwner(dir, job string) error {
	f, err := os.OpenFile(filepath.Join(dir, discoveryOwnerFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = io.WriteString(f, job+"\n"); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

func WriteDiscovery(ctx context.Context, st *store.Store, job, dir string, report DiscoveryReport) (map[string]string, error) {
	if err := claimDiscoveryDirectory(dir, job); err != nil {
		return nil, err
	}
	hosts, err := st.DiscoveryHosts(ctx, job)
	if err != nil {
		return nil, err
	}
	edges, err := st.DiscoveryEdges(ctx, job)
	if err != nil {
		return nil, err
	}
	report.HostRecords, report.Edges = hosts, edges
	rootsSet := map[string]struct{}{}
	for _, h := range hosts {
		if h.RegistrableDomain != "" {
			rootsSet[h.RegistrableDomain] = struct{}{}
		}
	}
	roots := make([]string, 0, len(rootsSet))
	for r := range rootsSet {
		roots = append(roots, r)
	}
	sort.Strings(roots)
	paths := map[string]string{"urls": filepath.Join(dir, "urls.txt"), "hosts": filepath.Join(dir, "hosts.txt"), "roots": filepath.Join(dir, "roots.txt"), "report": filepath.Join(dir, "discovery.json")}
	if err = writeReplace(paths["hosts"], func(w io.Writer) error {
		for _, h := range hosts {
			if _, e := fmt.Fprintln(w, h.Host); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err = writeReplace(paths["roots"], func(w io.Writer) error {
		for _, r := range roots {
			if _, e := fmt.Fprintln(w, r); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if _, err = WriteCollapsed(ctx, st, job, "txt", paths["urls"], true, io.Discard, CollapseNone); err != nil {
		return nil, err
	}
	report.Outputs = map[string]string{"urls": "urls.txt", "hosts": "hosts.txt", "roots": "roots.txt", "report": "discovery.json"}
	report.Hosts = len(hosts)
	report.Roots = len(roots)
	if err = writeReplace(paths["report"], func(w io.Writer) error { enc := json.NewEncoder(w); enc.SetIndent("", "  "); return enc.Encode(report) }); err != nil {
		return nil, err
	}
	return paths, nil
}

func WriteDiscoverySelection(ctx context.Context, st *store.Store, job, kind string, w io.Writer) error {
	hosts, err := st.DiscoveryHosts(ctx, job)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	seen := map[string]struct{}{}
	var values []string
	for _, h := range hosts {
		v := h.Host
		if kind == "roots" {
			v = h.RegistrableDomain
		}
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		values = append(values, v)
	}
	sort.Strings(values)
	for _, v := range values {
		if _, err = fmt.Fprintln(bw, v); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func writeReplace(path string, fn func(io.Writer) error) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".dorker-discovery-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(tmp)
		}
	}()
	if err = f.Chmod(0600); err == nil {
		err = fn(f)
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Close()
	}
	if err == nil {
		err = os.Rename(tmp, path)
		if err != nil {
			backup := path + ".dorker-old"
			_ = os.Remove(backup)
			if moveErr := os.Rename(path, backup); moveErr == nil {
				err = os.Rename(tmp, path)
				if err != nil {
					_ = os.Rename(backup, path)
				} else {
					_ = os.Remove(backup)
				}
			}
		}
	}
	if err != nil {
		return err
	}
	ok = true
	return nil
}
