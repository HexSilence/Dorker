package mock

import (
	"context"
	"fmt"
	"github.com/HexSilence/dorker/internal/model"
	"github.com/HexSilence/dorker/internal/provider"
	"strconv"
	"sync"
	"time"
)

type Config struct {
	Name                                          string
	Total, PageSize, RateLimitPage, TemporaryPage int
	QuotaPage                                     int
	Delay                                         time.Duration
	BaseURL                                       string
}
type Client struct {
	cfg      Config
	health   *provider.HealthTracker
	mu       sync.Mutex
	attempts map[string]int
}

func New(c Config) *Client {
	if c.Name == "" {
		c.Name = "mock"
	}
	if c.PageSize <= 0 {
		c.PageSize = 10
	}
	if c.BaseURL == "" {
		c.BaseURL = "https://example.test"
	}
	return &Client{cfg: c, health: provider.NewHealth(provider.Ready), attempts: map[string]int{}}
}
func (c *Client) Name() string                  { return c.cfg.Name }
func (c *Client) Health() provider.HealthStatus { return c.health.Snapshot() }
func (c *Client) Search(ctx context.Context, r provider.SearchRequest) (out provider.SearchResponse, err error) {
	done := c.health.Start()
	defer func() { done(err) }()
	if c.cfg.Delay > 0 {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(c.cfg.Delay):
		}
	}
	c.mu.Lock()
	key := r.Query + ":" + strconv.Itoa(r.Page)
	c.attempts[key]++
	a := c.attempts[key]
	c.mu.Unlock()
	if r.Page == c.cfg.RateLimitPage && a == 1 {
		return out, &provider.Error{Kind: provider.KindRateLimited, Message: "simulated 429", RetryAfter: 10 * time.Millisecond}
	}
	if r.Page == c.cfg.TemporaryPage && a == 1 {
		return out, &provider.Error{Kind: provider.KindTemporary, Message: "simulated 500"}
	}
	if r.Page == c.cfg.QuotaPage {
		return out, &provider.Error{Kind: provider.KindQuota, Message: "simulated quota exhaustion"}
	}
	size := c.cfg.PageSize
	start := r.Page * size
	if start >= c.cfg.Total {
		out.Done = true
		return out, nil
	}
	end := min(start+size, c.cfg.Total)
	for i := start; i < end; i++ {
		u := fmt.Sprintf("%s/result/%d?utm_source=mock", c.cfg.BaseURL, i)
		out.Results = append(out.Results, model.Result{URL: u, Title: fmt.Sprintf("Result %d", i), Provider: c.Name(), OriginalQuery: r.OriginalQuery, QueryVariant: r.Query, Rank: i + 1, Page: strconv.Itoa(r.Page), DiscoveredAt: time.Now().UTC()})
	}
	out.Done = end >= c.cfg.Total
	if !out.Done {
		out.NextCursor = strconv.Itoa(r.Page + 1)
	}
	return out, nil
}
