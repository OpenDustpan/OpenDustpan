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

func (brokenCounter) PruneFailuresAt(context.Context, time.Time, time.Duration) (int64, error) {
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

// TestParallelFailuresCountOnEveryGate proves that failures on two gates at
// once all count in the shared count, and that each gate meets the burst in
// the counts that its own adds answer. After a refill of one unit in the
// process, only the shared count holds the source over the budget.
func TestParallelFailuresCountOnEveryGate(t *testing.T) {
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

// slowAuthenticator answers every token after delay, accepts "badgood",
// and counts the lookups that reach it.
type slowAuthenticator struct {
	delay   time.Duration
	lookups atomic.Int64
}

func (s *slowAuthenticator) AuthenticateToken(ctx context.Context, token string) (identity.Principal, error) {
	s.lookups.Add(1)

	timer := time.NewTimer(s.delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}

	return (&countingAuthenticator{lookups: atomic.Int64{}}).AuthenticateToken(ctx, token)
}

// TestConcurrentLookupsReserveTheBudget proves that a burst of concurrent
// charged lookups on two gates gets at most the burst of each gate to the
// store: each lookup takes its unit of the budget before the store read.
// The requests over the budget wait the penalty, and their clients go away.
func TestConcurrentLookupsReserveTheBudget(t *testing.T) {
	t.Parallel()

	const perGate = 20

	c := newClock()
	failures := authgatetest.NewMemory(c.Now)
	nexts := []*slowAuthenticator{
		{delay: 50 * time.Millisecond, lookups: atomic.Int64{}},
		{delay: 50 * time.Millisecond, lookups: atomic.Int64{}},
	}

	var wg sync.WaitGroup

	for _, next := range nexts {
		g, err := authgate.New(authgate.Config{
			Next: next, Charge: chargeBad, Logger: nil, Now: c.Now,
			Failures: failures, Window: 0, CounterErrors: nil,
			Burst: 3, Refill: time.Second, Penalty: time.Hour, MaxWaiting: perGate, MaxSources: 0,
		})
		if err != nil {
			t.Fatalf("new gate: %v", err)
		}

		for range perGate {
			wg.Go(func() {
				ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
				defer cancel()

				// Each attempt misses, or ends its wait when its
				// client goes away; the store lookups are the result.
				err := attempt(ctx, g, "192.0.2.1", "bad")
				if !errors.Is(err, identity.ErrUnauthenticated) && !errors.Is(err, identity.ErrRateLimited) {
					t.Errorf("concurrent attempt: err = %v, want a miss or a rate limit", err)
				}
			})
		}
	}

	wg.Wait()

	for i, next := range nexts {
		if got := next.lookups.Load(); got > 3 {
			t.Fatalf("gate %d: %d concurrent lookups reached the store, want at most the burst of 3", i, got)
		}
	}
}

// TestValidLookupGivesBackItsUnit proves that a charged lookup that does
// not miss gives back the unit that it took, so valid tokens never spend
// the budget.
func TestValidLookupGivesBackItsUnit(t *testing.T) {
	t.Parallel()

	c := newClock()
	g := newSharedGate(t, c, authgatetest.NewMemory(c.Now), &operations{got: nil, mu: sync.Mutex{}})

	// A done context ends a wait at once, so a valid token over the budget
	// fails here instead of waiting the penalty.
	for i := range 5 {
		if err := attempt(canceled(t), g, "192.0.2.1", "badgood"); err != nil {
			t.Fatalf("valid token %d: %v, want no wait", i+1, err)
		}
	}

	// Each check within the budget is a miss of its own, so three checks
	// spend the burst.
	for i := range 3 {
		if overBudget(t, g, "192.0.2.1") {
			t.Fatalf("miss %d after 5 valid tokens: over budget, want within", i+1)
		}
	}

	if !overBudget(t, g, "192.0.2.1") {
		t.Fatal("after 5 valid tokens and the burst of misses: within, want over budget")
	}
}

// TestStoreClockDecidesTheWindow proves that the gate takes the rest of a
// window from the answer of the store, also when the clock of the store
// differs from the clock of the instance by hours and part of a window.
func TestStoreClockDecidesTheWindow(t *testing.T) {
	t.Parallel()

	const offset = 7*time.Hour + 1500*time.Millisecond

	c := newClock()
	storeNow := func() time.Time { return c.Now().Add(offset) }
	failures := authgatetest.NewMemory(storeNow)
	g := newSharedGate(t, c, failures, &operations{got: nil, mu: sync.Mutex{}})
	other := newSharedGate(t, c, failures, &operations{got: nil, mu: sync.Mutex{}})

	miss(t, g, "192.0.2.1")
	miss(t, g, "192.0.2.1")
	miss(t, other, "192.0.2.1")

	left := storeNow().Truncate(sharedWindow).Add(sharedWindow).Sub(storeNow())

	c.Advance(left - time.Millisecond)

	if !overBudget(t, other, "192.0.2.1") {
		t.Fatal("1 ms before the window of the store ends: within, want over budget")
	}

	c.Advance(time.Millisecond)

	if overBudget(t, other, "192.0.2.1") {
		t.Fatal("when the window of the store ends: over budget, want within")
	}
}

// TestNewRefusesAWindowUnderOneMillisecond proves that the gate refuses a
// window that the store cannot count in milliseconds.
func TestNewRefusesAWindowUnderOneMillisecond(t *testing.T) {
	t.Parallel()

	_, err := authgate.New(authgate.Config{
		Next: &countingAuthenticator{lookups: atomic.Int64{}}, Charge: nil, Logger: nil, Now: nil,
		Failures: nil, Window: 500 * time.Microsecond, CounterErrors: nil,
		Burst: 0, Refill: 0, Penalty: 0, MaxWaiting: 0, MaxSources: 0,
	})
	if !errors.Is(err, authgate.ErrInvalidWindow) {
		t.Fatalf("window of 500 µs: err = %v, want ErrInvalidWindow", err)
	}
}

// TestConcurrentValidLookupsAreNeverLimited proves that valid tokens above
// the burst, all in flight at once through a slow store, all pass: a unit
// that an unresolved lookup holds is not a failure, so the next lookup
// waits for one to finish instead of the penalty, and gets no 429.
func TestConcurrentValidLookupsAreNeverLimited(t *testing.T) {
	t.Parallel()

	const lookups = 20

	c := newClock()
	next := &slowAuthenticator{delay: 50 * time.Millisecond, lookups: atomic.Int64{}}

	g, err := authgate.New(authgate.Config{
		Next: next, Charge: chargeBad, Logger: nil, Now: c.Now,
		Failures: authgatetest.NewMemory(c.Now), Window: 0, CounterErrors: nil,
		Burst: 3, Refill: time.Second, Penalty: time.Hour, MaxWaiting: 1, MaxSources: 0,
	})
	if err != nil {
		t.Fatalf("new gate: %v", err)
	}

	var wg sync.WaitGroup

	for range lookups {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			if err := attempt(ctx, g, "192.0.2.1", "badgood"); err != nil {
				t.Errorf("concurrent valid token: %v, want it to pass", err)
			}
		})
	}

	wg.Wait()

	if got := next.lookups.Load(); got != lookups {
		t.Fatalf("store lookups = %d, want %d", got, lookups)
	}

	// The valid lookups gave back every unit: the burst of misses is
	// within the budget.
	for i := range 3 {
		if overBudget(t, g, "192.0.2.1") {
			t.Fatalf("miss %d after the valid lookups: over budget, want within", i+1)
		}
	}
}

// panicAuthenticator panics on the token "badpanic" and answers every other
// token as countingAuthenticator does.
type panicAuthenticator struct {
	countingAuthenticator
}

func (p *panicAuthenticator) AuthenticateToken(ctx context.Context, token string) (identity.Principal, error) {
	if token == "badpanic" {
		panic("store fails")
	}

	return p.countingAuthenticator.AuthenticateToken(ctx, token)
}

// TestPanicInTheStoreGivesBackTheUnit proves that a lookup whose store read
// panics gives back its unit.
func TestPanicInTheStoreGivesBackTheUnit(t *testing.T) {
	t.Parallel()

	c := newClock()

	g, err := authgate.New(authgate.Config{
		Next:   &panicAuthenticator{countingAuthenticator{lookups: atomic.Int64{}}},
		Charge: chargeBad, Logger: nil, Now: c.Now, Failures: nil, Window: 0, CounterErrors: nil,
		Burst: 3, Refill: time.Second, Penalty: time.Hour, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		t.Fatalf("new gate: %v", err)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("lookup of the panic token: no panic, want one")
			}
		}()

		_ = attempt(t.Context(), g, "192.0.2.1", "badpanic") //nolint:errcheck // The lookup panics.
	}()

	for i := range 3 {
		if overBudget(t, g, "192.0.2.1") {
			t.Fatalf("miss %d after the panic: over budget, want within", i+1)
		}
	}
}

// blockingAuthenticator misses every token after release closes, and
// reports each lookup that starts on started.
type blockingAuthenticator struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingAuthenticator) AuthenticateToken(context.Context, string) (identity.Principal, error) {
	b.started <- struct{}{}
	<-b.release

	return identity.Principal{}, identity.ErrUnauthenticated
}

// TestRefillKeepsLookupsInFlightWithinTheBurst proves that a refill while
// lookups are in flight does not raise the budget above the burst: with
// the whole burst in flight, another lookup waits for one of them, also
// after a long refill.
func TestRefillKeepsLookupsInFlightWithinTheBurst(t *testing.T) {
	t.Parallel()

	c := newClock()
	next := &blockingAuthenticator{started: make(chan struct{}, 4), release: make(chan struct{})}

	g, err := authgate.New(authgate.Config{
		Next: next, Charge: chargeBad, Logger: nil, Now: c.Now, Failures: nil, Window: 0, CounterErrors: nil,
		Burst: 3, Refill: time.Second, Penalty: time.Hour, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		t.Fatalf("new gate: %v", err)
	}

	var wg sync.WaitGroup

	for range 3 {
		wg.Go(func() {
			if err := attempt(t.Context(), g, "192.0.2.1", "bad"); !errors.Is(err, identity.ErrUnauthenticated) {
				t.Errorf("lookup in flight: err = %v, want ErrUnauthenticated", err)
			}
		})
	}

	for range 3 {
		<-next.started
	}

	c.Advance(time.Minute)

	done := make(chan error, 1)

	go func() { done <- attempt(canceled(t), g, "192.0.2.1", "bad") }()

	select {
	case err := <-done:
		if !errors.Is(err, identity.ErrRateLimited) {
			t.Errorf("lookup with the burst in flight after a refill: err = %v, want ErrRateLimited", err)
		}

		close(next.release)
	case <-next.started:
		t.Error("lookup with the burst in flight after a refill reached the store, want a wait")
		close(next.release)
		<-done
	}

	wg.Wait()
}

// TestRefillAfterMissesStaysAtTheBurst proves that misses end their units
// once: after the burst of misses and a long refill, the budget of the
// process holds the burst again and no more. The gate has no shared count,
// so only the budget of the process decides.
func TestRefillAfterMissesStaysAtTheBurst(t *testing.T) {
	t.Parallel()

	c := newClock()
	g := newSharedGate(t, c, nil, &operations{got: nil, mu: sync.Mutex{}})

	spend(t, g, "192.0.2.1")
	c.Advance(time.Hour)

	for i := range 3 {
		if overBudget(t, g, "192.0.2.1") {
			t.Fatalf("miss %d after a refill: over budget, want within", i+1)
		}
	}

	if !overBudget(t, g, "192.0.2.1") {
		t.Fatal("fourth miss after a refill: within, want over budget")
	}
}

// TestWaiterWakesWhenMissesInFlightResolve proves that a request waiting
// behind misses in flight wakes when they resolve: it then meets the
// penalty and returns within about the penalty, not at its own deadline.
func TestWaiterWakesWhenMissesInFlightResolve(t *testing.T) {
	t.Parallel()

	const penalty = 50 * time.Millisecond

	c := newClock()
	next := &blockingAuthenticator{started: make(chan struct{}, 4), release: make(chan struct{})}

	g, err := authgate.New(authgate.Config{
		Next: next, Charge: chargeBad, Logger: nil, Now: c.Now, Failures: nil, Window: 0, CounterErrors: nil,
		Burst: 3, Refill: time.Hour, Penalty: penalty, MaxWaiting: 0, MaxSources: 0,
	})
	if err != nil {
		t.Fatalf("new gate: %v", err)
	}

	var wg sync.WaitGroup

	for range 3 {
		wg.Go(func() {
			missErr := attempt(t.Context(), g, "192.0.2.1", "bad")
			if !errors.Is(missErr, identity.ErrUnauthenticated) {
				t.Errorf("miss in flight: err = %v, want ErrUnauthenticated", missErr)
			}
		})
	}

	for range 3 {
		<-next.started
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)

	go func() { done <- attempt(ctx, g, "192.0.2.1", "bad") }()

	// Give the waiter time to reach the wait behind the lookups in flight.
	waitUntil(t, time.Now().Add(100*time.Millisecond))

	released := time.Now()

	close(next.release)

	err = <-done
	if took := time.Since(released); took > 5*time.Second {
		t.Fatalf("waiter returned %v after the misses resolved, want about the penalty of %v", took, penalty)
	}

	if !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("waiter after the penalty: err = %v, want ErrUnauthenticated from its lookup", err)
	}

	wg.Wait()
}

// waitUntil waits until the wall clock passes at, or fails the test when
// the test ends first.
func waitUntil(t *testing.T, at time.Time) {
	t.Helper()

	wait := time.NewTimer(time.Until(at))
	defer wait.Stop()

	select {
	case <-t.Context().Done():
		t.Fatalf("wait until %v: %v", at, t.Context().Err())
	case <-wait.C:
	}
}
