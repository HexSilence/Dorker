package brave

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

func TestSearchAndPagination(t *testing.T) {
	const secret = "SUPER_SECRET_BRAVE_KEY"
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("X-Subscription-Token") != secret {
			t.Error("missing key header")
		}
		if r.URL.Query().Get("q") != `intitle:"example" site:.gov` {
			t.Errorf("query=%q", r.URL.Query().Get("q"))
		}
		if requests == 2 && r.URL.Query().Get("offset") != "1" {
			t.Errorf("offset=%q", r.URL.Query().Get("offset"))
		}
		w.Header().Set("Content-Type", "application/json")
		more := map[bool]string{true: "true", false: "false"}[requests == 1]
		_, _ = w.Write([]byte(`{"query":{"more_results_available":` + more + `},"web":{"results":[{"url":"https://example.com/a","title":"A","description":"S"}]}}`))
	}))
	defer s.Close()
	c := New(s.Client(), secret, s.URL, 4096)
	one, err := c.Search(context.Background(), provider.SearchRequest{Query: `intitle:"example" site:.gov`, OriginalQuery: "q", PageSize: 10})
	if err != nil || len(one.Results) != 1 || one.Results[0].Title != "A" || one.Results[0].Snippet != "S" || one.Results[0].Rank != 1 || one.NextCursor != "1" {
		t.Fatalf("%+v %v", one, err)
	}
	two, err := c.Search(context.Background(), provider.SearchRequest{Query: `intitle:"example" site:.gov`, Cursor: one.NextCursor, Page: 1, PageSize: 10})
	if err != nil || !two.Done || two.Results[0].Rank != 11 {
		t.Fatalf("%+v %v", two, err)
	}
}

func TestErrorsEmptyCancellationAndRedaction(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		kind   provider.ErrorKind
	}{{"bad", 400, provider.KindInvalidQuery}, {"auth", 401, provider.KindUnauthorized}, {"forbidden", 403, provider.KindUnauthorized}, {"unprocessable", 422, provider.KindInvalidQuery}, {"rate", 429, provider.KindRateLimited}, {"server", 500, provider.KindServer}, {"unavailable", 503, provider.KindServer}} {
		t.Run(tt.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.status == 429 {
					w.Header().Set("Retry-After", "7")
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte("SUPER_SECRET_BRAVE_KEY"))
			}))
			defer s.Close()
			_, err := New(s.Client(), "SUPER_SECRET_BRAVE_KEY", s.URL, 4096).Search(context.Background(), provider.SearchRequest{Query: "q"})
			if provider.KindOf(err) != tt.kind || strings.Contains(err.Error(), "SUPER_SECRET") {
				t.Fatalf("%v", err)
			}
			if tt.status == 429 && provider.RetryDelay(err) != 7*time.Second {
				t.Fatal("Retry-After ignored")
			}
		})
	}
	for name, body := range map[string]string{"empty": `{"query":{"more_results_available":false},"web":{"results":[]}}`, "malformed": "{"} {
		t.Run(name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer s.Close()
			r, err := New(s.Client(), "key", s.URL, 4096).Search(context.Background(), provider.SearchRequest{Query: "q"})
			if name == "empty" && (err != nil || !r.Done) {
				t.Fatalf("%+v %v", r, err)
			}
			if name == "malformed" && provider.KindOf(err) != provider.KindMalformed {
				t.Fatalf("%v", err)
			}
		})
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	client := slow.Client()
	client.Timeout = 20 * time.Millisecond
	_, err := New(client, "key", slow.URL, 4096).Search(context.Background(), provider.SearchRequest{Query: "q"})
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
