package export

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/HexSilence/dorker/internal/model"
	"golang.org/x/net/publicsuffix"
)

type CollapseMode string

const (
	CollapseNone   CollapseMode = "none"
	CollapseHost   CollapseMode = "host"
	CollapseDomain CollapseMode = "domain"
)

func ParseCollapseMode(value string) (CollapseMode, error) {
	mode := CollapseMode(strings.ToLower(strings.TrimSpace(value)))
	switch mode {
	case CollapseNone, CollapseHost, CollapseDomain:
		return mode, nil
	default:
		return "", fmt.Errorf("collapse must be none, host, or domain")
	}
}

func Collapse(results []model.Result, mode CollapseMode) ([]model.Result, error) {
	if mode == CollapseNone {
		return results, nil
	}
	groups := make(map[string]model.Result)
	for _, result := range results {
		key, err := collapseKey(result.NormalizedURL, mode)
		if err != nil {
			return nil, err
		}
		if current, ok := groups[key]; !ok || betterRepresentative(result, current) {
			groups[key] = result
		}
	}
	out := make([]model.Result, 0, len(groups))
	for _, result := range groups {
		out = append(out, result)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NormalizedURL < out[j].NormalizedURL })
	return out, nil
}

func collapseKey(raw string, mode CollapseMode) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("cannot collapse invalid URL %q", raw)
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if mode == CollapseHost {
		return host, nil
	}
	domain, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		// IP addresses and private/single-label hosts have no registrable domain.
		// Keeping their normalized host as the key is safe and avoids false merges.
		return host, nil
	}
	return domain, nil
}

func betterRepresentative(candidate, current model.Result) bool {
	return representativeRank(candidate.NormalizedURL) < representativeRank(current.NormalizedURL)
}

func representativeRank(raw string) string {
	u, _ := url.Parse(raw)
	https := "1"
	if strings.EqualFold(u.Scheme, "https") {
		https = "0"
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	root := "1"
	if path == "/" {
		root = "0"
	}
	queryCount := 0
	for _, values := range u.Query() {
		queryCount += len(values)
	}
	return fmt.Sprintf("%s\x00%s\x00%08d\x00%s\x00%08d\x00%s", https, root, len(path), path, queryCount, raw)
}
