package serpapi

import (
	"context"
	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/provider"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	client        *http.Client
	key, endpoint string
	maxBody       int64
	health        *provider.HealthTracker
}

func New(c *http.Client, key, endpoint string, maxBody int64) *Client {
	key = strings.TrimSpace(key)
	if endpoint == "" {
		endpoint = "https://serpapi.com/search.json"
	}
	st := provider.Ready
	if key == "" {
		st = provider.Disabled
	}
	return &Client{c, key, endpoint, maxBody, provider.NewHealth(st)}
}
func (c *Client) Name() string                  { return "serpapi" }
func (c *Client) Health() provider.HealthStatus { return c.health.Snapshot() }
func (c *Client) Capabilities() provider.Capabilities {
	// SerpAPI accepts num up to 100, but does not guarantee that many organic
	// results. Pagination therefore follows the upstream next offset rather
	// than treating this requested maximum as the amount actually returned.
	return provider.Capabilities{PageSizeMaximum: 100, SupportsPagination: true}
}
func (c *Client) Pagination(r provider.SearchRequest) provider.PaginationInfo {
	start := 0
	if r.Cursor != "" {
		start, _ = strconv.Atoi(r.Cursor)
	}
	return provider.PaginationInfo{UpstreamOffset: start, OffsetBased: true, PageSizeApplied: true}
}
func (c *Client) Search(ctx context.Context, r provider.SearchRequest) (out provider.SearchResponse, err error) {
	started := time.Now()
	done := c.health.Start()
	defer func() {
		out.Duration = time.Since(started)
		if err != nil {
			err = provider.WithProvider(c.Name(), err)
		}
		done(err)
	}()
	if c.key == "" {
		return out, &provider.Error{Kind: provider.KindUnauthorized, Message: "SERPAPI_API_KEY is required"}
	}
	n := r.PageSize
	if n <= 0 || n > c.Capabilities().PageSizeMaximum {
		n = 10
	}
	start := 0
	if r.Cursor != "" {
		var parseErr error
		start, parseErr = strconv.Atoi(r.Cursor)
		if parseErr != nil || start < 0 {
			return out, &provider.Error{Kind: provider.KindInvalidQuery, Message: "invalid pagination cursor"}
		}
	} else if r.Page > 0 {
		return out, &provider.Error{Kind: provider.KindMalformed, Message: "missing upstream pagination cursor"}
	}
	u, e := url.Parse(c.endpoint)
	if e != nil {
		return out, e
	}
	q := u.Query()
	q.Set("engine", "google")
	q.Set("api_key", c.key)
	q.Set("q", r.Query)
	q.Set("num", strconv.Itoa(n))
	q.Set("start", strconv.Itoa(start))
	u.RawQuery = q.Encode()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return out, e
	}
	resp, e := c.client.Do(req)
	if e != nil {
		return out, provider.TransportError("SerpAPI request", e)
	}
	out.HTTPStatus = resp.StatusCode
	var p struct {
		Organic []struct {
			Link, Title, Snippet string
			Position             int
		} `json:"organic_results"`
		SearchMetadata struct {
			Status string `json:"status"`
		} `json:"search_metadata"`
		Pagination struct {
			Next string `json:"next"`
		} `json:"serpapi_pagination"`
		Error string `json:"error"`
	}
	if e = provider.DecodeJSONResponse(resp, c.maxBody, &p); e != nil {
		return out, e
	}
	if p.Error != "" {
		low := strings.ToLower(p.Error)
		switch {
		case strings.Contains(low, "rate") || strings.Contains(low, "too many"):
			return out, &provider.Error{Kind: provider.KindRateLimited, Message: "rate limited"}
		case strings.Contains(low, "quota") || strings.Contains(low, "credits") || strings.Contains(low, "run out"):
			return out, &provider.Error{Kind: provider.KindQuota, Message: "quota exhausted"}
		case strings.Contains(low, "invalid api key") || strings.Contains(low, "missing api key") || strings.Contains(low, "unauthorized"):
			return out, &provider.Error{Kind: provider.KindUnauthorized, Message: "invalid API key"}
		default:
			return out, &provider.Error{Kind: provider.KindInvalidQuery, Message: "provider rejected request"}
		}
	}
	for i, x := range p.Organic {
		rank := x.Position
		if rank <= 0 {
			rank = start + i + 1
		}
		out.Results = append(out.Results, model.Result{URL: x.Link, Title: x.Title, Snippet: x.Snippet, Provider: c.Name(), OriginalQuery: r.OriginalQuery, QueryVariant: r.Query, Rank: x.Position, Page: strconv.Itoa(r.Page), DiscoveredAt: time.Now().UTC()})
		out.Results[len(out.Results)-1].Rank = rank
	}
	out.Done = p.Pagination.Next == ""
	if !out.Done {
		next, nextErr := validatedNextStart(p.Pagination.Next, c.endpoint, start)
		if nextErr != nil {
			// Preserve this page's usable results but stop rather than guessing an
			// offset that could replay or skip upstream results.
			out.Done = true
			out.PartialErrors = append(out.PartialErrors, &provider.Error{Kind: provider.KindMalformed, Message: "invalid SerpAPI next-page metadata"})
		} else {
			out.NextCursor = strconv.Itoa(next)
		}
	}
	return out, nil
}

func validatedNextStart(raw, endpoint string, current int) (int, error) {
	nextURL, err := url.Parse(raw)
	if err != nil {
		return 0, err
	}
	base, err := url.Parse(endpoint)
	if err != nil {
		return 0, err
	}
	if nextURL.IsAbs() && (!strings.EqualFold(nextURL.Scheme, base.Scheme) || !strings.EqualFold(nextURL.Host, base.Host)) {
		return 0, &provider.Error{Kind: provider.KindMalformed, Message: "unexpected SerpAPI pagination origin"}
	}
	value := nextURL.Query().Get("start")
	next, err := strconv.Atoi(value)
	if err != nil || next <= current {
		return 0, &provider.Error{Kind: provider.KindMalformed, Message: "invalid SerpAPI pagination offset"}
	}
	return next, nil
}

var _ provider.PaginationDescriber = (*Client)(nil)
