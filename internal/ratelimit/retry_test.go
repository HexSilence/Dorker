package ratelimit

import (
	"testing"
	"time"
)

func TestDelay(t *testing.T) {
	p := RetryPolicy{BaseDelay: time.Second, MaxDelay: 5 * time.Second}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second}
	for i, w := range want {
		if g := p.Delay(i, 0); g != w {
			t.Fatalf("%d: %v", i, g)
		}
	}
	if g := p.Delay(0, 3*time.Second); g != 3*time.Second {
		t.Fatal(g)
	}
}
