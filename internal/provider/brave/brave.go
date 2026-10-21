package brave

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
		endpoint = "https://api.search.brave.com/res/v1/web/search"
	}
	st := provider.Ready
	if key == "" {
		st = provider.Disabled
	}
	return &Client{c, key, endpoint, maxBody, provider.NewHealth(st)}
}
func (c *Client) Name() string                  { return "brave" }
func (c *Client) Health() provider.HealthStatus { return c.health.Snapshot() }
func (c *Client) Capabilities() provider.Capabilities {
	return provider.Capabilities{PageSizeMaximum: 20, MaximumAccessibleResults: 200, SupportsPagination: true}
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
		return out, &provider.Error{Kind: provider.KindUnauthorized, Message: "BRAVE_API_KEY is required"}
	}
	n := r.PageSize
	if n <= 0 || n > 20 {
		n = 20
	}
	u, e := url.Parse(c.endpoint)
	if e != nil {
		return out, e
	}
	q := u.Query()
	q.Set("q", r.Query)
	q.Set("count", strconv.Itoa(n))
	offset := r.Page
	if r.Cursor != "" {
		var parseErr error
		offset, parseErr = strconv.Atoi(r.Cursor)
		if parseErr != nil || offset < 0 {
			return out, &provider.Error{Kind: provider.KindInvalidQuery, Message: "invalid pagination cursor"}
		}
	}
	if offset > 9 {
		out.Done = true
		return out, nil
	}
	q.Set("offset", strconv.Itoa(offset))
	u.RawQuery = q.Encode()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return out, e
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", c.key)
	resp, e := c.client.Do(req)
	if e != nil {
		return out, provider.TransportError("Brave request", e)
	}
	out.HTTPStatus = resp.StatusCode
	var p struct {
		Query struct {
			More bool `json:"more_results_available"`
		} `json:"query"`
		Web struct {
			Results []struct{ URL, Title, Description string } `json:"results"`
		} `json:"web"`
	}
	if e = provider.DecodeJSONResponse(resp, c.maxBody, &p); e != nil {
		return out, e
	}
	for i, x := range p.Web.Results {
		out.Results = append(out.Results, model.Result{URL: x.URL, Title: x.Title, Snippet: x.Description, Provider: c.Name(), OriginalQuery: r.OriginalQuery, QueryVariant: r.Query, Rank: r.Page*n + i + 1, Page: strconv.Itoa(r.Page), DiscoveredAt: time.Now().UTC()})
	}
	out.Done = !p.Query.More || len(p.Web.Results) == 0 || offset >= 9
	if !out.Done {
		out.NextCursor = strconv.Itoa(offset + 1)
	}
	return out, nil
}
