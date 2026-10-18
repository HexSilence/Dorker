package serpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HexSilence/dorker/internal/provider"
)

func TestSearchParsingEncodingAndPagination(t *testing.T) {
	const secret = "SUPER_SECRET_SERPAPI_KEY"
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		q := r.URL.Query()
		if q.Get("api_key") != secret || q.Get("engine") != "google" {
			t.Error("credentials or engine missing")
		}
		if q.Get("q") != `"exact phrase" site:example.com` {
			t.Errorf("query=%q", q.Get("q"))
		}
		if requests == 2 && q.Get("start") != "10" {
			t.Errorf("start=%q", q.Get("start"))
		}
		if requests == 1 {
			_, _ = w.Write([]byte(`{"search_metadata":{"status":"Success"},"serpapi_pagination":{"next":"credential-bearing-url-is-not-retained"},"organic_results":[{"position":3,"link":"https://example.com/a","title":"A","snippet":"S"}]}`))
		} else {
			_, _ = w.Write([]byte(`{"organic_results":[]}`))
		}
	}))
	defer s.Close()
	c := New(s.Client(), secret, s.URL, 4096)
	one, err := c.Search(context.Background(), provider.SearchRequest{Query: `"exact phrase" site:example.com`, OriginalQuery: "q", PageSize: 10})
	if err != nil || len(one.Results) != 1 || one.Results[0].Rank != 3 || one.Results[0].Title != "A" || one.Results[0].Snippet != "S" || one.NextCursor != "10" {
		t.Fatalf("%+v %v", one, err)
	}
	two, err := c.Search(context.Background(), provider.SearchRequest{Query: `"exact phrase" site:example.com`, Cursor: one.NextCursor, Page: 1, PageSize: 10})
	if err != nil || !two.Done {
		t.Fatalf("%+v %v", two, err)
	}
}

func TestProviderErrorsHTTPTimeoutCancellationAndRedaction(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		kind       provider.ErrorKind
	}{{"key", `{"error":"Invalid API key SUPER_SECRET_SERPAPI_KEY"}`, provider.KindUnauthorized}, {"quota", `{"error":"Your account has run out of searches/credits"}`, provider.KindQuota}, {"rate", `{"error":"rate limit exceeded"}`, provider.KindRateLimited}, {"other", `{"error":"query rejected"}`, provider.KindInvalidQuery}} {
		t.Run(tt.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tt.body)) }))
			defer s.Close()
			_, err := New(s.Client(), "SUPER_SECRET_SERPAPI_KEY", s.URL, 4096).Search(context.Background(), provider.SearchRequest{Query: "q"})
			if provider.KindOf(err) != tt.kind || strings.Contains(err.Error(), "SUPER_SECRET") {
				t.Fatalf("%v", err)
			}
		})
	}
	for _, status := range []int{429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
			defer s.Close()
			_, err := New(s.Client(), "key", s.URL, 4096).Search(context.Background(), provider.SearchRequest{Query: "q"})
			want := provider.KindTemporary
			if status == 429 {
				want = provider.KindRateLimited
			}
			if provider.KindOf(err) != want {
				t.Fatalf("%v", err)
			}
		})
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{")) }))
	defer bad.Close()
	_, err := New(bad.Client(), "key", bad.URL, 4096).Search(context.Background(), provider.SearchRequest{Query: "q"})
	if provider.KindOf(err) != provider.KindTemporary {
		t.Fatalf("%v", err)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	client := slow.Client()
	client.Timeout = 20 * time.Millisecond
	_, err = New(client, "key", slow.URL, 4096).Search(context.Background(), provider.SearchRequest{Query: "q"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = New(slow.Client(), "key", slow.URL, 4096).Search(ctx, provider.SearchRequest{Query: "q"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}
