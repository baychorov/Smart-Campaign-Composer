package delivery

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Kind is what happens to a claimed row.
type Kind string

const (
	KindSent    Kind = "sent"    // final
	KindFailed  Kind = "failed"  // final: permanent error or retries used up
	KindSkipped Kind = "skipped" // final: consent withdrawn after approval
	KindRetry   Kind = "retry"   // back to pending, due after Delay
	// KindRelease returns a claimed row that was never sent (shutdown) to
	// pending at once, without using up an attempt.
	KindRelease Kind = "release"
)

// Decision is the outcome stored for a job.
type Decision struct {
	Kind  Kind
	Delay time.Duration // KindRetry only
	// Throttled marks a retry after 429: it is counted in throttles, not
	// in attempts (D-RETRY).
	Throttled bool
	// Error goes to deliveries.last_error and the dashboard. It never holds
	// personal data: only status codes and error classes.
	Error string
}

// RetryPolicy implements the D-RETRY table.
type RetryPolicy struct {
	Base         time.Duration // first backoff ceiling
	Cap          time.Duration // largest backoff ceiling and largest Retry-After honoured
	MaxAttempts  int           // attempts per row, 429s excluded
	MaxThrottles int           // 429 replies per row
}

// DefaultRetryPolicy is the D-RETRY setting: 2 s doubling up to 5 min,
// 5 attempts. 20 throttles at the mock's 1 s Retry-After is ~20 s of
// waiting before a row gives up.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{Base: 2 * time.Second, Cap: 5 * time.Minute, MaxAttempts: 5, MaxThrottles: 20}
}

// Decide maps a provider result to a decision. rnd returns a uniform
// number in [0, 1) for the jitter.
//
//	2xx                          → sent
//	429                          → retry after Retry-After; failed after MaxThrottles
//	5xx, 408, 409, transport     → retry with backoff; failed after MaxAttempts
//	other 4xx                    → failed (the request itself is wrong)
//
// 409 is the provider saying "a request with this key is in progress":
// typically our own earlier request still running after a crash. Retrying
// later gets its result through idempotency.
func (p RetryPolicy) Decide(res SendResult, job Job, rnd func() float64) Decision {
	code := res.StatusCode
	switch {
	case res.Err != nil:
		return p.transient(job, errorClass(res.Err), rnd)
	case code >= 200 && code < 300:
		return Decision{Kind: KindSent}
	case code == http.StatusTooManyRequests:
		if job.Throttles+1 >= p.MaxThrottles {
			return Decision{Kind: KindFailed, Error: fmt.Sprintf("http 429, rate limited %d times", job.Throttles+1)}
		}
		wait := res.RetryAfter
		if wait <= 0 {
			wait = p.Base
		}
		return Decision{Kind: KindRetry, Delay: min(wait, p.Cap), Throttled: true, Error: "http 429"}
	case code >= 500, code == http.StatusRequestTimeout, code == http.StatusConflict:
		return p.transient(job, fmt.Sprintf("http %d", code), rnd)
	default:
		return Decision{Kind: KindFailed, Error: fmt.Sprintf("http %d", code)}
	}
}

func (p RetryPolicy) transient(job Job, msg string, rnd func() float64) Decision {
	if job.Attempts >= p.MaxAttempts {
		return Decision{Kind: KindFailed, Error: fmt.Sprintf("%s, gave up after %d attempts", msg, job.Attempts)}
	}
	return Decision{Kind: KindRetry, Delay: p.Backoff(job.Attempts, rnd), Error: msg}
}

// Backoff is exponential backoff with full jitter: a uniform delay in
// [0, min(Cap, Base·2^(attempt-1))). Spreading retries over the whole range
// keeps messages that failed together from returning together.
func (p RetryPolicy) Backoff(attempt int, rnd func() float64) time.Duration {
	ceiling := p.Base
	for i := 1; i < attempt && ceiling < p.Cap; i++ {
		ceiling *= 2
	}
	ceiling = min(ceiling, p.Cap)
	return time.Duration(rnd() * float64(ceiling))
}

// errorClass names a transport error without its details (which can carry
// URLs or addresses).
func errorClass(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	default:
		return "connection error"
	}
}
