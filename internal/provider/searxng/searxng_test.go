package searxng

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HexSilence/dorker/internal/provider"
)

func newTestClient(t *testing.T, h http.Handler, mutate func(*Options)) (*Client, *httptest.Server) {
	t.Helper()
	s := httptest.NewServer(h)
	o := Options{Name: "primary", URL: s.URL, RequestsPerSecond: 1000, Burst: 10, MaxBodyBytes: 4096}
	if mutate != nil {
		mutate(&o)
	}
	c, err := New(s.Client(), o)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	return c, s
}

func TestSearchJSONQueryPaginationAndProvenance(t *testing.T) {
	dork := `site:.gov intitle:"index of" ext:pdf`
	var calls atomic.Int32
	c, s := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/search" || r.URL.Query().Get("q") != dork || r.URL.Query().Get("format") != "json" {
			t.Errorf("request %s", r.URL.String())
		}
		wantPage := "1"
		if calls.Load() == 2 {
			wantPage = "2"
		}
		if r.URL.Query().Get("pageno") != wantPage {
			t.Errorf("page=%s", r.URL.Query().Get("pageno"))
		}
		if r.URL.Query().Get("engines") != "google,bing" || r.URL.Query().Get("categories") != "general" || r.URL.Query().Get("language") != "en" || r.URL.Query().Get("safesearch") != "1" || r.URL.Query().Get("time_range") != "month" {
			t.Error("selectors missing")
		}
		w.Header().Set("Content-Type", "application/json")
		if calls.Load() == 2 {
			fmt.Fprint(w, `{"results":[]}`)
			return
		}
		fmt.Fprint(w, `{"results":[{"url":"https://example.com/a","title":"A","content":"S","engines":["bing","google"],"score":1.2,"category":"general"},{"url":"not a url"}]}`)
	}), func(o *Options) {
		safe := 1
		o.Engines = []string{"google", "bing"}
		o.Categories = []string{"general"}
		o.Language = "en"
		o.SafeSearch = &safe
		o.TimeRange = "month"
	})
	defer s.Close()
	r, err := c.Search(context.Background(), provider.SearchRequest{Query: dork, OriginalQuery: dork, Page: 0, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if r.Done || len(r.Results) != 2 || r.Results[0].Provider != "searxng" || r.Results[0].ProviderInstance != "primary" || strings.Join(r.Results[0].SourceEngines, ",") != "bing,google" || r.Results[0].Rank != 1 || r.Results[0].Page != "1" {
		t.Fatalf("%+v", r)
	}
	r, err = c.Search(context.Background(), provider.SearchRequest{Query: dork, Page: 1, PageSize: 10})
	if err != nil || !r.Done || len(r.Results) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestMissingOptionalFields(t *testing.T) {
	c, s := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[{"url":"https://example.com","engine":"duckduckgo"}]}`)
	}), nil)
	defer s.Close()
	out, err := c.Search(context.Background(), provider.SearchRequest{Query: "x", PageSize: 10})
	if err != nil || len(out.Results) != 1 || strings.Join(out.Results[0].SourceEngines, "") != "duckduckgo" {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestUpstreamDiagnosticsDoNotFailSuccessfulPage(t *testing.T) {
	c, s := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[{"url":"https://example.edu/report.pdf","engines":["google cse","bing"]}],"unresponsive_engines":[["brave","too many requests"],["duckduckgo","CAPTCHA"],["google","access denied"],["broken"],42]}`)
	}), nil)
	defer s.Close()
	out, err := c.Search(context.Background(), provider.SearchRequest{Query: "x", OriginalQuery: "x"})
	if err != nil || len(out.Results) != 1 || len(out.Diagnostics) != 3 {
		t.Fatalf("%+v %v", out, err)
	}
	if out.Diagnostics[0].Classification != provider.UpstreamRateLimited || out.Diagnostics[1].Classification != provider.UpstreamChallenge {
		t.Fatalf("%+v", out.Diagnostics)
	}
	if out.Diagnostics[2].Classification != provider.UpstreamAccessDenied {
		t.Fatalf("%+v", out.Diagnostics)
	}
}
func TestMalformedDiagnosticContainerIsIgnored(t *testing.T) {
	c, s := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[{"url":"https://example.com"}],"unresponsive_engines":{"bad":true}}`)
	}), nil)
	defer s.Close()
	out, err := c.Search(context.Background(), provider.SearchRequest{Query: "x"})
	if err != nil || len(out.Results) != 1 || len(out.Diagnostics) != 0 {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestDiagnosticRedactsConfiguredToken(t *testing.T) {
	c, s := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[],"unresponsive_engines":[["x","failure TOP_SECRET"]]}`)
	}), func(o *Options) { o.Token = "TOP_SECRET" })
	defer s.Close()
	out, err := c.Search(context.Background(), provider.SearchRequest{Query: "x"})
	if err != nil || len(out.Diagnostics) != 1 || strings.Contains(out.Diagnostics[0].Message, "TOP_SECRET") {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestHTTPAndMalformedResponses(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body, ct string
		kind     provider.ErrorKind
		message  string
	}{{"400", 400, "bad", "application/json", provider.KindInvalidQuery, ""}, {"401", 401, "no", "application/json", provider.KindUnauthorized, ""}, {"403", 403, "no", "application/json", provider.KindUnauthorized, "JSON search API unavailable"}, {"404", 404, "no", "application/json", provider.KindInvalidQuery, ""}, {"429", 429, "slow", "application/json", provider.KindRateLimited, ""}, {"500", 500, "oops", "application/json", provider.KindServer, ""}, {"502", 502, "oops", "application/json", provider.KindServer, ""}, {"503", 503, "oops", "application/json", provider.KindServer, ""}, {"json", 200, "{", "application/json", provider.KindMalformed, "decode JSON"}, {"html", 200, "<html>login</html>", "text/html", provider.KindUnauthorized, "expected JSON"}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, s := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.ct)
				if tc.status == 429 {
					w.Header().Set("Retry-After", "2")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}), nil)
			defer s.Close()
			_, err := c.Search(context.Background(), provider.SearchRequest{Query: "x"})
			if provider.KindOf(err) != tc.kind {
				t.Fatalf("kind=%s err=%v", provider.KindOf(err), err)
			}
			if tc.message != "" && !strings.Contains(err.Error(), tc.message) {
				t.Fatal(err)
			}
			if tc.status == 429 && provider.RetryDelay(err) < 2*time.Second {
				t.Fatalf("retry=%s", provider.RetryDelay(err))
			}
		})
	}
}

func TestTimeoutCancellationAndOversize(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(50 * time.Millisecond) }))
		defer s.Close()
		hc := s.Client()
		hc.Timeout = 5 * time.Millisecond
		c, err := New(hc, Options{Name: "x", URL: s.URL, RequestsPerSecond: 100})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Search(context.Background(), provider.SearchRequest{Query: "x"})
		if provider.KindOf(err) != provider.KindTimeout {
			t.Fatal(err)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		c, s := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }), nil)
		defer s.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := c.Search(ctx, provider.SearchRequest{Query: "x"})
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
	t.Run("oversize", func(t *testing.T) {
		c, s := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"results":[]}`+strings.Repeat(" ", 100))
		}), func(o *Options) { o.MaxBodyBytes = 16 })
		defer s.Close()
		_, err := c.Search(context.Background(), provider.SearchRequest{Query: "x"})
		if err == nil || !strings.Contains(err.Error(), "size limit") {
			t.Fatal(err)
		}
	})
}

func TestEndpointValidation(t *testing.T) {
	for _, raw := range []string{"bad", "file:///tmp/x", "http://user:pass@example.com"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := New(http.DefaultClient, Options{Name: "x", URL: raw}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestAuthNotForwardedAcrossRedirect(t *testing.T) {
	var got string
	dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[]}`)
	}))
	defer dst.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dst.URL+"/search", http.StatusFound) }))
	defer src.Close()
	c, err := New(src.Client(), Options{Name: "x", URL: src.URL, Token: "TOP_SECRET", RequestsPerSecond: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Search(context.Background(), provider.SearchRequest{Query: "x"}); err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("authorization forwarded: %q", got)
	}
}

func TestClusterBalancedFailoverAndCooldown(t *testing.T) {
	var primary, secondary atomic.Int32
	p, ps := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primary.Add(1)
		w.Header().Set("Retry-After", "60")
		http.Error(w, "limited", 429)
	}), nil)
	defer ps.Close()
	s, ss := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondary.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[{"url":"https://example.com/a","engines":["google","bing"]}]}`)
	}), func(o *Options) { o.Name = "secondary" })
	defer ss.Close()
	cl, _ := NewCluster([]*Client{p, s}, "balanced")
	out, err := cl.Search(context.Background(), provider.SearchRequest{Query: "x", PageSize: 10})
	if err != nil || len(out.Results) != 1 || out.Results[0].ProviderInstance != "secondary" {
		t.Fatalf("%+v %v", out, err)
	}
	for range 3 {
		if _, err = cl.Search(context.Background(), provider.SearchRequest{Query: "x", PageSize: 10}); err != nil {
			t.Fatal(err)
		}
	}
	if primary.Load() != 1 || secondary.Load() != 4 {
		t.Fatalf("primary=%d secondary=%d", primary.Load(), secondary.Load())
	}
}

func TestClusterTemporaryFailureFallsBack(t *testing.T) {
	p, ps := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", http.StatusServiceUnavailable) }), nil)
	defer ps.Close()
	s, ss := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[{"url":"https://example.com/ok"}]}`)
	}), func(o *Options) { o.Name = "secondary" })
	defer ss.Close()
	cl, _ := NewCluster([]*Client{p, s}, "balanced")
	out, err := cl.Search(context.Background(), provider.SearchRequest{Query: "x"})
	if err != nil || len(out.Results) != 1 || len(out.PartialErrors) != 1 {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestClusterBalancedAndCoverage(t *testing.T) {
	makeClient := func(name, path string) (*Client, *httptest.Server) {
		return newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"results":[{"url":"https://example.com/%s"}]}`, path)
		}), func(o *Options) { o.Name = name })
	}
	a, as := makeClient("a", "same")
	defer as.Close()
	b, bs := makeClient("b", "same")
	defer bs.Close()
	bal, _ := NewCluster([]*Client{a, b}, "balanced")
	seen := map[string]bool{}
	for range 4 {
		out, err := bal.Search(context.Background(), provider.SearchRequest{Query: "x"})
		if err != nil {
			t.Fatal(err)
		}
		seen[out.Results[0].ProviderInstance] = true
	}
	if len(seen) != 2 {
		t.Fatalf("not balanced: %v", seen)
	}
	cov, _ := NewCluster([]*Client{a, b}, "coverage")
	out, err := cov.Search(context.Background(), provider.SearchRequest{Query: "x"})
	if err != nil || len(out.Results) != 2 {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestClusterCoverageAcceptsHealthyEmptyResponses(t *testing.T) {
	a, as := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[]}`)
	}), nil)
	defer as.Close()
	b, bs := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[]}`)
	}), func(o *Options) { o.Name = "secondary" })
	defer bs.Close()
	cluster, _ := NewCluster([]*Client{a, b}, "coverage")
	out, err := cluster.Search(context.Background(), provider.SearchRequest{Query: "x"})
	if err != nil || len(out.Results) != 0 || !out.Done {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestCleanNormalizesAndDeduplicatesValues(t *testing.T) {
	got := clean([]string{" DuckDuckGo ", "duckduckgo", "YAHOO", ""})
	if fmt.Sprint(got) != "[duckduckgo yahoo]" {
		t.Fatalf("got %v", got)
	}
}
