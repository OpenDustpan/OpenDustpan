package storetest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/maintenance"
)

// BusyHarness is the part of a store that the busy case of an enter needs
// and that the port does not give: a way to keep a checked write in flight.
type BusyHarness struct {
	// Hold starts a write that the enter of maintenance mode waits for, and
	// returns when that write runs. The write stays in flight until the
	// returned function runs.
	Hold func(t *testing.T) (release func())
}

// BusyFactory returns a fresh store whose bound for the wait of an enter is
// short, and the harness that holds a write in flight on it.
type BusyFactory func(t *testing.T) (Store, BusyHarness)

// RunEnterBusy checks the enter of a store that bounds its wait for the writes
// in flight. A store that does not wait for them does not run it. For each way
// to enter maintenance mode, an enter that a write in flight keeps waiting
// past the bound reports maintenance.ErrBusy and not maintenance.ErrUnavailable,
// leaves the state normal, and the next enter after the write ended succeeds.
func RunEnterBusy(t *testing.T, newStore BusyFactory) {
	t.Helper()

	enters := []struct {
		name  string
		enter func(t *testing.T, s Store) error
	}{
		{"SetState", func(t *testing.T, s Store) error {
			t.Helper()

			return s.SetState(t.Context(), maintenance.StateMaintenance)
		}},
		{"SetStateIf", func(t *testing.T, s Store) error {
			t.Helper()

			return s.SetStateIf(t.Context(), maintenance.StateNormal, maintenance.StateMaintenance)
		}},
	}

	for _, tc := range enters {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s, harness := newStore(t)
			release := harness.Hold(t)

			err := tc.enter(t, s)
			if !errors.Is(err, maintenance.ErrBusy) || errors.Is(err, maintenance.ErrUnavailable) {
				t.Fatalf("enter = %v, want maintenance.ErrBusy and not maintenance.ErrUnavailable", err)
			}

			state, err := s.GetState(t.Context())
			if err != nil || state != maintenance.StateNormal {
				t.Fatalf("state after a busy enter = %q, %v; want normal", state, err)
			}

			release()

			if err := tc.enter(t, s); err != nil {
				t.Fatalf("enter after the write ended = %v, want nil", err)
			}
		})
	}
}
