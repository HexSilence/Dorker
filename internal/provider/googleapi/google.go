package googleapi

import (
	"context"
	"errors"
	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/provider"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	client            *http.Client
	key, cx, endpoint string
	maxBody           int64
	health            *provider.HealthTracker
}

func New(c *http.Client, key, cx, endpoint string, maxBody int64) *Client {
	key, cx = strings.TrimSpace(key), strings.TrimSpace(cx)
	if endpoint == "" {
		endpoint = "https://customsearch.googleapis.com/customsearch/v1"
	}
	state := provider.Ready
	if key == "" || cx == "" {
		state = provider.Disabled
	}
	return &Client{c, key, cx, endpoint, maxBody, provider.NewHealth(state)}
}
func (c *Client) Name() string                  { return "google-api" }
func (c *Client) Health() provider.HealthStatus { return c.health.Snapshot() }
func (c *Client) Capabilities() provider.Capabilities {
	return provider.Capabilities{PageSizeMaximum: 10, MaximumAccessibleResults: 100, SupportsPagination: true}
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
	if c.key == "" || c.cx == "" {
		return out, &provider.Error{Kind: provider.KindUnauthorized, Message: "GOOGLE_API_KEY and GOOGLE_CSE_ID are required"}
	}
	n := r.PageSize
	if n <= 0 || n > 10 {
		n = 10
	}
	start := r.Page*n + 1
	if r.Cursor != "" {
		if x, e := strconv.Atoi(r.Cursor); e == nil && x >= 1 {
			start = x
		} else if e != nil {
			return out, &provider.Error{Kind: provider.KindInvalidQuery, Message: "invalid pagination cursor"}
		}
	}
	if start > 100 || start+n-1 > 100 {
		out.Done = true
		return out, nil
	}
	u, e := url.Parse(c.endpoint)
	if e != nil {
		return out, e
	}
	q := u.Query()
	q.Set("key", c.key)
	q.Set("cx", c.cx)
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
		if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
			return out, &provider.Error{Kind: provider.KindTemporary, Message: "request canceled or timed out", Cause: e}
		}
		return out, &provider.Error{Kind: provider.KindTemporary, Message: "request failed", Cause: e}
	}
	out.HTTPStatus = resp.StatusCode
	var payload struct {
		Items []struct {
			Link    string `json:"link"`
			Title   string `json:"title"`
			Snippet string `json:"snippet"`
		} `json:"items"`
		Queries struct {
			NextPage []struct {
				StartIndex int `json:"startIndex"`
			} `json:"nextPage"`
		} `json:"queries"`
	}
	if e = provider.DecodeJSONResponse(resp, c.maxBody, &payload); e != nil {
		return out, e
	}
	for i, x := range payload.Items {
		out.Results = append(out.Results, model.Result{URL: x.Link, Title: x.Title, Snippet: x.Snippet, Provider: c.Name(), OriginalQuery: r.OriginalQuery, QueryVariant: r.Query, Rank: start + i, Page: strconv.Itoa(r.Page), DiscoveredAt: time.Now().UTC()})
	}
	if len(payload.Queries.NextPage) > 0 && payload.Queries.NextPage[0].StartIndex <= 100 {
		out.NextCursor = strconv.Itoa(payload.Queries.NextPage[0].StartIndex)
	} else {
		out.Done = true
	}
	return out, nil
}
