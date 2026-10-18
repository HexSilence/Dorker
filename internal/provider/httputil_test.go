package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDecodeRateLimit(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", "2"); w.WriteHeader(429) }))
	defer s.Close()
	resp, err := s.Client().Get(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	err = DecodeJSONResponse(resp, 1024, &struct{}{})
	if KindOf(err) != KindRateLimited || RetryDelay(err) != 2*time.Second {
		t.Fatalf("%v", err)
	}
}
func TestDecodeChallenge(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("captcha challenge"))
	}))
	defer s.Close()
	resp, _ := s.Client().Get(s.URL)
	if err := DecodeJSONResponse(resp, 1024, &struct{}{}); KindOf(err) != KindChallenge {
		t.Fatalf("%v", err)
	}
}
