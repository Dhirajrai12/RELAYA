// Package delivery forwards received events to customer destinations and
// retries failures according to the platform's recovery rules.
package delivery

import (
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// Outcome of one delivery attempt.
type Outcome string

const (
	Succeeded Outcome = "succeeded"
	Retry     Outcome = "retry"  // try again later
	Failed    Outcome = "failed" // retrying won't help
)

// Result is what one HTTP attempt produced.
type Result struct {
	StatusCode int // 0 when no response (network error, timeout, blocked)
	Err        error
	RetryAfter time.Duration // from a 429/503 Retry-After header, if any
	Duration   time.Duration
	Body       string // first bytes of the response, masked
}

// ErrBlockedAddress means the destination resolved to a private or internal address.
var ErrBlockedAddress = errors.New("destination address is not allowed (private or internal network)")

// Classify applies the recovery rules:
//
//	2xx                      -> succeeded
//	429, 408, 5xx, network   -> retry (backoff; 429/503 honour Retry-After)
//	3xx                      -> failed (redirects are not followed)
//	other 4xx                -> failed (the destination rejected it; retrying won't help)
//	blocked address          -> failed
func Classify(r Result) Outcome {
	switch {
	case errors.Is(r.Err, ErrBlockedAddress):
		return Failed
	case r.StatusCode == 0: // no response: network error or timeout
		return Retry
	case r.StatusCode >= 200 && r.StatusCode < 300:
		return Succeeded
	case r.StatusCode == http.StatusTooManyRequests, r.StatusCode == http.StatusRequestTimeout, r.StatusCode >= 500:
		return Retry
	default:
		return Failed
	}
}

// backoff is the delay before attempt n+1 after attempt n failed (1-based).
var backoff = []time.Duration{
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	30 * time.Minute,
	1 * time.Hour,
	3 * time.Hour,
	6 * time.Hour,
}

const maxRetryAfter = 6 * time.Hour

// NextDelay returns how long to wait after the given (1-based) failed attempt.
// A server-provided Retry-After wins when it is longer than our backoff.
// ±20% jitter spreads retries so a recovering endpoint isn't hit all at once.
func NextDelay(attempt int, retryAfter time.Duration) time.Duration {
	i := min(max(attempt-1, 0), len(backoff)-1)
	d := backoff[i]
	d += time.Duration((rand.Float64()*0.4 - 0.2) * float64(d))
	if retryAfter > d {
		d = min(retryAfter, maxRetryAfter)
	}
	return d
}

// parseRetryAfter reads a Retry-After header (seconds or HTTP date).
func parseRetryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}
