package serpapi

import (
	"context"
	"errors"
	"fmt"
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
	var s *httptest.Server
	s = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			_, _ = fmt.Fprintf(w, `{"search_metadata":{"status":"Success"},"serpapi_pagination":{"next":%q},"organic_results":[{"position":3,"link":"https://example.com/a","title":"A","snippet":"S"}]}`, s.URL+"?start=10&api_key=must-not-be-retained")
		} else {
			_, _ = w.Write([]byte(`{"organic_results":[]}`))
		}
	}))
	defer s.Close()
	c := New(s.Client(), secret, s.URL, 4096)
	one, err := c.Search(context.Background(), provider.SearchRequest{Query: `"exact phrase" site:example.com`, OriginalQuery: "q", PageSize: 10})
	if err != nil || len(one.Results) != 1 || one.Results[0].Rank != 3 || one.Results[0].Title != "A" || one.Results[0].Snippet != "S" || one.NextCursor != "10" || strings.Contains(fmt.Sprint(one), "must-not-be-retained") {
		t.Fatalf("%+v %v", one, err)
	}
	two, err := c.Search(context.Background(), provider.SearchRequest{Query: `"exact phrase" site:example.com`, Cursor: one.NextCursor, Page: 1, PageSize: 10})
	if err != nil || !two.Done {
		t.Fatalf("%+v %v", two, err)
	}
}

func TestPaginationFollowsValidatedUpstreamOffsets(t *testing.T) {
	var starts, nums []string
	requests := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		starts = append(starts, r.URL.Query().Get("start"))
		nums = append(nums, r.URL.Query().Get("num"))
		start := r.URL.Query().Get("start")
		requests++
		next := ""
		if start == "0" {
			next = server.URL + "?start=10&api_key=secret-in-next"
		} else if start == "10" {
			next = server.URL + "?start=20&api_key=secret-in-next"
		}
		var organic strings.Builder
		organic.WriteByte('[')
		for i := 0; i < 10; i++ {
			if i > 0 {
				organic.WriteByte(',')
			}
			_, _ = fmt.Fprintf(&organic, `{"position":%d,"link":"https://example.com/%s-%d"}`, i+1, start, i)
		}
		organic.WriteByte(']')
		_, _ = fmt.Fprintf(w, `{"serpapi_pagination":{"next":%q},"organic_results":%s}`, next, organic.String())
	}))
	defer server.Close()
	c := New(server.Client(), "key", server.URL, 4096)
	cursor := ""
	for page := 0; page < 3; page++ {
		out, err := c.Search(context.Background(), provider.SearchRequest{Query: "q", Page: page, PageSize: 100, Cursor: cursor})
		if err != nil || len(out.Results) != 10 {
			t.Fatalf("page=%d out=%+v err=%v", page, out, err)
		}
		cursor = out.NextCursor
		if page < 2 && out.Done {
			t.Fatalf("page %d ended despite continuation", page)
		}
		if page == 2 && !out.Done {
			t.Fatal("final page did not end")
		}
	}
	if fmt.Sprint(starts) != "[0 10 20]" || fmt.Sprint(nums) != "[100 100 100]" || requests != 3 {
		t.Fatalf("starts=%v nums=%v requests=%d", starts, nums, requests)
	}
}

func TestShortPagesAndUnsafePaginationMetadata(t *testing.T) {
	tests := []struct {
		name, next  string
		wantDone    bool
		wantCursor  string
		wantPartial bool
	}{
		{"short with continuation", "/search?start=10", false, "10", false},
		{"short without continuation", "", true, "", false},
		{"malformed", "/search?start=oops", true, "", true},
		{"replay", "/search?start=0", true, "", true},
		{"foreign origin", "https://attacker.test/search?start=10", true, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"serpapi_pagination":{"next":%q},"organic_results":[{"link":"https://example.com/a"},{"link":"https://example.com/b"},{"link":"https://example.com/c"},{"link":"https://example.com/d"}]}`, tt.next)
			}))
			defer s.Close()
			out, err := New(s.Client(), "key", s.URL, 4096).Search(context.Background(), provider.SearchRequest{Query: "q", PageSize: 100})
			if err != nil || out.Done != tt.wantDone || out.NextCursor != tt.wantCursor || (len(out.PartialErrors) > 0) != tt.wantPartial {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}

func TestEmptyPageWithoutContinuationIsDone(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"organic_results":[]}`)) }))
	defer s.Close()
	out, err := New(s.Client(), "key", s.URL, 4096).Search(context.Background(), provider.SearchRequest{Query: "q", PageSize: 100})
	if err != nil || !out.Done || len(out.Results) != 0 {
		t.Fatalf("out=%+v err=%v", out, err)
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
			want := provider.KindServer
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
	if provider.KindOf(err) != provider.KindMalformed {
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
