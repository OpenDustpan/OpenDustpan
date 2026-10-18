package httpexec

import (
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
	// CtxErr is the error of the context of the whole operation, ctx.Err()
	// of the engine, read after the attempt. A context error in Err with a
	// nil CtxErr is the timeout of one attempt, which the engine retries.
	CtxErr error
}

// Next returns the wait before the next attempt, and false when the engine
// must not send the request again. The engine adds random jitter to a wait
// that does not come from Retry-After.
//
// The rules, in order:
//   - No attempt after MaxAttempts.
//   - No retry when the context of the whole operation ended (CtxErr), and
//     no retry of ErrTooLarge or ErrOtherOrigin. The timeout of one attempt
//     is a network error.
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
	case a.CtxErr != nil, errors.Is(a.Err, ErrTooLarge), errors.Is(a.Err, ErrOtherOrigin):
		return false
	case (a.Method == http.MethodPost || a.Method == http.MethodPatch) && !a.Idempotent:
		return false
	case a.Err != nil:
		return true
	default:
		return slices.Contains(p.Statuses, a.Reply.Status)
	}
}

// maxRetryAfter is the largest Retry-After that retryAfter reads exactly. A
// larger value becomes this one before the conversion to a duration, so it
// cannot overflow, and it ends the retries as any value above MaxWait does.
const maxRetryAfter = 24 * time.Hour

// retryAfter reads a Retry-After header in seconds. The HTTP-date form falls
// back to the policy's own wait.
func retryAfter(h http.Header) (time.Duration, bool) {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" || strings.Trim(v, "0123456789") != "" {
		return 0, false
	}

	secs, err := strconv.ParseInt(v, 10, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, false
	}

	return time.Duration(min(secs, int64(maxRetryAfter/time.Second))) * time.Second, true
}
