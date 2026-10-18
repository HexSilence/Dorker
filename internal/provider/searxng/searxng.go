package searxng

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/provider"
	"golang.org/x/time/rate"
)

type Options struct {
	Name, URL, Token    string
	Engines, Categories []string
	Language, TimeRange string
	SafeSearch          *int
	RequestsPerSecond   float64
	Burst               int
	MaxBodyBytes        int64
}

type Client struct {
	client              *http.Client
	name                string
	endpoint            *url.URL
	token               string
	engines, categories []string
	language, timeRange string
	safeSearch          *int
	maxBody             int64
	health              *provider.HealthTracker
	limiter             *rate.Limiter
}

func New(client *http.Client, o Options) (*Client, error) {
	if client == nil {
		return nil, errors.New("searxng HTTP client is nil")
	}
	name := strings.TrimSpace(o.Name)
	if name == "" {
		return nil, errors.New("searxng instance name is required")
	}
	u, err := url.Parse(strings.TrimSpace(o.URL))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("searxng/%s: invalid configured URL", name)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("searxng/%s: URL scheme must be http or https", name)
	}
	if u.User != nil {
		return nil, fmt.Errorf("searxng/%s: embedded URL credentials are not allowed", name)
	}
	u.RawQuery, u.Fragment = "", ""
	u.Path = strings.TrimRight(u.Path, "/") + "/search"
	rps := o.RequestsPerSecond
	if rps <= 0 {
		rps = 1
	}
	burst := o.Burst
	if burst < 1 {
		burst = 1
	}
	// Clone once so redirect policy is instance-local while sharing the transport.
	hc := *client
	prior := client.CheckRedirect
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && !sameOrigin(via[0].URL, req.URL) {
			req.Header.Del("Authorization")
		}
		if prior != nil {
			return prior(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &Client{client: &hc, name: name, endpoint: u, token: strings.TrimSpace(o.Token), engines: clean(o.Engines), categories: clean(o.Categories), language: strings.TrimSpace(o.Language), timeRange: strings.TrimSpace(o.TimeRange), safeSearch: o.SafeSearch, maxBody: o.MaxBodyBytes, health: provider.NewHealth(provider.Ready), limiter: rate.NewLimiter(rate.Limit(rps), burst)}, nil
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}
func clean(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func (c *Client) Name() string                  { return "searxng" }
func (c *Client) Instance() string              { return c.name }
func (c *Client) Health() provider.HealthStatus { return c.health.Snapshot() }
func (c *Client) Capabilities() provider.Capabilities {
	return provider.Capabilities{SupportsPagination: true}
}

func (c *Client) Search(ctx context.Context, r provider.SearchRequest) (out provider.SearchResponse, err error) {
	started := time.Now()
	finish := c.health.Start()
	defer func() {
		out.Duration = time.Since(started)
		if err != nil {
			err = provider.WithProvider("searxng/"+c.name, err)
		}
		finish(err)
	}()
	if err = c.limiter.Wait(ctx); err != nil {
		return out, &provider.Error{Kind: provider.KindTemporary, Message: "request canceled or timed out", Cause: err}
	}
	u := *c.endpoint
	q := u.Query()
	q.Set("q", r.Query)
	q.Set("format", "json")
	q.Set("pageno", strconv.Itoa(r.Page+1))
	if len(c.engines) > 0 {
		q.Set("engines", strings.Join(c.engines, ","))
	}
	if len(c.categories) > 0 {
		q.Set("categories", strings.Join(c.categories, ","))
	}
	if c.language != "" {
		q.Set("language", c.language)
	}
	if c.safeSearch != nil {
		q.Set("safesearch", strconv.Itoa(*c.safeSearch))
	}
	if c.timeRange != "" {
		q.Set("time_range", c.timeRange)
	}
	u.RawQuery = q.Encode()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return out, e
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, e := c.client.Do(req)
	if e != nil {
		return out, &provider.Error{Kind: provider.KindTemporary, Message: "request canceled or timed out", Cause: e}
	}
	out.HTTPStatus = resp.StatusCode
	if resp.StatusCode == 403 {
		resp.Body.Close()
		return out, &provider.Error{Kind: provider.KindUnauthorized, HTTPStatus: 403, Message: "JSON search API unavailable or access denied"}
	}
	var payload struct {
		Results []struct {
			URL, Title, Content, Engine, Category string
			Engines                               []string
			Score                                 float64
		} `json:"results"`
		UnresponsiveEngines json.RawMessage `json:"unresponsive_engines"`
	}
	if e = provider.DecodeJSONResponse(resp, c.maxBody, &payload); e != nil {
		return out, e
	}
	var diagnosticEntries []json.RawMessage
	_ = json.Unmarshal(payload.UnresponsiveEngines, &diagnosticEntries)
	for _, raw := range diagnosticEntries {
		if d, ok := parseDiagnostic(raw, c.name, c.token, r); ok {
			out.Diagnostics = append(out.Diagnostics, d)
		}
	}
	for i, x := range payload.Results {
		engines := clean(x.Engines)
		if len(engines) == 0 && strings.TrimSpace(x.Engine) != "" {
			engines = []string{strings.TrimSpace(x.Engine)}
		}
		sort.Strings(engines)
		out.Results = append(out.Results, model.Result{URL: x.URL, Title: x.Title, Snippet: x.Content, Provider: "searxng", ProviderInstance: c.name, SourceEngines: engines, OriginalQuery: r.OriginalQuery, QueryVariant: r.Query, Rank: r.Page*max(1, r.PageSize) + i + 1, Page: strconv.Itoa(r.Page + 1), DiscoveredAt: time.Now().UTC()})
	}
	out.Done = len(payload.Results) == 0
	if !out.Done {
		out.NextCursor = strconv.Itoa(r.Page + 2)
	}
	return out, nil
}

func parseDiagnostic(raw json.RawMessage, instance, secret string, r provider.SearchRequest) (provider.UpstreamDiagnostic, bool) {
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || len(parts) < 2 {
		return provider.UpstreamDiagnostic{}, false
	}
	var engine, reason string
	if json.Unmarshal(parts[0], &engine) != nil || json.Unmarshal(parts[1], &reason) != nil {
		return provider.UpstreamDiagnostic{}, false
	}
	engine, reason = strings.TrimSpace(engine), strings.TrimSpace(reason)
	if engine == "" || reason == "" {
		return provider.UpstreamDiagnostic{}, false
	}
	if len(reason) > 256 {
		reason = reason[:256]
	}
	if secret != "" {
		reason = strings.ReplaceAll(reason, secret, "[REDACTED]")
	}
	low := strings.ToLower(reason)
	class := provider.UpstreamUnknown
	switch {
	case strings.Contains(low, "too many requests") || strings.Contains(low, "rate limit") || strings.Contains(low, "429"):
		class = provider.UpstreamRateLimited
	case strings.Contains(low, "captcha") || strings.Contains(low, "challenge"):
		class = provider.UpstreamChallenge
	case strings.Contains(low, "timeout") || strings.Contains(low, "timed out"):
		class = provider.UpstreamTimeout
	case strings.Contains(low, "network") || strings.Contains(low, "connection"):
		class = provider.UpstreamNetworkError
	case strings.Contains(low, "server error") || strings.Contains(low, "500") || strings.Contains(low, "502") || strings.Contains(low, "503"):
		class = provider.UpstreamServerError
	case strings.Contains(low, "access denied") || strings.Contains(low, "forbidden"):
		class = provider.UpstreamAccessDenied
	case strings.Contains(low, "disabled") || strings.Contains(low, "unavailable"):
		class = provider.UpstreamUnavailable
	}
	return provider.UpstreamDiagnostic{Provider: "searxng", Instance: instance, Engine: engine, Classification: class, Message: reason, Query: r.OriginalQuery, Page: r.Page + 1, Timestamp: time.Now().UTC()}, true
}

type Cluster struct {
	instances []*Client
	strategy  string
	next      atomic.Uint64
}

func NewCluster(instances []*Client, strategy string) (*Cluster, error) {
	if len(instances) == 0 {
		return nil, errors.New("searxng requires at least one instance")
	}
	if strategy == "" {
		strategy = "balanced"
	}
	if strategy != "balanced" && strategy != "coverage" {
		return nil, errors.New("invalid searxng strategy")
	}
	return &Cluster{instances: instances, strategy: strategy}, nil
}
func (c *Cluster) Name() string { return "searxng" }
func (c *Cluster) Capabilities() provider.Capabilities {
	return provider.Capabilities{SupportsPagination: true}
}
func (c *Cluster) Instances() []*Client { return append([]*Client(nil), c.instances...) }
func (c *Cluster) Health() provider.HealthStatus {
	h := provider.HealthStatus{State: provider.Unavailable}
	for _, i := range c.instances {
		x := i.Health()
		h.Requests += x.Requests
		h.Successes += x.Successes
		h.TemporaryFailures += x.TemporaryFailures
		h.RateLimits += x.RateLimits
		if x.State == provider.Ready || x.State == provider.Busy {
			h.State = provider.Ready
		} else if x.State == provider.Degraded && h.State != provider.Ready {
			h.State = provider.Degraded
		}
	}
	return h
}
func (c *Cluster) Search(ctx context.Context, r provider.SearchRequest) (provider.SearchResponse, error) {
	start := int(c.next.Add(1)-1) % len(c.instances)
	var combined provider.SearchResponse
	var first error
	hasReady := false
	for _, candidate := range c.instances {
		state := candidate.Health().State
		if state == provider.Ready || state == provider.Busy {
			hasReady = true
			break
		}
	}
	for n := 0; n < len(c.instances); n++ {
		i := c.instances[(start+n)%len(c.instances)]
		h := i.Health()
		if h.State == provider.Cooldown && h.CooldownUntil.After(time.Now()) {
			continue
		}
		if h.State == provider.Disabled || h.State == provider.Unavailable || h.State == provider.QuotaExhausted || ((h.State == provider.Unhealthy || h.State == provider.Degraded) && hasReady) {
			continue
		}
		out, err := i.Search(ctx, r)
		if err != nil {
			if first == nil {
				first = err
			}
			combined.PartialErrors = append(combined.PartialErrors, err)
			continue
		}
		if c.strategy == "balanced" {
			out.PartialErrors = append(out.PartialErrors, combined.PartialErrors...)
			return out, nil
		}
		combined.Results = append(combined.Results, out.Results...)
		combined.Diagnostics = append(combined.Diagnostics, out.Diagnostics...)
		combined.HTTPStatus = out.HTTPStatus
		combined.Duration += out.Duration
		combined.Done = combined.Done || out.Done
	}
	if c.strategy == "coverage" && len(combined.Results) > 0 {
		combined.Done = false
		return combined, nil
	}
	if first != nil {
		return combined, first
	}
	return combined, &provider.Error{Provider: "searxng", Kind: provider.KindTemporary, Message: "all instances are in cooldown"}
}
