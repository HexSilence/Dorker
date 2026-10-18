package googleapi

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

func TestSearchRequestParsingAndPagination(t *testing.T) {
	const secret = "SUPER_SECRET_GOOGLE_KEY"
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("q") != `site:example.com ext:pdf` {
			t.Errorf("query=%q", r.URL.Query().Get("q"))
		}
		if r.URL.Query().Get("key") != secret || r.URL.Query().Get("cx") != "cse" {
			t.Error("credentials missing")
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = w.Write([]byte(`{"items":[{"link":"https://example.com/a","title":"A","snippet":"S"}],"queries":{"nextPage":[{"startIndex":11}]}}`))
		} else {
			if r.URL.Query().Get("start") != "11" {
				t.Errorf("start=%q", r.URL.Query().Get("start"))
			}
			_, _ = w.Write([]byte(`{"items":[{"link":"https://example.com/b","title":"B","snippet":"T"}]}`))
		}
	}))
	defer s.Close()
	c := New(s.Client(), secret, "cse", s.URL, 4096)
	one, err := c.Search(context.Background(), provider.SearchRequest{Query: `site:example.com ext:pdf`, OriginalQuery: "original", PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Results) != 1 || one.Results[0].Title != "A" || one.Results[0].Snippet != "S" || one.Results[0].Rank != 1 || one.NextCursor != "11" || one.Done {
		t.Fatalf("%+v", one)
	}
	two, err := c.Search(context.Background(), provider.SearchRequest{Query: `site:example.com ext:pdf`, OriginalQuery: "original", Cursor: one.NextCursor, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(two.Results) != 1 || two.Results[0].Rank != 11 || !two.Done {
		t.Fatalf("%+v", two)
	}
}

func TestErrorsTimeoutCancellationAndRedaction(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		kind   provider.ErrorKind
	}{
		{"bad-request", 400, `{"error":"bad"}`, provider.KindInvalidQuery},
		{"unauthorized", 401, `{}`, provider.KindUnauthorized},
		{"forbidden", 403, `{}`, provider.KindUnauthorized},
		{"quota", 403, `{"error":"daily quota exceeded"}`, provider.KindQuota},
		{"rate", 429, `{}`, provider.KindRateLimited},
		{"server", 500, `{}`, provider.KindTemporary},
		{"unavailable", 503, `{}`, provider.KindTemporary},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.status == 429 {
					w.Header().Set("Retry-After", "30")
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer s.Close()
			_, err := New(s.Client(), "SUPER_SECRET_GOOGLE_KEY", "cse", s.URL, 4096).Search(context.Background(), provider.SearchRequest{Query: "q"})
			if provider.KindOf(err) != tt.kind || strings.Contains(err.Error(), "SUPER_SECRET") {
				t.Fatalf("%v", err)
			}
			if tt.status == 429 && provider.RetryDelay(err) != 30*time.Second {
				t.Fatalf("retry=%s", provider.RetryDelay(err))
			}
		})
	}
	malformed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{")) }))
	defer malformed.Close()
	_, err := New(malformed.Client(), "key", "cse", malformed.URL, 4096).Search(context.Background(), provider.SearchRequest{Query: "q"})
	if provider.KindOf(err) != provider.KindTemporary {
		t.Fatalf("malformed: %v", err)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	client := slow.Client()
	client.Timeout = 20 * time.Millisecond
	_, err = New(client, "key", "cse", slow.URL, 4096).Search(context.Background(), provider.SearchRequest{Query: "q"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = New(slow.Client(), "key", "cse", slow.URL, 4096).Search(ctx, provider.SearchRequest{Query: "q"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestEmptyAndLimit(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer s.Close()
	c := New(s.Client(), "key", "cse", s.URL, 4096)
	r, err := c.Search(context.Background(), provider.SearchRequest{Query: "q"})
	if err != nil || !r.Done || len(r.Results) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = c.Search(context.Background(), provider.SearchRequest{Query: "q", Cursor: "101"})
	if err != nil || !r.Done {
		t.Fatalf("%+v %v", r, err)
	}
}
