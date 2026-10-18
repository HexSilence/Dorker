package ratelimit

import (
	"math/rand/v2"
	"time"
)

type RetryPolicy struct {
	MaxAttempts         int
	BaseDelay, MaxDelay time.Duration
	Jitter              bool
}

func (p RetryPolicy) Delay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return min(retryAfter, p.MaxDelay)
	}
	if attempt < 0 {
		attempt = 0
	}
	d := p.BaseDelay
	for i := 0; i < attempt && d < p.MaxDelay; i++ {
		if d > p.MaxDelay/2 {
			d = p.MaxDelay
			break
		}
		d *= 2
	}
	if d > p.MaxDelay {
		d = p.MaxDelay
	}
	if p.Jitter && d > 0 {
		d += time.Duration(rand.Int64N(int64(d/4) + 1))
		if d > p.MaxDelay {
			d = p.MaxDelay
		}
	}
	return d
}
