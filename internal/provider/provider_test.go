package provider

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

func TestTransportErrorClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind ErrorKind
	}{
		{"timeout", context.DeadlineExceeded, KindTimeout},
		{"canceled", context.Canceled, KindTemporary},
		{"dns", &net.DNSError{Err: "no such host", Name: "invalid.example"}, KindDNS},
		{"tls", x509.UnknownAuthorityError{}, KindTLS},
		{"refused", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, KindConnection},
		{"network", errors.New("transport broke"), KindNetwork},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TransportError("request", tt.err)
			var pe *Error
			if !errors.As(got, &pe) || pe.Kind != tt.kind || !errors.Is(pe.Cause, tt.err) {
				t.Fatalf("got=%v kind=%s cause=%v", got, KindOf(got), errors.Unwrap(got))
			}
		})
	}
}

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
