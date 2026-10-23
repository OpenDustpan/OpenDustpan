package sqlite

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/maintenance"
)

// White-box: the bound of the wait is a field of the store, so the test can
// make it short instead of holding a write for the whole bound.
//
// TestEnterBusyAfterTheBound checks that an enter of maintenance mode that a
// write in flight keeps waiting past the bound reports maintenance.ErrBusy,
// not maintenance.ErrUnavailable, changes nothing and leaves no hold, and that
// the next enter after the write ends succeeds.
func TestEnterBusyAfterTheBound(t *testing.T) {
	t.Parallel()

	for _, enter := range gateEnters() {
		t.Run(enter.name, func(t *testing.T) {
			t.Parallel()

			r := newGateRig(t)
			r.s.enterGate.onBlock = nil
			r.s.enterWait = 50 * time.Millisecond
			release := r.holdCheckedWrite()

			err := enter.run(t.Context(), r.s, func() {})
			if !errors.Is(err, maintenance.ErrBusy) || errors.Is(err, maintenance.ErrUnavailable) {
				t.Fatalf("enter = %v, want maintenance.ErrBusy and not maintenance.ErrUnavailable", err)
			}

			state, err := r.s.GetState(t.Context())
			if err != nil || state != maintenance.StateNormal {
				t.Fatalf("state after a busy enter = %q, %v; want normal", state, err)
			}

			release()
			r.assertIdle()

			if err := enter.run(t.Context(), r.s, func() {}); err != nil {
				t.Fatalf("enter after the write ended = %v, want nil", err)
			}
		})
	}
}
