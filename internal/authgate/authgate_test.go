package authgate_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// countingAuthenticator accepts the token "badgood" and counts its lookups.
type countingAuthenticator struct {
	lookups atomic.Int64
}

func (c *countingAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	c.lookups.Add(1)

	if token != "badgood" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "ci",
		DisplayName:    "CI",
		Admin:          false,
		TokenID:        "tok-1",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}, nil
}

// clock is a settable test clock for the budget.
type clock struct {
	now time.Time
	mu  sync.Mutex
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

func newClock() *clock {
	return &clock{now: time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC), mu: sync.Mutex{}}
}

// chargeBad charges the lookups of tokens that start with "bad", as the
// server charges secret-shaped values. "badgood" is the valid token.
func chargeBad(token string) bool { return strings.HasPrefix(token, "bad") }

type gateOptions struct {
	penalty    time.Duration
	maxWaiting int
	maxSources int
}

func newGate(t *testing.T, next identity.Authenticator, c *clock, o gateOptions) *authgate.Gate {
	t.Helper()

	g, err := authgate.New(authgate.Config{
		Next:          next,
		Charge:        chargeBad,
		Logger:        nil,
		Now:           c.Now,
		Failures:      nil,
		Window:        0,
		Scope:         "",
		CounterErrors: nil,
		Burst:         3,
		Refill:        time.Second,
		Penalty:       o.penalty,
		MaxWaiting:    o.maxWaiting,
		MaxSources:    o.maxSources,
	})
	if err != nil {
		t.Fatalf("new gate: %v", err)
	}

	return g
}

func attempt(ctx context.Context, g *authgate.Gate, source, token string) error {
	_, err := g.AuthenticateToken(authgate.WithRequest(ctx, source), token)

	return err
}

// canceled returns a context that is already done: a request over the
// budget then ends its wait at once, with ErrRateLimited and no lookup,
// while a request within the budget still goes to the store.
func canceled(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	return ctx
}

// spend makes three misses from source, the whole burst.
func spend(t *testing.T, g *authgate.Gate, source string) {
	t.Helper()

	for i := range 3 {
		if err := attempt(t.Context(), g, source, "bad"); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Fatalf("miss %d from %s: err = %v, want ErrUnauthenticated", i+1, source, err)
		}
	}
}

// overBudget reports whether source is over its budget: its request waits,
// so with a done context it ends with ErrRateLimited.
func overBudget(t *testing.T, g *authgate.Gate, source string) bool {
	t.Helper()

	return errors.Is(attempt(canceled(t), g, source, "bad"), identity.ErrRateLimited)
}

func TestNewNeedsNext(t *testing.T) {
	t.Parallel()

	_, err := authgate.New(authgate.Config{
		Next:          nil,
		Charge:        nil,
		Logger:        nil,
		Now:           nil,
		Failures:      nil,
		Window:        0,
		Scope:         "",
		CounterErrors: nil,
		Burst:         0,
		Refill:        0,
		Penalty:       0,
		MaxWaiting:    0,
		MaxSources:    0,
	})
	if !errors.Is(err, authgate.ErrMissingNext) {
		t.Fatalf("err = %v, want ErrMissingNext", err)
	}
}

func TestOverTheBudgetEveryRequestWaitsBeforeTheStore(t *testing.T) {
	t.Parallel()

	next := &countingAuthenticator{lookups: atomic.Int64{}}
	c := newClock()
	g := newGate(t, next, c, gateOptions{penalty: 20 * time.Millisecond, maxWaiting: 4, maxSources: 0})

	spend(t, g, "192.0.2.1")

	// A valid token waits too, so the time of the answer tells nothing, and
	// it still passes.
	start := time.Now()

	if err := attempt(t.Context(), g, "192.0.2.1", "badgood"); err != nil {
		t.Fatalf("valid token over the budget: %v", err)
	}

	if waited := time.Since(start); waited < 20*time.Millisecond {
		t.Fatalf("valid token over the budget answered after %s, want the penalty of 20ms first", waited)
	}

	// A miss over the budget waits and answers the plain 401.
	if err := attempt(t.Context(), g, "192.0.2.1", "bad"); !errors.Is(err, identity.ErrUnauthenticated) ||
		errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("miss over the budget: err = %v, want a plain ErrUnauthenticated", err)
	}

	// A client that goes away during the wait causes no store read.
	before := next.lookups.Load()

	if !overBudget(t, g, "192.0.2.1") || next.lookups.Load() != before {
		t.Fatal("a request whose client went away during the wait reached the store")
	}

	// Another source keeps its budget, and one refill period gives one
	// request back.
	if overBudget(t, g, "192.0.2.2") {
		t.Fatal("another source was over the budget")
	}

	c.Advance(time.Second)

	if overBudget(t, g, "192.0.2.1") {
		t.Fatal("the source was over the budget after one refill")
	}
}

func TestWaitingCapAnswers429WithNoStoreRead(t *testing.T) {
	t.Parallel()

	next := &countingAuthenticator{lookups: atomic.Int64{}}
	g := newGate(t, next, newClock(), gateOptions{penalty: time.Hour, maxWaiting: 1, maxSources: 0})

	spend(t, g, "192.0.2.1")

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	hold := func() { go func() { done <- attempt(ctx, g, "192.0.2.1", "badgood") }() }

	hold()

	// The holder takes the one waiting place. A probe then gets ErrLimited
	// at once, with no store read. A probe can also take the place for a
	// moment, and the holder then gets ErrLimited: start it again.
	deadline := time.Now().Add(10 * time.Second)

	for {
		select {
		case err := <-done:
			if !errors.Is(err, authgate.ErrLimited) {
				t.Fatalf("holder: err = %v, want it to wait", err)
			}

			hold()
		default:
		}

		before := next.lookups.Load()

		err := attempt(canceled(t), g, "192.0.2.1", "badgood")
		if errors.Is(err, authgate.ErrLimited) {
			if next.lookups.Load() != before {
				t.Fatal("a request over the waiting cap reached the store")
			}

			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("no request over the waiting cap after 10s: last err = %v", err)
		}
	}

	if !errors.Is(authgate.ErrLimited, identity.ErrRateLimited) {
		t.Fatal("ErrLimited does not wrap ErrRateLimited, so adapters would not answer 429")
	}

	cancel()

	if err := <-done; !errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("waiting request after its client went away: err = %v, want ErrRateLimited", err)
	}
}

func TestOnlyChargedLookupsUseTheBudget(t *testing.T) {
	t.Parallel()

	next := &countingAuthenticator{lookups: atomic.Int64{}}
	g := newGate(t, next, newClock(), gateOptions{penalty: time.Hour, maxWaiting: 0, maxSources: 0})

	for i := range 10 {
		if err := attempt(canceled(t), g, "192.0.2.1", "other"); errors.Is(err, identity.ErrRateLimited) {
			t.Fatalf("uncharged miss %d waited", i+1)
		}
	}

	if overBudget(t, g, "192.0.2.1") {
		t.Fatal("uncharged misses used the budget")
	}
}

func TestIPv6SourcesShareTheirSlash64(t *testing.T) {
	t.Parallel()

	next := &countingAuthenticator{lookups: atomic.Int64{}}
	g := newGate(t, next, newClock(), gateOptions{penalty: time.Hour, maxWaiting: 0, maxSources: 0})

	for _, source := range []string{"2001:db8:1:2::1", "2001:db8:1:2::2", "2001:db8:1:2:ffff::3"} {
		if err := attempt(t.Context(), g, source, "bad"); errors.Is(err, identity.ErrRateLimited) {
			t.Fatalf("miss from %s waited inside the burst", source)
		}
	}

	if !overBudget(t, g, "2001:db8:1:2::4") {
		t.Fatal("the /64 was not over the budget after three misses")
	}

	if overBudget(t, g, "2001:db8:1:3::1") {
		t.Fatal("another /64 was over the budget")
	}

	if overBudget(t, g, "::ffff:192.0.2.9") {
		t.Fatal("a mapped IPv4 address was over the budget")
	}
}

func TestMemoResolvesOncePerRequest(t *testing.T) {
	t.Parallel()

	next := &countingAuthenticator{lookups: atomic.Int64{}}
	g := newGate(t, next, newClock(), gateOptions{penalty: time.Hour, maxWaiting: 0, maxSources: 0})
	ctx := authgate.WithRequest(t.Context(), "192.0.2.1")

	for range 3 {
		p, err := g.AuthenticateToken(ctx, "badgood")
		if err != nil || p.TokenID != "tok-1" {
			t.Fatalf("authenticate = %+v, %v", p, err)
		}
	}

	if got := next.lookups.Load(); got != 1 {
		t.Fatalf("lookups = %d, want 1", got)
	}
}

func TestNoRequestStatePassesThrough(t *testing.T) {
	t.Parallel()

	next := &countingAuthenticator{lookups: atomic.Int64{}}
	g := newGate(t, next, newClock(), gateOptions{penalty: time.Hour, maxWaiting: 0, maxSources: 0})

	for range 5 {
		if _, err := g.AuthenticateToken(canceled(t), "bad"); errors.Is(err, identity.ErrRateLimited) {
			t.Fatal("a context without request state waited")
		}
	}

	if got := next.lookups.Load(); got != 5 {
		t.Fatalf("lookups = %d, want 5", got)
	}
}

func TestForgetsTheSourceSeenLeastRecently(t *testing.T) {
	t.Parallel()

	next := &countingAuthenticator{lookups: atomic.Int64{}}
	g := newGate(t, next, newClock(), gateOptions{penalty: time.Hour, maxWaiting: 0, maxSources: 2})

	spend(t, g, "192.0.2.1")
	spend(t, g, "192.0.2.2")

	// A third source pushes out 192.0.2.1, the one seen least recently.
	if err := attempt(t.Context(), g, "192.0.2.3", "bad"); errors.Is(err, identity.ErrRateLimited) {
		t.Fatal("a new source waited")
	}

	if overBudget(t, g, "192.0.2.1") {
		t.Fatal("the source seen least recently was kept")
	}
}

func TestFailSpendsTheBudgetOfTheSource(t *testing.T) {
	t.Parallel()

	next := &countingAuthenticator{lookups: atomic.Int64{}}
	g := newGate(t, next, newClock(), gateOptions{penalty: time.Hour, maxWaiting: 4, maxSources: 0})

	ctx := authgate.WithRequest(t.Context(), "192.0.2.1")

	if err := g.Admit(ctx); err != nil {
		t.Fatalf("admit within the budget: %v", err)
	}

	for range 3 {
		g.Fail(authgate.WithRequest(t.Context(), "192.0.2.1"))
	}

	// Three failures of sign-in callbacks spend the burst of the source,
	// as three failed token lookups do.
	if !overBudget(t, g, "192.0.2.1") {
		t.Fatal("the source was within the budget after three failures")
	}

	if err := g.Admit(authgate.WithRequest(canceled(t), "192.0.2.1")); !errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("admit over the budget with a done context: err = %v, want ErrRateLimited", err)
	}

	if next.lookups.Load() != 0 {
		t.Fatalf("Admit and Fail made %d lookups, want 0", next.lookups.Load())
	}

	if overBudget(t, g, "192.0.2.2") {
		t.Fatal("another source was over the budget")
	}
}

// TestTokenAndSignInFailuresShareTheBudget proves that failed token
// lookups and failed sign-in callbacks of one source spend one budget.
func TestTokenAndSignInFailuresShareTheBudget(t *testing.T) {
	t.Parallel()

	g := newGate(t, &countingAuthenticator{lookups: atomic.Int64{}}, newClock(),
		gateOptions{penalty: time.Hour, maxWaiting: 4, maxSources: 0})

	for i := range 2 {
		if err := attempt(t.Context(), g, "192.0.2.1", "bad"); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Fatalf("miss %d: err = %v, want ErrUnauthenticated", i+1, err)
		}
	}

	// Admit spends no budget, so it probes the source with no side effect.
	if err := g.Admit(authgate.WithRequest(canceled(t), "192.0.2.1")); err != nil {
		t.Fatalf("admit after two failed token lookups: %v", err)
	}

	g.Fail(authgate.WithRequest(t.Context(), "192.0.2.1"))

	if err := g.Admit(authgate.WithRequest(canceled(t), "192.0.2.1")); !errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("admit after two failed token lookups and one failed callback: err = %v, want ErrRateLimited", err)
	}
}

func TestAdmitAndFailWithoutRequestState(t *testing.T) {
	t.Parallel()

	g := newGate(t, &countingAuthenticator{lookups: atomic.Int64{}}, newClock(),
		gateOptions{penalty: time.Hour, maxWaiting: 4, maxSources: 0})

	for range 10 {
		g.Fail(t.Context())
	}

	if err := g.Admit(canceled(t)); err != nil {
		t.Fatalf("admit with no request state: %v", err)
	}
}

// scopedCounter is a FailureCounter that keeps the count of each key.
type scopedCounter struct {
	counts map[string]int64
	mu     sync.Mutex
}

func (c *scopedCounter) AddFailure(
	_ context.Context, source string, length time.Duration,
) (authgate.FailureCount, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.counts[source]++

	return authgate.FailureCount{At: time.Time{}, End: time.Time{}.Add(length), Count: c.counts[source]}, nil
}

func (c *scopedCounter) PruneFailures(context.Context, time.Duration) (int64, error) { return 0, nil }

func (c *scopedCounter) PruneFailuresAt(context.Context, time.Time, time.Duration) (int64, error) {
	return 0, nil
}

func TestScopesKeepSeparateSharedCounts(t *testing.T) {
	t.Parallel()

	counter := &scopedCounter{counts: map[string]int64{}, mu: sync.Mutex{}}

	for _, scope := range []string{"", "signin"} {
		g, err := authgate.New(authgate.Config{
			Next: &countingAuthenticator{lookups: atomic.Int64{}}, Failures: counter, Scope: scope,
			Charge: nil, Logger: nil, Now: nil, Window: 0, CounterErrors: nil, Burst: 0, Refill: 0, Penalty: 0,
			MaxWaiting: 0, MaxSources: 0,
		})
		if err != nil {
			t.Fatalf("new gate: %v", err)
		}

		g.Fail(authgate.WithRequest(t.Context(), "192.0.2.1"))
	}

	counter.mu.Lock()
	defer counter.mu.Unlock()

	if counter.counts["192.0.2.1"] != 1 || counter.counts["signin|192.0.2.1"] != 1 || len(counter.counts) != 2 {
		t.Fatalf("counts = %v, want one under the source and one under the scope and source", counter.counts)
	}
}

// TestSharedBudgetStaysInItsScope exhausts the shared budget of one scope and
// checks that another scope, with the same source and the same counter, is
// still usable. The exhausted scope must stay refused after its tokens refill.
// Only the shared mark keeps it refused, so this test fails if the mark is
// not set.
func TestSharedBudgetStaysInItsScope(t *testing.T) {
	t.Parallel()

	counter := &scopedCounter{counts: map[string]int64{}, mu: sync.Mutex{}}
	c := newClock()

	newScoped := func(t *testing.T, scope string) *authgate.Gate {
		t.Helper()

		g, err := authgate.New(authgate.Config{
			Next: &countingAuthenticator{lookups: atomic.Int64{}}, Failures: counter, Scope: scope,
			Charge: nil, Logger: nil, Now: c.Now, Window: time.Minute, CounterErrors: nil,
			Burst: 2, Refill: time.Second, Penalty: time.Hour, MaxWaiting: 0, MaxSources: 0,
		})
		if err != nil {
			t.Fatalf("new gate: %v", err)
		}

		return g
	}

	exhausted := newScoped(t, "")
	other := newScoped(t, "signin")
	// A canceled request ends the penalty wait of an over-budget source at once,
	// so no test waits for the penalty.
	ctx := authgate.WithRequest(canceled(t), "192.0.2.1")

	// The second miss reaches the burst, so the scope is marked as shared.
	exhausted.Fail(ctx)
	exhausted.Fail(ctx)

	// One token refills, but the shared mark still holds for the window.
	c.Advance(time.Second)

	if err := exhausted.Admit(ctx); err == nil {
		t.Fatal("exhausted scope admitted within the shared window")
	}

	// One miss of the other scope: it counts under its own key, below the burst.
	other.Fail(ctx)

	if err := other.Admit(ctx); err != nil {
		t.Fatalf("other scope refused by the shared budget of its own scope: %v", err)
	}

	counter.mu.Lock()
	defer counter.mu.Unlock()

	if counter.counts["192.0.2.1"] != 2 || counter.counts["signin|192.0.2.1"] != 1 {
		t.Fatalf("counts = %v, want two misses under the exhausted scope and one under the other", counter.counts)
	}
}
