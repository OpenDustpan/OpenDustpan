package trustrulesvc_test

import (
	"errors"
	"fmt"
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
		if _, err := g.Admit(key(i), "acme", 0); errors.Is(err, trustrulesvc.ErrRateLimited) {
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

	g := newGuard(t, newClock(), func(c *trustrulesvc.MtlsGuardConfig) { c.Limit, c.GlobalLimit = 10, 1000 })

	for _, n := range []int64{1025, 1 << 40, -1} {
		if _, err := g.Admit("k", "acme", n); !errors.Is(err, trustrulesvc.ErrBodyTooLarge) {
			t.Fatalf("body %d: error = %v", n, err)
		}
	}

	if _, err := g.Admit("k", "acme", 1024); err != nil {
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

	if _, err := g.Admit("b", "acme", 0); err != nil {
		t.Fatalf("other key: %v", err)
	}

	clock.advance(time.Minute)

	if _, err := g.Admit("a", "acme", 0); err != nil {
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
			if _, err := g.Admit("same", "acme", 0); err == nil {
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

// TestMtlsGuardFirstRefusal checks that Admit reports the first refusal of a
// window once, so the caller does work for a flood at most once for each
// window.
func TestMtlsGuardFirstRefusal(t *testing.T) {
	t.Parallel()

	t.Run("one first refusal for each window of a key", testFirstRefusalPerWindow)
	t.Run("one first refusal while all keys are over the global limit", testFirstRefusalGlobal)
	t.Run("an admitted call is never a first refusal", testAdmittedNotFirst)
	t.Run("an oversize body is a first refusal once for each window", testFirstRefusalOversize)
}

func testFirstRefusalPerWindow(t *testing.T) {
	t.Parallel()

	clock := newClock()
	g := newGuard(t, clock, func(c *trustrulesvc.MtlsGuardConfig) { c.Limit, c.GlobalLimit = 2, 1000 })

	if got := countFirst(g, 20, func(int) string { return "a" }); got != 1 {
		t.Fatalf("first refusals = %d, want 1", got)
	}

	clock.advance(time.Minute)

	if got := countFirst(g, 20, func(int) string { return "a" }); got != 1 {
		t.Fatalf("first refusals in the next window = %d, want 1", got)
	}
}

func testFirstRefusalGlobal(t *testing.T) {
	t.Parallel()

	g := newGuard(t, newClock(), func(c *trustrulesvc.MtlsGuardConfig) { c.Limit, c.GlobalLimit = 1, 3 })

	got := countFirst(g, 100, func(i int) string { return fmt.Sprintf("key-%d", i) })
	if got != 1 {
		t.Fatalf("first refusals = %d, want 1", got)
	}
}

func testAdmittedNotFirst(t *testing.T) {
	t.Parallel()

	g := newGuard(t, newClock(), nil)

	if r, err := g.Admit("a", "acme", 0); r.First || err != nil {
		t.Fatalf("first = %v, err = %v, want false and nil", r.First, err)
	}
}

func testFirstRefusalOversize(t *testing.T) {
	t.Parallel()

	g := newGuard(t, newClock(), func(c *trustrulesvc.MtlsGuardConfig) { c.Limit, c.GlobalLimit = 100, 1000 })

	var got int

	for range 10 {
		if r, err := g.Admit("a", "acme", 1<<40); r.First && errors.Is(err, trustrulesvc.ErrBodyTooLarge) {
			got++
		}
	}

	if got != 1 {
		t.Fatalf("first refusals = %d, want 1", got)
	}
}

// countFirst makes n calls of Admit with the keys that key returns, and
// returns the number of first refusals.
func countFirst(g *trustrulesvc.MtlsGuard, n int, key func(int) string) int {
	var got int

	for i := range n {
		if r, err := g.Admit(key(i), "acme", 0); r.First && err != nil {
			got++
		}
	}

	return got
}

// TestMtlsGuardRearm checks that a refusal that the caller gives back is a
// first refusal again, and that the counts of the guard reach the next one.
func TestMtlsGuardRearm(t *testing.T) {
	t.Parallel()

	t.Run("a refusal given back is first again", testRearmFirstAgain)
	t.Run("a name that does not exist keeps no slot", testRearmNotFound)
	t.Run("names beyond the bound share one slot", testSlotBound)
	t.Run("a held count reaches the next first refusal", testHeldCarry)
}

func rearmGuard(t *testing.T) (*trustrulesvc.MtlsGuard, *guardClock) {
	t.Helper()

	clock := newClock()

	return newGuard(t, clock, func(c *trustrulesvc.MtlsGuardConfig) { c.Limit, c.GlobalLimit = 1, 1000 }), clock
}

// admitOK makes a call that the guard must admit.
func admitOK(t *testing.T, g *trustrulesvc.MtlsGuard, ns string) {
	t.Helper()

	if _, err := g.Admit("a", ns, 0); err != nil {
		t.Fatalf("Admit(%q): %v, want nil", ns, err)
	}
}

// admitRefused makes a call that the guard must refuse.
func admitRefused(t *testing.T, g *trustrulesvc.MtlsGuard, ns string) trustrulesvc.Refusal {
	t.Helper()

	r, err := g.Admit("a", ns, 0)
	if err == nil {
		t.Fatalf("Admit(%q) = nil, want a refusal", ns)
	}

	return r
}

func testRearmFirstAgain(t *testing.T) {
	t.Parallel()

	g, _ := rearmGuard(t)

	admitOK(t, g, "acme")

	r := admitRefused(t, g, "acme")
	if !r.First {
		t.Fatal("the first refusal is not first")
	}

	if admitRefused(t, g, "acme").First {
		t.Fatal("a second refusal is first before Rearm")
	}

	g.Rearm(r)

	if !admitRefused(t, g, "acme").First {
		t.Fatal("the refusal after Rearm is not first")
	}
}

func testRearmNotFound(t *testing.T) {
	t.Parallel()

	g, _ := rearmGuard(t)

	admitOK(t, g, "ghost")

	ghost := admitRefused(t, g, "ghost")
	if !ghost.First {
		t.Fatal("the refusal for ghost is not first")
	}

	g.Forget(ghost)

	if !admitRefused(t, g, "acme").First {
		t.Fatal("a refusal for another namespace is hidden by the one for ghost")
	}
}

func testSlotBound(t *testing.T) {
	t.Parallel()

	g, _ := rearmGuard(t)

	admitOK(t, g, "n0")

	var first int

	for i := range 40 {
		if admitRefused(t, g, fmt.Sprintf("n%d", i)).First {
			first++
		}
	}

	if first != 9 {
		t.Fatalf("first refusals = %d, want 9 (8 names and one shared slot)", first)
	}
}

func testHeldCarry(t *testing.T) {
	t.Parallel()

	g, clock := rearmGuard(t)

	admitOK(t, g, "acme")

	for range 5 {
		admitRefused(t, g, "acme")
	}

	clock.advance(time.Hour)

	admitOK(t, g, "acme")

	r := admitRefused(t, g, "acme")
	if !r.First {
		t.Fatal("the refusal of the new window is not first")
	}

	if len(r.Held) != 1 || r.Held[0].Namespace != "acme" || r.Held[0].Count != 4 {
		t.Fatalf("held = %+v, want acme 4", r.Held)
	}
}

// TestMtlsGuardOversizeCounts checks that a call with a body that is too
// large counts toward the rate limit of its key.
func TestMtlsGuardOversizeCounts(t *testing.T) {
	t.Parallel()

	g := newGuard(t, newClock(), func(c *trustrulesvc.MtlsGuardConfig) { c.Limit, c.GlobalLimit = 3, 1000 })

	for range 3 {
		if _, err := g.Admit("a", "acme", 1<<40); !errors.Is(err, trustrulesvc.ErrBodyTooLarge) {
			t.Fatalf("oversize call: %v, want ErrBodyTooLarge", err)
		}
	}

	if _, err := g.Admit("a", "acme", 0); !errors.Is(err, trustrulesvc.ErrRateLimited) {
		t.Fatalf("normal call after the oversize calls: %v, want ErrRateLimited", err)
	}
}
