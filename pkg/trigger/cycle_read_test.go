package trigger_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// TestManagerCycleHidesUnitsTheCallerCannotRead saves a loop a -> b -> c -> a.
// The caller grace may read a and b, not c. The cycle error names no ID and no
// name of c, and the text counts it.
func TestManagerCycleHidesUnitsTheCallerCannotRead(t *testing.T) {
	t.Parallel()

	r := newManagerRig(t)
	r.authz.grant(grace, "b", trigger.SettingsAction, trigger.StartAction, identity.ActionUnitRead)
	r.authz.grant(grace, "a", identity.ActionUnitRead)

	for _, in := range []trigger.SaveInput{followsInput("c", "b"), followsInput("a", "c")} {
		if _, err := r.m.Create(t.Context(), person(ada), reqInfo(), in); err != nil {
			t.Fatalf("Create %s follows %s = %v", in.UnitID, in.SourceID, err)
		}
	}

	_, err := r.m.Create(t.Context(), person(grace), reqInfo(), followsInput("b", "a"))

	var cycle *trigger.CycleError
	if !errors.As(err, &cycle) {
		t.Fatalf("Create = %v, want a *CycleError", err)
	}

	if got, want := cycle.Error(), "trigger closes a loop of units: a -> b -> 1 unit that you cannot read -> a"; got != want {
		t.Errorf("cycle text = %q, want %q", got, want)
	}

	for _, tc := range []struct {
		id       string
		readable bool
	}{
		{id: "a", readable: true},
		{id: "b", readable: true},
		{id: "c", readable: false},
	} {
		u := unit(tc.id)
		if cycle.Readable[u] != tc.readable {
			t.Errorf("readable %s = %t, want %t", tc.id, cycle.Readable[u], tc.readable)
		}

		if _, named := cycle.Names[u]; named != tc.readable {
			t.Errorf("name of %s given = %t, want %t", tc.id, named, tc.readable)
		}
	}
}

func TestCycleErrorCountsUnreadableUnits(t *testing.T) {
	t.Parallel()

	path := []graph.Unit{unit("a"), unit("b"), unit("c"), unit("d"), unit("a")}

	for _, tc := range []struct {
		name     string
		readable map[graph.Unit]bool
		want     string
	}{
		{name: "no marks", readable: nil, want: "a -> b -> c -> d -> a"},
		{name: "none readable", readable: map[graph.Unit]bool{}, want: "5 units that you cannot read"},
		{
			name:     "a run of two",
			readable: map[graph.Unit]bool{unit("a"): true, unit("d"): true},
			want:     "a -> 2 units that you cannot read -> d -> a",
		},
		{
			name:     "a false mark",
			readable: map[graph.Unit]bool{unit("a"): true, unit("b"): false, unit("c"): true, unit("d"): true},
			want:     "a -> 1 unit that you cannot read -> c -> d -> a",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := &trigger.CycleError{Path: path, Names: nil, Readable: tc.readable}
			if got, want := e.Error(), "trigger closes a loop of units: "+tc.want; got != want {
				t.Errorf("Error() = %q, want %q", got, want)
			}
		})
	}
}
