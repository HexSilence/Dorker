package collector

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/normalize"
	"github.com/HexSilence/dorker/internal/query"
	"github.com/HexSilence/dorker/internal/store"
)

type Filter struct {
	IncludeDomains, ExcludeDomains []string
	Include, Exclude               *regexp.Regexp
}

func CompileFilter(incDomains, excDomains []string, inc, exc string) (Filter, error) {
	f := Filter{IncludeDomains: normalizeDomains(incDomains), ExcludeDomains: normalizeDomains(excDomains)}
	var err error
	if inc != "" {
		f.Include, err = regexp.Compile(inc)
		if err != nil {
			return f, fmt.Errorf("invalid include regex: %w", err)
		}
	}
	if exc != "" {
		f.Exclude, err = regexp.Compile(exc)
		if err != nil {
			return f, fmt.Errorf("invalid exclude regex: %w", err)
		}
	}
	return f, nil
}
func normalizeDomains(in []string) []string {
	out := make([]string, 0, len(in))
	for _, d := range in {
		d = strings.ToLower(strings.Trim(strings.TrimSpace(d), "."))
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}
func domainMatch(host, want string) bool { return host == want || strings.HasSuffix(host, "."+want) }
func (f Filter) Accept(u, domain string) bool {
	if len(f.IncludeDomains) > 0 {
		ok := false
		for _, d := range f.IncludeDomains {
			if domainMatch(domain, d) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, d := range f.ExcludeDomains {
		if domainMatch(domain, d) {
			return false
		}
	}
	if f.Include != nil && !f.Include.MatchString(u) {
		return false
	}
	return f.Exclude == nil || !f.Exclude.MatchString(u)
}

type Collector struct {
	Store                                                               *store.Store
	JobID                                                               string
	Normalizer                                                          *normalize.Normalizer
	Filter                                                              Filter
	OnUnique                                                            func(string)
	OnRejected                                                          func(model.Result, query.ValidationResult)
	ValidationMode                                                      query.ValidationMode
	ScopeConstraints                                                    *query.QueryConstraints
	Raw, Accepted, Rejected, UnknownKept, Unique, Duplicates, Malformed atomic.Int64
	constraints                                                         sync.Map
	reasonsMu                                                           sync.Mutex
	reasons                                                             map[query.ValidationReason]int64
}

func (c *Collector) RejectionReasons() map[query.ValidationReason]int64 {
	c.reasonsMu.Lock()
	defer c.reasonsMu.Unlock()
	out := map[query.ValidationReason]int64{}
	for k, v := range c.reasons {
		out[k] = v
	}
	return out
}
func (c *Collector) constraintsFor(raw string) (query.QueryConstraints, error) {
	if v, ok := c.constraints.Load(raw); ok {
		return v.(query.QueryConstraints), nil
	}
	x, err := query.ExtractConstraints(raw)
	if err != nil {
		return x, fmt.Errorf("parse query constraints: %w", err)
	}
	actual, _ := c.constraints.LoadOrStore(raw, x)
	return actual.(query.QueryConstraints), nil
}

func (c *Collector) Add(ctx context.Context, r model.Result) (bool, error) {
	n, err := c.AddBatch(ctx, []model.Result{r})
	return n == 1, err
}

// AddBatch normalizes and persists a complete response page atomically.
func (c *Collector) AddBatch(ctx context.Context, results []model.Result) (int, error) {
	valid := make([]model.Result, 0, len(results))
	c.Raw.Add(int64(len(results)))
	for _, r := range results {
		n, d, err := c.Normalizer.Normalize(r.URL)
		if err != nil {
			c.Malformed.Add(1)
			c.addReason(query.ReasonMalformedURL)
			continue
		}
		mode := c.ValidationMode
		if mode == "" {
			mode = query.ValidationConservative
		}
		constraints, err := c.constraintsFor(r.OriginalQuery)
		if err != nil {
			return 0, err
		}
		vr := constraints.Evaluate(n, mode)
		if vr.Decision != "REJECT" && c.ScopeConstraints != nil {
			scopeResult := c.ScopeConstraints.Evaluate(n, mode)
			if scopeResult.Decision == "REJECT" {
				vr = scopeResult
			}
		}
		if vr.Decision == "REJECT" {
			c.Rejected.Add(1)
			c.reasonsMu.Lock()
			if c.reasons == nil {
				c.reasons = map[query.ValidationReason]int64{}
			}
			for i, reason := range vr.Reasons {
				if vr.Evaluations[i].State == query.Mismatch || (vr.Evaluations[i].State == query.Unknown && mode == query.ValidationStrict) {
					c.reasons[reason]++
				}
			}
			c.reasonsMu.Unlock()
			if c.OnRejected != nil {
				c.OnRejected(r, vr)
			}
			continue
		}
		c.Accepted.Add(1)
		if vr.State == query.Unknown {
			c.UnknownKept.Add(1)
		}
		if !c.Filter.Accept(n, d) {
			continue
		}
		r.NormalizedURL, r.Domain = n, d
		valid = append(valid, r)
	}
	fresh, err := c.Store.InsertResults(ctx, c.JobID, valid)
	if err != nil {
		return 0, err
	}
	count := 0
	for i, isFresh := range fresh {
		if isFresh {
			count++
			c.Unique.Add(1)
			if c.OnUnique != nil {
				c.OnUnique(valid[i].NormalizedURL)
			}
		} else {
			c.Duplicates.Add(1)
		}
	}
	return count, nil
}

func (c *Collector) addReason(reason query.ValidationReason) {
	c.reasonsMu.Lock()
	defer c.reasonsMu.Unlock()
	if c.reasons == nil {
		c.reasons = map[query.ValidationReason]int64{}
	}
	c.reasons[reason]++
}
