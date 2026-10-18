package httpexec

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// RetryPolicy decides when the engine sends a request again.
type RetryPolicy struct {
	// MaxAttempts is the number of attempts, including the first.
	MaxAttempts int
	// Statuses are the HTTP statuses that the engine retries.
	Statuses []int
	// BaseWait is the wait after the first attempt. Each later wait doubles,
	// up to MaxWait.
	BaseWait time.Duration
	// MaxWait is the longest wait. A Retry-After that asks for more ends the
	// retries, so the engine does not ignore what the server asked for.
	MaxWait time.Duration
}

// The values of DefaultRetry.
const (
	defaultAttempts = 4
	defaultBaseWait = time.Second
	defaultMaxWait  = 30 * time.Second
)

// DefaultRetry returns the policy for a declaration that sets no retry: four
// attempts, the statuses 429, 502, 503 and 504, a first wait of one second,
// and at most 30 seconds.
func DefaultRetry() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: defaultAttempts,
		Statuses: []int{
			http.StatusTooManyRequests, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout,
		},
		BaseWait: defaultBaseWait,
		MaxWait:  defaultMaxWait,
	}
}

// Attempt is the outcome of one attempt.
type Attempt struct {
	// Number is the number of the attempt that ended, from 1.
	Number int
	// Method is the HTTP method of the request.
	Method string
	// Idempotent is true when the declaration allows a retry of a POST or a
	// PATCH.
	Idempotent bool
	// Reply is the reply. It is the zero value when Err is set.
	Reply Reply
	// Err is the error of the call, or nil when a reply came back.
	Err error
}

// Next returns the wait before the next attempt, and false when the engine
// must not send the request again. The engine adds random jitter to a wait
// that does not come from Retry-After.
//
// The rules, in order:
//   - No attempt after MaxAttempts.
//   - No retry of a canceled or expired context, or of ErrTooLarge.
//   - No retry of a POST or a PATCH that is not idempotent, also not after a
//     network error: the server can have applied it.
//   - A retry of a network error, and of a status in Statuses.
//   - Retry-After in seconds sets the wait. A value above MaxWait ends the
//     retries.
func (p RetryPolicy) Next(a Attempt) (time.Duration, bool) {
	if a.Number >= p.MaxAttempts || !p.retryable(a) {
		return 0, false
	}

	if a.Err == nil {
		if wait, ok := retryAfter(a.Reply.Header); ok {
			if wait > p.MaxWait {
				return 0, false
			}

			return wait, true
		}
	}

	wait := p.BaseWait
	for range a.Number - 1 {
		wait *= 2
		if wait >= p.MaxWait {
			return p.MaxWait, true
		}
	}

	return min(wait, p.MaxWait), true
}

func (p RetryPolicy) retryable(a Attempt) bool {
	switch {
	case errors.Is(a.Err, context.Canceled), errors.Is(a.Err, context.DeadlineExceeded), errors.Is(a.Err, ErrTooLarge):
		return false
	case (a.Method == http.MethodPost || a.Method == http.MethodPatch) && !a.Idempotent:
		return false
	case a.Err != nil:
		return true
	default:
		return slices.Contains(p.Statuses, a.Reply.Status)
	}
}

// retryAfter reads a Retry-After header in seconds. The HTTP-date form falls
// back to the policy's own wait.
func retryAfter(h http.Header) (time.Duration, bool) {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0, false
	}

	secs, err := strconv.Atoi(v)
	if err != nil || secs < 0 {
		return 0, false
	}

	return time.Duration(secs) * time.Second, true
}
