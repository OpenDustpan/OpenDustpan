package inventory_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

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
	if inventory.States()[0] != inventory.StateFailed {
		t.Error("States() shares its backing array")
	}
}

func TestStatesBadgeOrder(t *testing.T) {
	t.Parallel()
	want := []inventory.State{
		inventory.StateFailed, inventory.StateAwaitingApproval,
		inventory.StateApplyNeeded, inventory.StateDrifted,
		inventory.StateRunning, inventory.StateQueued, inventory.StateLocked,
		inventory.StateUnknown, inventory.StateInSync,
	}
	if got := inventory.States(); !slices.Equal(got, want) {
		t.Errorf("States() = %v, want %v", got, want)
	}
}

// TestValidStateListFollowsStates proves that the text of the valid values
// names every state once, in the order of the badges, with a comma and a space.
func TestValidStateListFollowsStates(t *testing.T) {
	t.Parallel()

	states := inventory.States()
	names := make([]string, 0, len(states))

	for _, s := range states {
		names = append(names, string(s))
	}

	if got, want := inventory.ValidStateList(), strings.Join(names, ", "); got != want {
		t.Fatalf("ValidStateList() = %q, want %q", got, want)
	}
}

func TestParseState(t *testing.T) {
	t.Parallel()

	for _, s := range inventory.States() {
		got, err := inventory.ParseState(string(s))
		if err != nil || got != s {
			t.Errorf("ParseState(%q) = %q, %v; want the same state", s, got, err)
		}
	}

	for _, raw := range []string{"", "Locked", "LOCKED", "locked ", " locked", "in_sync", "in sync", "winning", "locked\n"} {
		got, err := inventory.ParseState(raw)
		if !errors.Is(err, inventory.ErrUnknownState) {
			t.Errorf("ParseState(%q) error = %v, want ErrUnknownState", raw, err)
		}

		if got != "" {
			t.Errorf("ParseState(%q) = %q, want no state", raw, got)
		}

		for _, s := range inventory.States() {
			if !strings.Contains(err.Error(), string(s)) {
				t.Errorf("ParseState(%q) error %q does not name %q", raw, err, s)
			}
		}
	}
}

func TestEntryLockedState(t *testing.T) {
	t.Parallel()

	early := time.Date(2026, time.October, 1, 8, 0, 0, 0, time.UTC)
	late := early.Add(48 * time.Hour)
	lock := func(at time.Time) *inventory.Lock { return &inventory.Lock{Since: at, Holder: "u", Operation: "lock"} }
	manual := func(at time.Time) *inventory.ManualLock {
		return &inventory.ManualLock{Since: at, Kind: "unit", Object: "o", Holder: "u", Reason: "r"}
	}

	tests := []struct {
		name   string
		lock   *inventory.Lock
		manual *inventory.ManualLock
		want   []inventory.StateSince
	}{
		{"neither", nil, nil, []inventory.StateSince{}},
		{"lock only", lock(late), nil, []inventory.StateSince{{Since: late, State: inventory.StateLocked}}},
		{"manual lock only", nil, manual(late), []inventory.StateSince{{Since: late, State: inventory.StateLocked}}},
		{
			"both, lock earlier", lock(early), manual(late),
			[]inventory.StateSince{{Since: early, State: inventory.StateLocked}},
		},
		{
			"both, manual lock earlier", lock(late), manual(early),
			[]inventory.StateSince{{Since: early, State: inventory.StateLocked}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := inventory.Entry{
				LastRelease: nil,
				LastChanged: time.Time{}, Namespace: "", Tool: "", Name: "", UnitID: "", DisplayName: "",
				Status: "", ResourceCount: nil, Lock: tt.lock, ManualLock: tt.manual, Collection: "", Labels: nil,
			}

			checkDerivedStates(t, e, tt.want)

			if got, want := e.HasState(inventory.StateLocked), tt.lock != nil || tt.manual != nil; got != want {
				t.Errorf("HasState(locked) = %v, want %v", got, want)
			}
		})
	}
}

// checkDerivedStates checks the states that e derives: the list
// and that no other state is derived.
func checkDerivedStates(t *testing.T, e inventory.Entry, want []inventory.StateSince) {
	t.Helper()

	if got := e.StatesOf(); !slices.Equal(got, want) || got == nil {
		t.Errorf("StatesOf() = %v, want %v", got, want)
	}

	for _, s := range inventory.States() {
		if s != inventory.StateLocked && e.HasState(s) {
			t.Errorf("HasState(%q) = true, want false: no other state is derived", s)
		}
	}
}
