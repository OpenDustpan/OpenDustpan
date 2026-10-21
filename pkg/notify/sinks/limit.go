package sinks

import (
	"errors"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// Limits of the test route. A test sends one message to an address that a
// principal gave, and its result tells whether the receiver answered. An
// unbounded test would let a principal probe the hosts and ports of other
// networks from the address of the server. So a sink takes one test in each
// TestSinkInterval, and a namespace takes TestNamespaceBurst tests in each
// TestNamespaceWindow.
const (
	TestSinkInterval    = 10 * time.Second
	TestNamespaceBurst  = 10
	TestNamespaceWindow = time.Minute
)

// ErrRateLimited reports a test that the limits refuse. The service sends
// nothing and writes nothing for it.
var ErrRateLimited = errors.New("too many tests: try again later")

// RateLimitedError is the error of a test that the limits refuse. It matches
// ErrRateLimited. RetryAfter is the time until the limits take the next test.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string { return ErrRateLimited.Error() }

// Is makes errors.Is(err, ErrRateLimited) true.
func (*RateLimitedError) Is(target error) bool { return target == ErrRateLimited }

// testLimiter holds the times of the last tests. It lives in the memory of one
// server instance: with more instances, each one applies the limits, so the
// limits of the whole installation are the limits times the instances.
type testLimiter struct {
	mu     sync.Mutex
	sinks  map[string]time.Time
	spaces map[namespace.Name][]time.Time
	swept  time.Time
}

func newTestLimiter() *testLimiter {
	return &testLimiter{
		mu: sync.Mutex{}, sinks: map[string]time.Time{}, spaces: map[namespace.Name][]time.Time{},
		swept: time.Time{},
	}
}

// take records a test of the sink id in ns at now, and returns zero. When a
// limit refuses the test, take records nothing and returns the time until the
// test can pass.
func (l *testLimiter) take(ns namespace.Name, id string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)

	key := ns.String() + "/" + id

	var wait time.Duration

	if last, ok := l.sinks[key]; ok {
		wait = min(last.Add(TestSinkInterval).Sub(now), TestSinkInterval)
	}

	recent := l.recent(ns, now)
	if len(recent) >= TestNamespaceBurst {
		wait = max(wait, min(recent[0].Add(TestNamespaceWindow).Sub(now), TestNamespaceWindow))
	}

	if wait > 0 {
		return wait
	}

	l.sinks[key] = now
	l.spaces[ns] = append(recent, now)

	return 0
}

// recent returns the times of the tests of ns in the last TestNamespaceWindow.
func (l *testLimiter) recent(ns namespace.Name, now time.Time) []time.Time {
	times := l.spaces[ns]
	cut := 0

	for cut < len(times) && !times[cut].After(now.Add(-TestNamespaceWindow)) {
		cut++
	}

	return times[cut:]
}

// sweep drops the entries that no limit holds any more, so the maps stay as
// small as the tests of the last TestNamespaceWindow.
func (l *testLimiter) sweep(now time.Time) {
	if now.Sub(l.swept) < TestSinkInterval {
		return
	}

	l.swept = now

	for key, last := range l.sinks {
		if !last.Add(TestSinkInterval).After(now) {
			delete(l.sinks, key)
		}
	}

	for ns := range l.spaces {
		if recent := l.recent(ns, now); len(recent) == 0 {
			delete(l.spaces, ns)
		} else {
			l.spaces[ns] = recent
		}
	}
}
