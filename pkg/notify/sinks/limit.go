package sinks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
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

// ErrLimitUnavailable reports a test that the server refuses because it cannot
// read or write the shared count of the limits. A test that the server cannot
// count could reach other networks without bound, so the server sends nothing
// and writes nothing for it.
var ErrLimitUnavailable = errors.New("the limits of tests are not available: try again later")

// RateLimitedError is the error of a test that the limits refuse. It matches
// ErrRateLimited. RetryAfter is the time until the limits take the next test.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string { return ErrRateLimited.Error() }

// Is makes errors.Is(err, ErrRateLimited) true.
func (*RateLimitedError) Is(target error) bool { return target == ErrRateLimited }

// Keys of the shared count. The scope and the separator keep these keys apart
// from the keys of the sign-in budget, which hold a source address only.
const (
	sinkKeyPrefix      = "sinktest|sink|"
	namespaceKeyPrefix = "sinktest|namespace|"
	// maxSinkIDLength caps the sink ID in a counter key, in bytes. A sink ID
	// is the prefix and lowercase letters and digits, as ident.NewID makes it.
	maxSinkIDLength = 100
	// counterTimeout bounds one call of the shared count, so that a stalled
	// database cannot hold a request.
	counterTimeout = 5 * time.Second
)

// testLimiter counts the tests in the shared count of the database, in fixed
// windows, so that the limits hold for all instances of one installation: the
// count of a window is the count of all of them. It keeps nothing in the
// memory of the process except the time of its last prune.
type testLimiter struct {
	counter  authgate.FailureCounter
	mu       sync.Mutex
	prunedAt time.Time
}

func newTestLimiter(counter authgate.FailureCounter) *testLimiter {
	return &testLimiter{counter: counter, mu: sync.Mutex{}, prunedAt: time.Time{}}
}

// take counts a test of the sink id in ns, and returns zero when both limits
// take it. When a limit refuses the test, take returns the time until the
// window of that limit ends. When the shared count fails, take returns an
// error that matches ErrLimitUnavailable and the test must not go on: the
// limits fail closed. A name or a sink ID that is not valid is refused with an
// error that matches notify.ErrInvalid, before a counter call, so a hostile
// identifier cannot change a key of the shared count.
//
// A window counts each call, also a refused one, so a caller that keeps
// asking does not get more tests. The sink limit counts first. A test that
// the sink limit refuses does not use a place of the namespace limit.
func (l *testLimiter) take(ctx context.Context, ns namespace.Name, id string, now time.Time) (time.Duration, error) {
	// Defense in depth: the handler and the service check the name and the ID
	// too; this check stays so that a new caller cannot skip it.
	if _, err := namespace.ParseName(ns.String()); err != nil {
		return 0, fmt.Errorf("%w: %w", notify.ErrInvalid, err)
	}

	if !strings.HasPrefix(id, notify.SinkIDPrefix) || !ident.ValidSlug(id, maxSinkIDLength) {
		return 0, fmt.Errorf("%w: the sink ID is not valid", notify.ErrInvalid)
	}

	ctx, cancel := context.WithTimeout(ctx, counterTimeout)
	defer cancel()

	sink, err := l.counter.AddFailure(ctx, sinkKeyPrefix+ns.String()+"/"+id, TestSinkInterval)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrLimitUnavailable, err)
	}

	if sink.Count > 1 {
		return waitOf(sink, TestSinkInterval), nil
	}

	space, err := l.counter.AddFailure(ctx, namespaceKeyPrefix+ns.String(), TestNamespaceWindow)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrLimitUnavailable, err)
	}

	if pruneErr := l.prune(ctx, now); pruneErr != nil {
		return 0, fmt.Errorf("%w: %w", ErrLimitUnavailable, pruneErr)
	}

	if space.Count > TestNamespaceBurst {
		return waitOf(space, TestNamespaceWindow), nil
	}

	return 0, nil
}

// waitOf returns the time until the window of c ends, at least one
// millisecond and at most length.
func waitOf(c authgate.FailureCount, length time.Duration) time.Duration {
	return max(time.Millisecond, min(c.End.Sub(c.At), length))
}

// prune deletes the windows that ended, at most once in each
// TestNamespaceWindow in this process. A prune that fails is tried again at
// the next test, and its error fails that test: a database that cannot prune
// cannot keep the count either.
func (l *testLimiter) prune(ctx context.Context, now time.Time) error {
	l.mu.Lock()
	due := l.prunedAt.IsZero() || now.Sub(l.prunedAt) >= TestNamespaceWindow

	previous := l.prunedAt
	if due {
		l.prunedAt = now
	}
	l.mu.Unlock()

	if !due {
		return nil
	}

	if _, err := l.counter.PruneFailures(ctx, TestNamespaceWindow); err != nil {
		l.mu.Lock()
		l.prunedAt = previous
		l.mu.Unlock()

		return fmt.Errorf("prune the count of tests: %w", err)
	}

	return nil
}
