package provider

import (
	"testing"
	"time"
)

func TestHealthTransitions(t *testing.T) {
	h := NewHealth(Ready)
	finish := h.Start()
	finish(&Error{Kind: KindRateLimited, RetryAfter: time.Millisecond})
	if got := h.Snapshot().State; got != Cooldown {
		t.Fatalf("got %s", got)
	}
	time.Sleep(2 * time.Millisecond)
	if got := h.Snapshot().State; got != Ready {
		t.Fatalf("got %s", got)
	}
	finish = h.Start()
	finish(&Error{Kind: KindQuota})
	if got := h.Snapshot().State; got != QuotaExhausted {
		t.Fatalf("got %s", got)
	}
}
