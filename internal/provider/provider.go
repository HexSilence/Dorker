package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/HexSilence/dorker/internal/model"
)

type State string

const (
	Ready          State = "READY"
	Busy           State = "BUSY"
	RateLimited    State = "RATE_LIMITED"
	Cooldown       State = "COOLDOWN"
	QuotaExhausted State = "QUOTA_EXHAUSTED"
	Disabled       State = "DISABLED"
	Unhealthy      State = "UNHEALTHY"
	Degraded       State = "DEGRADED"
	Unavailable    State = "UNAVAILABLE"
)

type ErrorKind string

const (
	KindRateLimited  ErrorKind = "rate_limited"
	KindQuota        ErrorKind = "quota_exhausted"
	KindChallenge    ErrorKind = "challenge"
	KindUnauthorized ErrorKind = "unauthorized"
	KindTemporary    ErrorKind = "temporary"
	KindInvalidQuery ErrorKind = "invalid_query"
)

type Error struct {
	Provider   string
	Kind       ErrorKind
	Message    string
	RetryAfter time.Duration
	HTTPStatus int
	Cause      error
}

func (e *Error) Error() string {
	prefix := ""
	if e.Provider != "" {
		prefix = e.Provider + ": "
	}
	if e.Message != "" {
		return fmt.Sprintf("%s%s", prefix, e.Message)
	}
	return prefix + string(e.Kind)
}
func (e *Error) Unwrap() error { return e.Cause }
func KindOf(err error) ErrorKind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return ""
}
func Retryable(err error) bool {
	k := KindOf(err)
	return k == KindRateLimited || k == KindTemporary || k == KindChallenge
}
func RetryDelay(err error) time.Duration {
	var e *Error
	if errors.As(err, &e) {
		return e.RetryAfter
	}
	return 0
}

func WithProvider(name string, err error) error {
	if err == nil {
		return nil
	}
	var pe *Error
	if errors.As(err, &pe) {
		copy := *pe
		copy.Provider = name
		return &copy
	}
	return &Error{Provider: name, Kind: KindTemporary, Message: "request failed", Cause: err}
}

type SearchRequest struct {
	Query, OriginalQuery, Cursor string
	Page, PageSize               int
}
type SearchResponse struct {
	Results       []model.Result
	NextCursor    string
	Done          bool
	HTTPStatus    int
	Duration      time.Duration
	PartialErrors []error
	Diagnostics   []UpstreamDiagnostic
}

type UpstreamClassification string

const (
	UpstreamRateLimited  UpstreamClassification = "rate_limited"
	UpstreamChallenge    UpstreamClassification = "challenge"
	UpstreamTimeout      UpstreamClassification = "timeout"
	UpstreamNetworkError UpstreamClassification = "network_error"
	UpstreamServerError  UpstreamClassification = "server_error"
	UpstreamUnavailable  UpstreamClassification = "unavailable"
	UpstreamAccessDenied UpstreamClassification = "access_denied"
	UpstreamUnknown      UpstreamClassification = "unknown"
)

type UpstreamDiagnostic struct {
	Provider, Instance, Engine string
	Classification             UpstreamClassification
	Message, Query             string
	Page                       int
	Timestamp                  time.Time
}

// Capabilities describes hard provider bounds. Zero means unknown, not unlimited.
type Capabilities struct {
	PageSizeMaximum          int
	MaximumAccessibleResults int
	SupportsPagination       bool
}

type Capable interface{ Capabilities() Capabilities }
type Provider interface {
	Name() string
	Search(context.Context, SearchRequest) (SearchResponse, error)
	Health() HealthStatus
}
type HealthStatus struct {
	State                                              State
	Requests, Successes, TemporaryFailures, RateLimits int64
	AverageLatency                                     time.Duration
	ConsecutiveFailures                                int
	CooldownUntil                                      time.Time
	LastError                                          string
	LastErrorClass                                     ErrorKind
	LastSuccess, LastFailure                           time.Time
	RetryAfter                                         time.Duration
}

type HealthTracker struct {
	mu           sync.RWMutex
	h            HealthStatus
	totalLatency time.Duration
}

func NewHealth(state State) *HealthTracker { return &HealthTracker{h: HealthStatus{State: state}} }
func (t *HealthTracker) Snapshot() HealthStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	h := t.h
	if h.Requests > 0 {
		h.AverageLatency = t.totalLatency / time.Duration(h.Requests)
	}
	if h.State == Cooldown && !h.CooldownUntil.After(time.Now()) {
		h.State = Ready
	}
	return h
}
func (t *HealthTracker) Start() func(error) {
	start := time.Now()
	t.mu.Lock()
	t.h.Requests++
	t.h.State = Busy
	t.mu.Unlock()
	return func(err error) {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.totalLatency += time.Since(start)
		if err == nil {
			t.h.Successes++
			t.h.ConsecutiveFailures = 0
			t.h.State = Ready
			t.h.LastError = ""
			t.h.LastErrorClass = ""
			t.h.LastSuccess = time.Now()
			t.h.RetryAfter = 0
			return
		}
		t.h.ConsecutiveFailures++
		t.h.LastError = err.Error()
		t.h.LastErrorClass = KindOf(err)
		t.h.LastFailure = time.Now()
		t.h.RetryAfter = RetryDelay(err)
		switch KindOf(err) {
		case KindRateLimited, KindChallenge:
			t.h.RateLimits++
			t.h.State = RateLimited
			d := RetryDelay(err)
			if d > 0 {
				t.h.State = Cooldown
				t.h.CooldownUntil = time.Now().Add(d)
			}
		case KindQuota:
			t.h.State = QuotaExhausted
		case KindTemporary:
			t.h.TemporaryFailures++
			t.h.State = Degraded
		case KindUnauthorized:
			t.h.State = Unavailable
		default:
			t.h.State = Unavailable
		}
	}
}
