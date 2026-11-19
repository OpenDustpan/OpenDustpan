package trustrulesvc_test

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// guardClock is a clock that tests move.
type guardClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *guardClock { return &guardClock{mu: sync.Mutex{}, t: exchangeNow()} }

func (c *guardClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.t
}

func (c *guardClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.t = c.t.Add(d)
}

// newGuard returns a guard with a body cap of 1024 bytes, 3 calls for each
// key, 10 calls in all, a window of one minute and 4 keys. change edits the
// config first.
func newGuard(
	t *testing.T, clock *guardClock, change func(*trustrulesvc.MtlsGuardConfig),
) *trustrulesvc.MtlsGuard {
	t.Helper()

	cfg := trustrulesvc.MtlsGuardConfig{
		MaxBodyBytes: 1024, Limit: 3, GlobalLimit: 10, Window: time.Minute, MaxKeys: 4, Now: clock.now,
	}
	if change != nil {
		change(&cfg)
	}

	g, err := trustrulesvc.NewMtlsGuard(cfg)
	if err != nil {
		t.Fatalf("guard: %v", err)
	}

	return g
}

// countLimited makes n calls of Admit with the keys that key returns, and
// returns the number of calls that the rate limit refused.
func countLimited(g *trustrulesvc.MtlsGuard, n int, key func(int) string) int {
	var limited int

	for i := range n {
		if err := g.Admit(key(i), 0); errors.Is(err, trustrulesvc.ErrRateLimited) {
			limited++
		}
	}

	return limited
}

// TestMtlsExchangeLimitHostile floods the guard and sends it bodies of bad
// size.
func TestMtlsExchangeLimitHostile(t *testing.T) {
	t.Parallel()

	t.Run("a flood of calls with no certificate", testFloodNoCertificate)
	t.Run("an oversize body is refused", testOversizeBody)
}

func testFloodNoCertificate(t *testing.T) {
	t.Parallel()

	g := newGuard(t, newClock(), nil)

	if limited := countLimited(g, 50, func(int) string { return "192.0.2.1" }); limited != 47 {
		t.Fatalf("limited = %d, want 47", limited)
	}
}

func testOversizeBody(t *testing.T) {
	t.Parallel()

	g := newGuard(t, newClock(), nil)

	for _, n := range []int64{1025, 1 << 40, -1} {
		if err := g.Admit("k", n); !errors.Is(err, trustrulesvc.ErrBodyTooLarge) {
			t.Fatalf("body %d: error = %v", n, err)
		}
	}

	if err := g.Admit("k", 1024); err != nil {
		t.Fatalf("body at the cap: %v", err)
	}
}

// TestMtlsGuardRate sends calls of many keys to the rate limit and the key
// bound.
func TestMtlsGuardRate(t *testing.T) {
	t.Parallel()

	t.Run("the key limit is per key and resets with the window", testKeyLimit)
	t.Run("the global limit holds for many keys", testGlobalLimit)
	t.Run("memory of keys is bounded", testKeyBound)
	t.Run("keys beyond the bound share one limit", testOverflowKey)
	t.Run("concurrent calls respect the limit", testConcurrentLimit)
}

func testKeyLimit(t *testing.T) {
	t.Parallel()

	clock := newClock()
	g := newGuard(t, clock, nil)

	if n := countLimited(g, 4, func(int) string { return "a" }); n != 1 {
		t.Fatalf("limited = %d, want 1", n)
	}

	if err := g.Admit("b", 0); err != nil {
		t.Fatalf("other key: %v", err)
	}

	clock.advance(time.Minute)

	if err := g.Admit("a", 0); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

func testGlobalLimit(t *testing.T) {
	t.Parallel()

	g := newGuard(t, newClock(), func(c *trustrulesvc.MtlsGuardConfig) { c.MaxKeys = 100 })

	if n := countLimited(g, 30, func(i int) string { return strings.Repeat("k", i+1) }); n != 20 {
		t.Fatalf("limited = %d, want 20", n)
	}
}

func testKeyBound(t *testing.T) {
	t.Parallel()

	g := newGuard(t, newClock(),
		func(c *trustrulesvc.MtlsGuardConfig) { c.GlobalLimit = 1_000_000 })

	for i := range 1000 {
		countLimited(g, 1, func(int) string { return strings.Repeat("x", 1000) + strings.Repeat("y", i) })

		if g.Tracked() > 4 {
			t.Fatalf("tracked = %d, want at most 4", g.Tracked())
		}
	}
}

func testOverflowKey(t *testing.T) {
	t.Parallel()

	g := newGuard(t, newClock(),
		func(c *trustrulesvc.MtlsGuardConfig) { c.GlobalLimit = 1_000_000 })

	if n := countLimited(g, 4, func(i int) string { return strings.Repeat("k", i+1) }); n != 0 {
		t.Fatalf("limited = %d before the bound", n)
	}

	if n := countLimited(g, 10, func(i int) string { return strings.Repeat("z", i+1) }); n != 7 {
		t.Fatalf("limited = %d, want 7", n)
	}
}

func testConcurrentLimit(t *testing.T) {
	t.Parallel()

	g := newGuard(t, newClock(),
		func(c *trustrulesvc.MtlsGuardConfig) { c.Limit, c.GlobalLimit = 5, 1000 })

	var (
		wg      sync.WaitGroup
		allowed atomic.Int64
	)

	for range 50 {
		wg.Go(func() {
			if g.Admit("same", 0) == nil {
				allowed.Add(1)
			}
		})
	}

	wg.Wait()

	if allowed.Load() != 5 {
		t.Fatalf("allowed = %d, want 5", allowed.Load())
	}
}

// TestMtlsGuardConfig sends bad settings to the constructor.
func TestMtlsGuardConfig(t *testing.T) {
	t.Parallel()

	for name, change := range map[string]func(*trustrulesvc.MtlsGuardConfig){
		"body":   func(c *trustrulesvc.MtlsGuardConfig) { c.MaxBodyBytes = -1 },
		"limit":  func(c *trustrulesvc.MtlsGuardConfig) { c.Limit = 0 },
		"global": func(c *trustrulesvc.MtlsGuardConfig) { c.GlobalLimit = 0 },
		"window": func(c *trustrulesvc.MtlsGuardConfig) { c.Window = 0 },
		"keys":   func(c *trustrulesvc.MtlsGuardConfig) { c.MaxKeys = 0 },
	} {
		cfg := trustrulesvc.MtlsGuardConfig{
			MaxBodyBytes: 1, Limit: 1, GlobalLimit: 1, Window: time.Second, MaxKeys: 1, Now: nil,
		}
		change(&cfg)

		if _, err := trustrulesvc.NewMtlsGuard(cfg); !errors.Is(err, trustrule.ErrMissingDependency) {
			t.Errorf("%s: error = %v", name, err)
		}
	}
}
