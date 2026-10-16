package authgate_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/authgate/authgatetest"
	"opendustpan.org/opendustpan/pkg/identity"
)

// errCounter is the failure of brokenCounter.
var errCounter = errors.New("counter fails")

// brokenCounter is a FailureCounter whose every call fails.
type brokenCounter struct{}

func (brokenCounter) AddFailure(context.Context, string, time.Time) (int64, error) {
	return 0, errCounter
}

func (brokenCounter) Failures(context.Context, string, time.Time) (int64, error) {
	return 0, errCounter
}

func (brokenCounter) PruneFailures(context.Context, time.Time) (int64, error) { return 0, errCounter }

// sharedWindow is the window of newSharedGate: the burst of 3 times the
// refill of 1 second.
const sharedWindow = 3 * time.Second

// newSharedGate returns a gate with a burst of 3 that counts its failures
// in failures.
func newSharedGate(
	t *testing.T, next identity.Authenticator, c *clock, failures authgate.FailureCounter,
) *authgate.Gate {
	t.Helper()

	g, err := authgate.New(authgate.Config{
		Next: next, Charge: chargeBad, Logger: nil, Now: c.Now, Failures: failures, Window: 0,
		Burst: 3, Refill: time.Second, Penalty: time.Hour, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		t.Fatalf("new gate: %v", err)
	}

	return g
}

// TestGatesOnOneCounterShareOneBudget proves that the misses on one gate
// spend the budget of the source on another gate with the same counter.
func TestGatesOnOneCounterShareOneBudget(t *testing.T) {
	t.Parallel()

	c := newClock()
	failures := authgatetest.NewMemory()
	next := &countingAuthenticator{lookups: atomic.Int64{}}
	first := newSharedGate(t, next, c, failures)
	second := newSharedGate(t, next, c, failures)

	spend(t, first, "192.0.2.1")

	if !overBudget(t, second, "192.0.2.1") {
		t.Fatal("second gate after the first gate spent the budget: within, want over budget")
	}

	if overBudget(t, second, "192.0.2.2") {
		t.Fatal("another source on the second gate: over budget, want within")
	}
}

// TestSharedBudgetRollsOverWithTheWindow proves that the shared count
// starts again in the next window, and that a gate prunes the windows
// before the one that ended.
func TestSharedBudgetRollsOverWithTheWindow(t *testing.T) {
	t.Parallel()

	c := newClock()
	failures := authgatetest.NewMemory()
	next := &countingAuthenticator{lookups: atomic.Int64{}}
	first := newSharedGate(t, next, c, failures)
	second := newSharedGate(t, next, c, failures)

	spend(t, first, "192.0.2.1")

	if !overBudget(t, second, "192.0.2.1") {
		t.Fatal("second gate in the window of the misses: within, want over budget")
	}

	c.Advance(sharedWindow)

	if overBudget(t, second, "192.0.2.1") {
		t.Fatal("second gate in the next window: over budget, want within")
	}

	c.Advance(2 * sharedWindow)

	if err := attempt(t.Context(), second, "192.0.2.1", "bad"); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("miss after two windows: err = %v, want ErrUnauthenticated", err)
	}

	if got := failures.Windows(); got != 1 {
		t.Fatalf("stored windows after a miss two windows later = %d, want 1", got)
	}
}

// TestSuccessfulAttemptWritesNoFailure proves that a valid token and an
// admitted step write nothing to the counter.
func TestSuccessfulAttemptWritesNoFailure(t *testing.T) {
	t.Parallel()

	c := newClock()
	failures := authgatetest.NewMemory()
	g := newSharedGate(t, &countingAuthenticator{lookups: atomic.Int64{}}, c, failures)

	if err := attempt(t.Context(), g, "192.0.2.1", "badgood"); err != nil {
		t.Fatalf("valid token: %v", err)
	}

	if err := g.Admit(authgate.WithRequest(t.Context(), "192.0.2.1")); err != nil {
		t.Fatalf("admit: %v", err)
	}

	if got := failures.Adds(); got != 0 {
		t.Fatalf("counter writes after a success = %d, want 0", got)
	}

	g.Fail(authgate.WithRequest(t.Context(), "192.0.2.1"))

	if got := failures.Adds(); got != 1 {
		t.Fatalf("counter writes after one failure = %d, want 1", got)
	}
}

// TestBrokenCounterKeepsTheBudgetOfTheProcess proves that a gate whose
// counter fails still limits a source by its own budget.
func TestBrokenCounterKeepsTheBudgetOfTheProcess(t *testing.T) {
	t.Parallel()

	c := newClock()
	g := newSharedGate(t, &countingAuthenticator{lookups: atomic.Int64{}}, c, brokenCounter{})

	spend(t, g, "192.0.2.1")

	if !overBudget(t, g, "192.0.2.1") {
		t.Fatal("after the burst with a broken counter: within, want over budget")
	}
}

// TestMissOfAGoneClientCounts proves that a miss counts in the shared count
// also when the client went away, so a client cannot cancel its count.
func TestMissOfAGoneClientCounts(t *testing.T) {
	t.Parallel()

	c := newClock()
	failures := authgatetest.NewMemory()
	g := newSharedGate(t, &countingAuthenticator{lookups: atomic.Int64{}}, c, failures)

	if err := attempt(canceled(t), g, "192.0.2.1", "bad"); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("miss of a gone client: err = %v, want ErrUnauthenticated", err)
	}

	start := c.Now().Truncate(sharedWindow)
	if got, err := failures.Failures(t.Context(), "192.0.2.1", start); err != nil || got != 1 {
		t.Fatalf("shared count after the miss of a gone client = %d, %v, want 1", got, err)
	}
}
