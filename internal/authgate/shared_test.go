package authgate_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
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

func (brokenCounter) AddFailure(context.Context, string, time.Duration) (authgate.FailureCount, error) {
	return authgate.FailureCount{At: time.Time{}, End: time.Time{}, Count: 0}, errCounter
}

func (brokenCounter) PruneFailures(context.Context, time.Duration) (int64, error) {
	return 0, errCounter
}

// sharedWindow is the window of newSharedGate: the burst of 3 times the
// refill of 1 second.
const sharedWindow = 3 * time.Second

// operations records the operations that a gate reports to CounterErrors.
type operations struct {
	got []string
	mu  sync.Mutex
}

func (o *operations) add(operation string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.got = append(o.got, operation)
}

func (o *operations) list() []string {
	o.mu.Lock()
	defer o.mu.Unlock()

	return append([]string(nil), o.got...)
}

// newSharedGate returns a gate with a burst of 3 that counts its failures
// in failures and reports counter errors to errs.
func newSharedGate(
	t *testing.T, c *clock, failures authgate.FailureCounter, errs *operations,
) *authgate.Gate {
	t.Helper()

	g, err := authgate.New(authgate.Config{
		Next: &countingAuthenticator{lookups: atomic.Int64{}}, Charge: chargeBad, Logger: nil, Now: c.Now,
		Failures: failures, Window: 0, CounterErrors: errs.add,
		Burst: 3, Refill: time.Second, Penalty: time.Hour, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		t.Fatalf("new gate: %v", err)
	}

	return g
}

// miss makes one miss from source on g.
func miss(t *testing.T, g *authgate.Gate, source string) {
	t.Helper()

	if err := attempt(t.Context(), g, source, "bad"); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("miss from %s: err = %v, want ErrUnauthenticated", source, err)
	}
}

// TestGatesOnOneCounterShareOneBudget proves that each gate decides on the
// count that its add answers: misses on one gate spend the shared budget,
// and the next miss on another gate puts the source over the budget there.
func TestGatesOnOneCounterShareOneBudget(t *testing.T) {
	t.Parallel()

	c := newClock()
	failures := authgatetest.NewMemory(c.Now)
	first := newSharedGate(t, c, failures, &operations{got: nil, mu: sync.Mutex{}})
	second := newSharedGate(t, c, failures, &operations{got: nil, mu: sync.Mutex{}})

	miss(t, first, "192.0.2.1")
	miss(t, first, "192.0.2.1")
	miss(t, second, "192.0.2.1")

	if !overBudget(t, second, "192.0.2.1") {
		t.Fatal("second gate after the third shared miss: within, want over budget")
	}

	if overBudget(t, second, "192.0.2.2") {
		t.Fatal("another source on the second gate: over budget, want within")
	}
}

// TestParallelMissesPassNoMoreThanTheBudget proves that failures on two
// gates at once all count: each gate meets the burst in the counts that its
// own adds answer. After a refill of one unit in the process, only the
// shared count holds the source over the budget.
func TestParallelMissesPassNoMoreThanTheBudget(t *testing.T) {
	t.Parallel()

	c := newClock()
	failures := authgatetest.NewMemory(c.Now)
	gates := []*authgate.Gate{
		newSharedGate(t, c, failures, &operations{got: nil, mu: sync.Mutex{}}),
		newSharedGate(t, c, failures, &operations{got: nil, mu: sync.Mutex{}}),
	}

	var wg sync.WaitGroup

	for _, g := range gates {
		for range 3 {
			wg.Go(func() { g.Fail(authgate.WithRequest(t.Context(), "192.0.2.1")) })
		}
	}

	wg.Wait()

	c.Advance(time.Second)

	for i, g := range gates {
		if !overBudget(t, g, "192.0.2.1") {
			t.Fatalf("gate %d after 6 parallel misses and a refill of one: within, want over budget", i)
		}
	}
}

// TestSharedBudgetRollsOverWithTheWindow proves that a source over the
// shared budget is within it again when the window ends, and that a gate
// prunes the old windows but keeps the current windows of other sources.
func TestSharedBudgetRollsOverWithTheWindow(t *testing.T) {
	t.Parallel()

	c := newClock()
	failures := authgatetest.NewMemory(c.Now)
	first := newSharedGate(t, c, failures, &operations{got: nil, mu: sync.Mutex{}})
	second := newSharedGate(t, c, failures, &operations{got: nil, mu: sync.Mutex{}})

	miss(t, first, "192.0.2.1")
	miss(t, first, "192.0.2.1")
	miss(t, second, "192.0.2.1")

	end := c.Now().Truncate(sharedWindow).Add(sharedWindow)

	c.Advance(end.Sub(c.Now()) - time.Millisecond)

	if !overBudget(t, second, "192.0.2.1") {
		t.Fatal("second gate 1 ms before the window ends: within, want over budget")
	}

	c.Advance(time.Millisecond + sharedWindow)

	if overBudget(t, second, "192.0.2.1") {
		t.Fatal("second gate after the window ended: over budget, want within")
	}

	current := c.Now().Truncate(sharedWindow).Add(sharedWindow)

	c.Advance(sharedWindow)
	miss(t, first, "192.0.2.2")

	if got := failures.Count("192.0.2.1", end); got != 0 {
		t.Fatalf("count of the old window after a prune = %d, want 0", got)
	}

	if got := failures.Count("192.0.2.1", current); got != 1 {
		t.Fatalf("count of the window that ended less than a window ago = %d, want 1", got)
	}
}

// TestSuccessfulAttemptWritesNoFailure proves that a valid token and an
// admitted step write nothing to the counter.
func TestSuccessfulAttemptWritesNoFailure(t *testing.T) {
	t.Parallel()

	c := newClock()
	failures := authgatetest.NewMemory(c.Now)
	g := newSharedGate(t, c, failures, &operations{got: nil, mu: sync.Mutex{}})

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
// counter fails still limits a source by its own budget, and reports each
// failed call by operation.
func TestBrokenCounterKeepsTheBudgetOfTheProcess(t *testing.T) {
	t.Parallel()

	c := newClock()
	errs := &operations{got: nil, mu: sync.Mutex{}}
	g := newSharedGate(t, c, brokenCounter{}, errs)

	spend(t, g, "192.0.2.1")

	if !overBudget(t, g, "192.0.2.1") {
		t.Fatal("after the burst with a broken counter: within, want over budget")
	}

	got := errs.list()

	var adds, prunes int

	for _, op := range got {
		switch op {
		case "add":
			adds++
		case "prune":
			prunes++
		default:
			t.Fatalf("counter error operations = %v, want add and prune only", got)
		}
	}

	if adds != 3 || prunes != 1 {
		t.Fatalf("counter error operations = %v, want 3 adds and 1 prune", got)
	}
}

// TestMissOfAGoneClientCounts proves that a miss counts in the shared count
// also when the client went away, so a client cannot cancel its count.
func TestMissOfAGoneClientCounts(t *testing.T) {
	t.Parallel()

	c := newClock()
	failures := authgatetest.NewMemory(c.Now)
	g := newSharedGate(t, c, failures, &operations{got: nil, mu: sync.Mutex{}})

	if err := attempt(canceled(t), g, "192.0.2.1", "bad"); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("miss of a gone client: err = %v, want ErrUnauthenticated", err)
	}

	end := c.Now().Truncate(sharedWindow).Add(sharedWindow)
	if got := failures.Count("192.0.2.1", end); got != 1 {
		t.Fatalf("shared count after the miss of a gone client = %d, want 1", got)
	}
}

// deadlineCounter is a broken counter that records whether each call had a
// deadline.
type deadlineCounter struct {
	brokenCounter

	without atomic.Int64
}

func (d *deadlineCounter) AddFailure(
	ctx context.Context,
	source string,
	length time.Duration,
) (authgate.FailureCount, error) {
	if _, ok := ctx.Deadline(); !ok {
		d.without.Add(1)
	}

	return d.brokenCounter.AddFailure(ctx, source, length)
}

// TestCounterCallsHaveADeadline proves that the add of a miss carries a
// deadline, also for a client that went away, so a stalled database cannot
// hold the goroutines of the misses.
func TestCounterCallsHaveADeadline(t *testing.T) {
	t.Parallel()

	failures := &deadlineCounter{brokenCounter: brokenCounter{}, without: atomic.Int64{}}
	g := newSharedGate(t, newClock(), failures, &operations{got: nil, mu: sync.Mutex{}})

	g.Fail(authgate.WithRequest(t.Context(), "192.0.2.1"))
	g.Fail(authgate.WithRequest(canceled(t), "192.0.2.1"))

	if got := failures.without.Load(); got != 0 {
		t.Fatalf("adds without a deadline = %d, want 0", got)
	}
}

// TestGoneClientLogsNoCounterWarning proves that a failed counter call for
// a client that went away counts in CounterErrors but logs no warning.
func TestGoneClientLogsNoCounterWarning(t *testing.T) {
	t.Parallel()

	var logs strings.Builder

	errs := &operations{got: nil, mu: sync.Mutex{}}

	g, err := authgate.New(authgate.Config{
		Next: &countingAuthenticator{lookups: atomic.Int64{}}, Charge: chargeBad,
		Logger: slog.New(slog.NewTextHandler(&lockedWriter{w: &logs, mu: sync.Mutex{}}, nil)), Now: newClock().Now,
		Failures: brokenCounter{}, Window: 0, CounterErrors: errs.add,
		Burst: 3, Refill: time.Second, Penalty: time.Hour, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		t.Fatalf("new gate: %v", err)
	}

	g.Fail(authgate.WithRequest(canceled(t), "192.0.2.1"))

	if strings.Contains(logs.String(), "sign-in failure count failed") {
		t.Fatalf("log after a gone client:\n%s\nwant no counter warning", logs.String())
	}

	if got := errs.list(); len(got) == 0 || got[0] != "add" {
		t.Fatalf("counter error operations = %v, want add first", got)
	}

	g.Fail(authgate.WithRequest(t.Context(), "192.0.2.1"))

	if !strings.Contains(logs.String(), "sign-in failure count failed") {
		t.Fatalf("log after a present client:\n%s\nwant a counter warning", logs.String())
	}
}

// lockedWriter serializes writes to w.
type lockedWriter struct {
	w  io.Writer
	mu sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	n, err := l.w.Write(p)
	if err != nil {
		return n, fmt.Errorf("write log: %w", err)
	}

	return n, nil
}
