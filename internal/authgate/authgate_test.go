package authgate_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// countingAuthenticator accepts the token "good" and counts its lookups.
type countingAuthenticator struct {
	lookups atomic.Int64
}

func (c *countingAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	c.lookups.Add(1)

	if token != "good" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{Subject: "ci", DisplayName: "CI", Admin: false, TokenID: "tok-1"}, nil
}

// clock is a settable test clock.
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

func newGate(t *testing.T, next identity.Authenticator, c *clock, maxSources int) *authgate.Gate {
	t.Helper()

	g, err := authgate.New(authgate.Config{
		Next: next, Logger: nil, Now: c.Now, Burst: 3, Refill: time.Second, MaxSources: maxSources,
	})
	if err != nil {
		t.Fatalf("new gate: %v", err)
	}

	return g
}

func newClock() *clock {
	return &clock{now: time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC), mu: sync.Mutex{}}
}

func TestNewNeedsNext(t *testing.T) {
	t.Parallel()

	_, err := authgate.New(authgate.Config{Next: nil, Logger: nil, Now: nil, Burst: 0, Refill: 0, MaxSources: 0})
	if !errors.Is(err, authgate.ErrMissingNext) {
		t.Fatalf("err = %v, want ErrMissingNext", err)
	}
}

func TestLimitsFailuresOfOneSource(t *testing.T) {
	t.Parallel()

	next := &countingAuthenticator{lookups: atomic.Int64{}}
	c := newClock()
	g := newGate(t, next, c, 0)

	attempt := func(source, token string) error {
		_, err := g.AuthenticateToken(authgate.WithRequest(t.Context(), source), token)

		return err
	}

	for i := range 3 {
		if err := attempt("192.0.2.1", "bad"); !errors.Is(err, identity.ErrUnauthenticated) ||
			errors.Is(err, authgate.ErrLimited) {
			t.Fatalf("failure %d: err = %v, want a plain ErrUnauthenticated", i+1, err)
		}
	}

	// The budget is spent: the next lookup does not reach the store, and a
	// valid token from the same source waits too.
	for _, token := range []string{"bad", "good"} {
		if err := attempt("192.0.2.1", token); !errors.Is(err, authgate.ErrLimited) {
			t.Fatalf("token %q after the burst: err = %v, want ErrLimited", token, err)
		}
	}

	if got := next.lookups.Load(); got != 3 {
		t.Fatalf("lookups = %d, want 3", got)
	}

	if !errors.Is(authgate.ErrLimited, identity.ErrUnauthenticated) {
		t.Fatal("ErrLimited does not wrap ErrUnauthenticated, so adapters would not answer 401")
	}

	// Another source keeps its budget.
	if err := attempt("192.0.2.2", "good"); err != nil {
		t.Fatalf("other source: %v", err)
	}

	// One refill period gives one lookup back.
	c.Advance(time.Second)

	if err := attempt("192.0.2.1", "good"); err != nil {
		t.Fatalf("after one refill: %v", err)
	}
}

func TestSuccessUsesNoBudget(t *testing.T) {
	t.Parallel()

	next := &countingAuthenticator{lookups: atomic.Int64{}}
	g := newGate(t, next, newClock(), 0)

	for i := range 10 {
		if _, err := g.AuthenticateToken(authgate.WithRequest(t.Context(), "192.0.2.1"), "good"); err != nil {
			t.Fatalf("success %d: %v", i+1, err)
		}
	}
}

func TestMemoResolvesOncePerRequest(t *testing.T) {
	t.Parallel()

	next := &countingAuthenticator{lookups: atomic.Int64{}}
	g := newGate(t, next, newClock(), 0)
	ctx := authgate.WithRequest(t.Context(), "192.0.2.1")

	for range 3 {
		p, err := g.AuthenticateToken(ctx, "good")
		if err != nil || p.TokenID != "tok-1" {
			t.Fatalf("authenticate = %+v, %v", p, err)
		}
	}

	if got := next.lookups.Load(); got != 1 {
		t.Fatalf("lookups = %d, want 1", got)
	}

	// Another token in the same request is a new lookup, and a failure counts
	// once even when the request asks again.
	for range 2 {
		if _, err := g.AuthenticateToken(ctx, "bad"); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Fatalf("bad token: err = %v", err)
		}
	}

	if got := next.lookups.Load(); got != 2 {
		t.Fatalf("lookups = %d, want 2", got)
	}
}

func TestNoRequestStatePassesThrough(t *testing.T) {
	t.Parallel()

	next := &countingAuthenticator{lookups: atomic.Int64{}}
	g := newGate(t, next, newClock(), 0)

	for range 5 {
		if _, err := g.AuthenticateToken(t.Context(), "bad"); errors.Is(err, authgate.ErrLimited) {
			t.Fatal("a context without request state was limited")
		}
	}

	if got := next.lookups.Load(); got != 5 {
		t.Fatalf("lookups = %d, want 5", got)
	}
}

func TestSourcesStayBounded(t *testing.T) {
	t.Parallel()

	next := &countingAuthenticator{lookups: atomic.Int64{}}
	g := newGate(t, next, newClock(), 2)

	// Three sources spend their budget. The gate keeps two at most, so it
	// forgets one, and every source can still make lookups.
	for _, source := range []string{"a", "b", "c"} {
		for range 3 {
			if _, err := g.AuthenticateToken(authgate.WithRequest(t.Context(), source), "bad"); err == nil {
				t.Fatal("a bad token authenticated")
			}
		}
	}

	forgotten := 0

	for _, source := range []string{"a", "b", "c"} {
		_, err := g.AuthenticateToken(authgate.WithRequest(t.Context(), source), "good")
		if err == nil {
			forgotten++
		}
	}

	if forgotten == 0 {
		t.Fatal("the gate kept every source past MaxSources")
	}
}
