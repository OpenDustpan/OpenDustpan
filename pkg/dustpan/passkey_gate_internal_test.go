package dustpan

import (
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// gateClock is a settable clock.
type gateClock struct {
	now time.Time
	mu  sync.Mutex
}

func (c *gateClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *gateClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// TestPasskeyFailureGateBudget checks the budget of the failed responses of a
// passkey sign-in: 19 failures leave the source in, the 20th refuses it at
// once with no wait, another source stays in, and the source is in again after
// the window of 15 minutes.
func TestPasskeyFailureGateBudget(t *testing.T) {
	t.Parallel()

	clock := &gateClock{now: time.Date(2026, time.November, 6, 12, 0, 0, 0, time.UTC), mu: sync.Mutex{}}

	gate, err := newPasskeyFailureGate(nil, slog.New(slog.DiscardHandler), clock.Now)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}

	source := "192.0.2.7"

	for i := range passkeyFailureLimit - 1 {
		gate.Fail(authgate.WithRequest(t.Context(), source))

		if admitErr := gate.Admit(authgate.WithRequest(t.Context(), source)); admitErr != nil {
			t.Fatalf("admit after %d failures: %v", i+1, admitErr)
		}
	}

	gate.Fail(authgate.WithRequest(t.Context(), source))

	started := time.Now()
	err = gate.Admit(authgate.WithRequest(t.Context(), source))

	if !errors.Is(err, authgate.ErrLimited) || !errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("admit after %d failures: err = %v, want ErrLimited", passkeyFailureLimit, err)
	}

	if time.Since(started) > time.Second {
		t.Errorf("the refusal waited %v, want no wait", time.Since(started))
	}

	if err := gate.Admit(authgate.WithRequest(t.Context(), "192.0.2.8")); err != nil {
		t.Errorf("another source: %v", err)
	}

	clock.Advance(passkeyFailureWindow - time.Minute)

	if err := gate.Admit(authgate.WithRequest(t.Context(), source)); err == nil {
		t.Error("the source was in again before the window ended")
	}

	clock.Advance(2 * time.Minute)

	if err := gate.Admit(authgate.WithRequest(t.Context(), source)); err != nil {
		t.Errorf("the source after the window: %v", err)
	}
}
