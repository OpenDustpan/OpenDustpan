package authgate_test

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/authgate/authgatetest"
)

// newScopedGate returns a gate of scope with a burst of 3 on failures.
func newScopedGate(t *testing.T, c *clock, failures authgate.FailureCounter, scope string) (*authgate.Gate, error) {
	t.Helper()

	return authgate.New(authgate.Config{
		Next: &countingAuthenticator{lookups: atomic.Int64{}}, Charge: chargeBad, Logger: nil, Now: c.Now,
		Failures: failures, Window: 0, CounterErrors: nil, Scope: scope,
		Burst: 3, Refill: time.Second, Penalty: time.Hour, MaxWaiting: 0, MaxSources: 0,
	})
}

// TestNewChecksTheScope proves that New takes an empty scope and a scope of
// the letters a to z, and refuses every other scope.
func TestNewChecksTheScope(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		scope string
		ok    bool
	}{
		{name: "empty", scope: "", ok: true},
		{name: "letters", scope: "signin", ok: true},
		{name: "one letter", scope: "a", ok: true},
		{name: "separator", scope: "a|b", ok: false},
		{name: "only the separator", scope: "|", ok: false},
		{name: "upper case", scope: "SignIn", ok: false},
		{name: "digit", scope: "signin2", ok: false},
		{name: "hyphen", scope: "sign-in", ok: false},
		{name: "space", scope: "sign in", ok: false},
		{name: "line feed", scope: "signin\n", ok: false},
		{name: "non-ASCII letter", scope: "sïgnin", ok: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := newScopedGate(t, newClock(), nil, tc.scope)

			switch {
			case tc.ok && err != nil:
				t.Errorf("scope %q: err = %v, want nil", tc.scope, err)
			case !tc.ok && !errors.Is(err, authgate.ErrInvalidScope):
				t.Errorf("scope %q: err = %v, want ErrInvalidScope", tc.scope, err)
			default:
			}
		})
	}
}

// TestSourceWithSeparatorIsUnknown proves that a source that holds the scope
// separator cannot spend the budget of another scope in the shared count,
// and that all such sources count as one unknown source.
func TestSourceWithSeparatorIsUnknown(t *testing.T) {
	t.Parallel()

	t.Run("forged scope spends nothing of the other scope", func(t *testing.T) {
		t.Parallel()

		c := newClock()
		failures := authgatetest.NewMemory(c.Now)
		gates := newTwoGates(t, c, failures)
		token, signIn := gates.token, gates.signIn

		for range 3 {
			miss(t, token, "signin|192.0.2.7")
		}

		// A gate reads the shared count on its own miss, so one miss first.
		miss(t, signIn, "192.0.2.7")

		if overBudget(t, signIn, "192.0.2.7") {
			t.Fatal("sign-in gate after forged misses on the token gate: over budget, want within")
		}
	})

	t.Run("all such sources share one budget", func(t *testing.T) {
		t.Parallel()

		c := newClock()
		token := newTwoGates(t, c, authgatetest.NewMemory(c.Now)).token

		spend(t, token, "a|b")

		if !overBudget(t, token, "c|d") {
			t.Fatal("another source with a separator: within, want over budget as the unknown source")
		}

		if overBudget(t, token, "192.0.2.9") {
			t.Fatal("a plain address: over budget, want within")
		}
	})

	t.Run("no plain source shares the unknown budget", func(t *testing.T) {
		t.Parallel()

		for _, plain := range []string{"unknown", "signin", "192.0.2.10"} {
			c := newClock()
			token := newTwoGates(t, c, authgatetest.NewMemory(c.Now)).token

			spend(t, token, "a|b")

			if overBudget(t, token, plain) {
				t.Errorf("source %q after a source with a separator spent its budget: over budget, want within", plain)
			}
		}
	})
}

// twoGates is the two gates of newTwoGates.
type twoGates struct {
	token, signIn *authgate.Gate
}

// newTwoGates returns a gate of the empty scope and a gate of the scope
// "signin" on one counter.
func newTwoGates(t *testing.T, c *clock, failures authgate.FailureCounter,
) twoGates {
	t.Helper()

	token, err := newScopedGate(t, c, failures, "")
	if err != nil {
		t.Fatalf("token gate: %v", err)
	}

	signIn, err := newScopedGate(t, c, failures, "signin")
	if err != nil {
		t.Fatalf("sign-in gate: %v", err)
	}

	return twoGates{token: token, signIn: signIn}
}

// TestScopeSpendLeavesOtherScopeUnmarked proves that spending one scope up
// to the burst marks the source over the budget on the gates of that scope
// only: the gate of another scope on the same counter still has its budget.
func TestScopeSpendLeavesOtherScopeUnmarked(t *testing.T) {
	t.Parallel()

	c := newClock()
	failures := authgatetest.NewMemory(c.Now)
	gates := newTwoGates(t, c, failures)
	token, signIn := gates.token, gates.signIn
	other, err := newScopedGate(t, c, failures, "signin")
	if err != nil {
		t.Fatalf("second sign-in gate: %v", err)
	}

	spend(t, signIn, "192.0.2.7")

	if !overBudget(t, signIn, "192.0.2.7") {
		t.Fatal("spent scope: within, want over budget")
	}

	// The second gate of the spent scope reads the shared count on its first
	// miss, so it marks the source over the budget too.
	miss(t, other, "192.0.2.7")

	if !overBudget(t, other, "192.0.2.7") {
		t.Fatal("second gate of the spent scope: within, want over budget")
	}

	// The token gate shares no key with that scope, so its first misses stay
	// within the burst.
	miss(t, token, "192.0.2.7")

	if overBudget(t, token, "192.0.2.7") {
		t.Fatal("token gate after the other scope spent its burst: over budget, want within")
	}
}
