package inventory_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/inventory"
)

func TestStateWireValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		state inventory.State
		wire  string
	}{
		{inventory.StateInSync, "in-sync"},
		{inventory.StateRunning, "running"},
		{inventory.StateQueued, "queued"},
		{inventory.StateLocked, "locked"},
		{inventory.StateAwaitingApproval, "awaiting-approval"},
		{inventory.StateApplyNeeded, "apply-needed"},
		{inventory.StateDrifted, "drifted"},
		{inventory.StateFailed, "failed"},
		{inventory.StateUnknown, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.wire, func(t *testing.T) {
			t.Parallel()
			if string(tt.state) != tt.wire {
				t.Errorf("wire value = %q, want %q", tt.state, tt.wire)
			}
			if !tt.state.Valid() {
				t.Errorf("%q is not valid", tt.state)
			}
		})
	}
	if got, want := len(inventory.States()), len(tests); got != want {
		t.Errorf("States() has %d values, the table has %d", got, want)
	}
}

func TestStateValidRejects(t *testing.T) {
	t.Parallel()
	for _, s := range []inventory.State{"", "locked ", "Locked", "in_sync", "winning"} {
		if s.Valid() {
			t.Errorf("%q is valid, want invalid", s)
		}
	}
}

func TestStatesReturnsACopy(t *testing.T) {
	t.Parallel()
	a := inventory.States()
	a[0] = "changed"
	if inventory.States()[0] != inventory.StateInSync {
		t.Error("States() shares its backing array")
	}
}
