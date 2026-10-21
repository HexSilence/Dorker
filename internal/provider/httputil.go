package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func DecodeJSONResponse(resp *http.Response, max int64, dst any) error {
	defer resp.Body.Close()
	if max <= 0 {
		max = 4 << 20
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return TransportError("read response", err)
	}
	if int64(len(body)) > max {
		return &Error{Kind: KindMalformed, Message: "response exceeds size limit"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classifyHTTP(resp, body)
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	low := strings.ToLower(string(body))
	if strings.Contains(ct, "text/html") && (strings.Contains(low, "captcha") || strings.Contains(low, "challenge")) {
		return &Error{Kind: KindChallenge, Message: "provider returned a challenge page", RetryAfter: time.Minute}
	}
	if strings.Contains(ct, "text/html") || strings.HasPrefix(strings.TrimSpace(low), "<!doctype html") || strings.HasPrefix(strings.TrimSpace(low), "<html") {
		return &Error{Kind: KindUnauthorized, Message: "expected JSON search response but received HTML"}
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return &Error{Kind: KindMalformed, Message: "decode JSON response", Cause: err}
	}
	return nil
}
func classifyHTTP(resp *http.Response, body []byte) error {
	// Response bodies can contain echoed request parameters or provider details.
	// Classify them, but never return them verbatim to callers or logs.
	msg := strings.ToLower(strings.TrimSpace(string(body)))
	base := &Error{HTTPStatus: resp.StatusCode}
	switch resp.StatusCode {
	case 401:
		base.Kind, base.Message = KindUnauthorized, "invalid API credentials"
	case 403:
		if strings.Contains(msg, "quota") || strings.Contains(msg, "daily limit") || strings.Contains(msg, "usage limit") {
			base.Kind, base.Message = KindQuota, "quota exhausted"
			return base
		}
		base.Kind, base.Message, base.RetryAfter = KindUnauthorized, "permission denied", retryAfter(resp)
	case 429:
		base.Kind, base.Message, base.RetryAfter = KindRateLimited, "rate limited", retryAfter(resp)
	case 400, 404, 422:
		base.Kind, base.Message = KindInvalidQuery, fmt.Sprintf("invalid request (HTTP %d)", resp.StatusCode)
	default:
		if resp.StatusCode >= 500 {
			base.Kind, base.Message, base.RetryAfter = KindServer, fmt.Sprintf("provider server error (HTTP %d)", resp.StatusCode), retryAfter(resp)
			return base
		}
		base.Kind, base.Message = KindTemporary, fmt.Sprintf("unexpected provider response (HTTP %d)", resp.StatusCode)
	}
	return base
}
func retryAfter(r *http.Response) time.Duration {
	v := r.Header.Get("Retry-After")
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(time.Now()) {
		return time.Until(t)
	}
	return 0
}
