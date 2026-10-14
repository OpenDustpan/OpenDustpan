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

func newClock() *clock {
	return &clock{now: time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC), mu: sync.Mutex{}}
}

// chargeBad charges the misses of tokens that start with "bad", as the
// server charges the misses of secret-shaped values.
func chargeBad(token string) bool { return strings.HasPrefix(token, "bad") }

func newGate(t *testing.T, next identity.Authenticator, c *clock, maxSources int) *authgate.Gate {
	t.Helper()

	g, err := authgate.New(authgate.Config{
		Next: next, Charge: chargeBad, Logger: nil, Now: c.Now, Burst: 3, Refill: time.Second,
		Penalty: time.Millisecond, MaxSources: maxSources,
	})
	if err != nil {
		t.Fatalf("new gate: %v", err)
	}

	return g
}

func attempt(t *testing.T, g *authgate.Gate, source, token string) error {
	t.Helper()

	_, err := g.AuthenticateToken(authgate.WithRequest(t.Context(), source), token)

	return err
}

func TestNewNeedsNext(t *testing.T) {
	t.Parallel()

	_, err := authgate.New(authgate.Config{
		Next: nil, Charge: nil, Logger: nil, Now: nil, Burst: 0, Refill: 0, Penalty: 0, MaxSources: 0,
	})
	if !errors.Is(err, authgate.ErrMissingNext) {
		t.Fatalf("err = %v, want ErrMissingNext", err)
	}
}

func TestLimitsMissesButNeverAValidToken(t *testing.T) {
	t.Parallel()

	next := &countingAuthenticator{lookups: atomic.Int64{}}
	c := newClock()
	g := newGate(t, next, c, 0)

	for i := range 3 {
		if err := attempt(t, g, "192.0.2.1", "bad"); !errors.Is(err, identity.ErrUnauthenticated) ||
			errors.Is(err, authgate.ErrLimited) {
			t.Fatalf("miss %d: err = %v, want a plain ErrUnauthenticated", i+1, err)
		}
	}

	if err := attempt(t, g, "192.0.2.1", "bad"); !errors.Is(err, authgate.ErrLimited) {
		t.Fatalf("miss after the burst: err = %v, want ErrLimited", err)
	}

	if !errors.Is(authgate.ErrLimited, identity.ErrUnauthenticated) {
		t.Fatal("ErrLimited does not wrap ErrUnauthenticated, so adapters would not answer 401")
	}

	// A valid token from the same address passes: the budget never refuses
	// it, so an attacker behind a shared address locks out nobody.
	if err := attempt(t, g, "192.0.2.1", "good"); err != nil {
		t.Fatalf("valid token from a limited address: %v", err)
	}

	// One refill period gives one miss back.
	c.Advance(time.Second)

	if err := attempt(t, g, "192.0.2.1", "bad"); errors.Is(err, authgate.ErrLimited) {
		t.Fatalf("miss after one refill: err = %v, want a plain ErrUnauthenticated", err)
	}
}

func TestOnlyChargedMissesUseTheBudget(t *testing.T) {
	t.Parallel()

	next := &countingAuthenticator{lookups: atomic.Int64{}}
	g := newGate(t, next, newClock(), 0)

	for i := range 10 {
		if err := attempt(t, g, "192.0.2.1", "other"); errors.Is(err, authgate.ErrLimited) {
			t.Fatalf("uncharged miss %d was limited", i+1)
		}
	}

	if err := attempt(t, g, "192.0.2.1", "bad"); errors.Is(err, authgate.ErrLimited) {
		t.Fatal("the first charged miss was limited after uncharged misses")
	}
}

func TestIPv6SourcesShareTheirSlash64(t *testing.T) {
	t.Parallel()

	next := &countingAuthenticator{lookups: atomic.Int64{}}
	g := newGate(t, next, newClock(), 0)

	for _, source := range []string{"2001:db8:1:2::1", "2001:db8:1:2::2", "2001:db8:1:2:ffff::3"} {
		if err := attempt(t, g, source, "bad"); errors.Is(err, authgate.ErrLimited) {
			t.Fatalf("miss from %s was limited inside the burst", source)
		}
	}

	if err := attempt(t, g, "2001:db8:1:2::4", "bad"); !errors.Is(err, authgate.ErrLimited) {
		t.Fatalf("fourth miss from the same /64: err = %v, want ErrLimited", err)
	}

	if err := attempt(t, g, "2001:db8:1:3::1", "bad"); errors.Is(err, authgate.ErrLimited) {
		t.Fatal("a miss from another /64 was limited")
	}

	if err := attempt(t, g, "::ffff:192.0.2.9", "bad"); errors.Is(err, authgate.ErrLimited) {
		t.Fatal("a miss from a mapped IPv4 address was limited")
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
	// forgets one, and that source gets a fresh budget.
	for _, source := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		for range 3 {
			if err := attempt(t, g, source, "bad"); err == nil {
				t.Fatal("a bad token authenticated")
			}
		}
	}

	fresh := 0

	for _, source := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		if err := attempt(t, g, source, "bad"); !errors.Is(err, authgate.ErrLimited) {
			fresh++
		}
	}

	if fresh == 0 {
		t.Fatal("the gate kept every source past MaxSources")
	}
}
