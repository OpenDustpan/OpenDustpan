package trustrulesvc_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// errDiskFull is the store error of the guard tests.
var errDiskFull = errors.New("disk full")

// noResult returns the result of a refused exchange: the zero value.
func noResult() trustrulesvc.MtlsExchangeResult {
	var zero trustrulesvc.MtlsExchangeResult

	return zero
}

// countingExchanger counts calls and answers with res and err.
type countingExchanger struct {
	calls atomic.Int64
	res   trustrulesvc.MtlsExchangeResult
	err   error
}

func newCounter(res trustrulesvc.MtlsExchangeResult, err error) *countingExchanger {
	return &countingExchanger{calls: atomic.Int64{}, res: res, err: err}
}

func (c *countingExchanger) Exchange(
	context.Context, *trustrulesvc.MtlsExchangeInput,
) (trustrulesvc.MtlsExchangeResult, error) {
	c.calls.Add(1)

	return c.res, c.err
}

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
	t *testing.T, ex trustrulesvc.MtlsExchanging, clock *guardClock, change func(*trustrulesvc.MtlsGuardConfig),
) *trustrulesvc.MtlsGuard {
	t.Helper()

	cfg := trustrulesvc.MtlsGuardConfig{
		Exchanger: ex, MaxBodyBytes: 1024, Limit: 3, GlobalLimit: 10, Window: time.Minute, MaxKeys: 4, Now: clock.now,
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

// emptyInput is a call with no certificate.
func emptyInput() *trustrulesvc.MtlsExchangeInput {
	return &trustrulesvc.MtlsExchangeInput{
		Namespace: "acme",
		Chain:     trustrulesvc.VerifiedChain{},
		Request:   emptyEntry(),
	}
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

// TestMtlsExchangeLimitHostile floods the guard. Each call gets no token.
func TestMtlsExchangeLimitHostile(t *testing.T) {
	t.Parallel()

	t.Run("a flood of calls with no certificate", testFloodNoCertificate)
	t.Run("an oversize body never reaches the exchange", testOversizeBody)
	t.Run("a store error gives no token", testStoreError)
	t.Run("a refusal keeps its cause", testRefusalCause)
	t.Run("a success passes", testSuccessPasses)
}

func testFloodNoCertificate(t *testing.T) {
	t.Parallel()

	e := newMtlsEnv(t)
	g := newGuard(t, e.exchanger(nil), newClock(), nil)

	var limited int

	for range 50 {
		res, err := g.Exchange(t.Context(), "192.0.2.1", 0, emptyInput())
		if res.Secret.Reveal() != "" || err == nil {
			t.Fatal("a call with no certificate got a token")
		}

		if errors.Is(err, trustrulesvc.ErrRateLimited) {
			limited++
		}
	}

	if limited != 47 {
		t.Fatalf("limited = %d, want 47", limited)
	}

	if n := e.tokenCount(e.worker); n != 0 {
		t.Fatalf("tokens = %d", n)
	}
}

func testOversizeBody(t *testing.T) {
	t.Parallel()

	ex := newCounter(noResult(), nil)
	g := newGuard(t, ex, newClock(), nil)

	for _, n := range []int64{1025, 1 << 40, -1} {
		if _, err := g.Exchange(t.Context(), "k", n, emptyInput()); !errors.Is(err, trustrulesvc.ErrBodyTooLarge) {
			t.Fatalf("body %d: error = %v", n, err)
		}
	}

	if ex.calls.Load() != 0 {
		t.Fatalf("the exchange ran %d times", ex.calls.Load())
	}
}

func testStoreError(t *testing.T) {
	t.Parallel()

	var partial trustrulesvc.MtlsExchangeResult

	partial.Token.ID = "tok"

	g := newGuard(t, newCounter(partial, errDiskFull), newClock(), nil)

	res, err := g.Exchange(t.Context(), "k", 0, emptyInput())
	if !errors.Is(err, trustrulesvc.ErrIssueFailed) || res.Token.ID != "" {
		t.Fatalf("result = %+v, error = %v", res, err)
	}
}

func testRefusalCause(t *testing.T) {
	t.Parallel()

	for _, cause := range []error{trustrulesvc.ErrNoCertificate, trustrulesvc.ErrExpired, trustrule.ErrPrincipal} {
		g := newGuard(t, newCounter(noResult(), cause), newClock(), nil)

		if _, err := g.Exchange(t.Context(), "k", 0, emptyInput()); !errors.Is(err, cause) {
			t.Fatalf("error = %v, want %v", err, cause)
		}
	}
}

func testSuccessPasses(t *testing.T) {
	t.Parallel()

	var ok trustrulesvc.MtlsExchangeResult

	var tok accesstoken.Token

	tok.ID = "tok"
	ok.Token = tok

	g := newGuard(t, newCounter(ok, nil), newClock(), nil)

	if res, err := g.Exchange(t.Context(), "k", 0, emptyInput()); err != nil || res.Token.ID != "tok" {
		t.Fatalf("result = %+v, error = %v", res, err)
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
	g := newGuard(t, newCounter(noResult(), nil), clock, nil)

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

	g := newGuard(t, newCounter(noResult(), nil), newClock(), func(c *trustrulesvc.MtlsGuardConfig) { c.MaxKeys = 100 })

	if n := countLimited(g, 30, func(i int) string { return strings.Repeat("k", i+1) }); n != 20 {
		t.Fatalf("limited = %d, want 20", n)
	}
}

func testKeyBound(t *testing.T) {
	t.Parallel()

	g := newGuard(t, newCounter(noResult(), nil), newClock(),
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

	g := newGuard(t, newCounter(noResult(), nil), newClock(),
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

	g := newGuard(t, newCounter(noResult(), nil), newClock(),
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

	ex := newCounter(noResult(), nil)

	for name, change := range map[string]func(*trustrulesvc.MtlsGuardConfig){
		"no exchanger": func(c *trustrulesvc.MtlsGuardConfig) { c.Exchanger = nil },
		"body":         func(c *trustrulesvc.MtlsGuardConfig) { c.MaxBodyBytes = -1 },
		"limit":        func(c *trustrulesvc.MtlsGuardConfig) { c.Limit = 0 },
		"global":       func(c *trustrulesvc.MtlsGuardConfig) { c.GlobalLimit = 0 },
		"window":       func(c *trustrulesvc.MtlsGuardConfig) { c.Window = 0 },
		"keys":         func(c *trustrulesvc.MtlsGuardConfig) { c.MaxKeys = 0 },
	} {
		cfg := trustrulesvc.MtlsGuardConfig{
			Exchanger: ex, MaxBodyBytes: 1, Limit: 1, GlobalLimit: 1, Window: time.Second, MaxKeys: 1, Now: nil,
		}
		change(&cfg)

		if _, err := trustrulesvc.NewMtlsGuard(cfg); !errors.Is(err, trustrule.ErrMissingDependency) {
			t.Errorf("%s: error = %v", name, err)
		}
	}
}
