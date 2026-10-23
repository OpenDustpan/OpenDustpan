package inventory_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/inventory"
)

// unitEntries returns n units of acme, unit-00 to unit-nn, in the reverse
// order of the unit ID. The even units have the label team=a, and unit-03 is
// locked.
func unitEntries(n int) []inventory.Entry {
	out := make([]inventory.Entry, 0, n)

	for i := n - 1; i >= 0; i-- {
		e := inventory.Entry{ //nolint:exhaustruct_v5 // The fields of the filters only.
			Namespace: "acme", Tool: "pulumi", Name: fmt.Sprintf("web/s%02d", i), UnitID: fmt.Sprintf("unit-%02d", i),
			Labels: map[string]string{},
		}

		if i%2 == 0 {
			e.Labels["team"] = "a"
		}

		if i == 3 {
			e.Lock = &inventory.Lock{Since: time.Unix(0, 0), Holder: "ci", Operation: "update"}
		}

		out = append(out, e)
	}

	return out
}

// ids returns the unit IDs of entries.
func ids(entries []inventory.Entry) []string {
	out := make([]string, len(entries))
	for i := range entries {
		out[i] = entries[i].UnitID
	}

	return out
}

// TestPageUnits proves the order, the cursor, the limit and each filter of
// PageUnits, and that MatchesNothing agrees with it.
func TestPageUnits(t *testing.T) {
	t.Parallel()

	all := unitEntries(8)

	tests := []struct {
		name string
		q    inventory.UnitQuery
		want []string
	}{
		{
			name: "the order of the unit ID and the limit",
			q:    inventory.UnitQuery{Namespace: "acme", Limit: 3}, //nolint:exhaustruct_v5 // No filter.
			want: []string{"unit-00", "unit-01", "unit-02"},
		},
		{
			name: "after the cursor",
			q: inventory.UnitQuery{ //nolint:exhaustruct_v5 // A cursor and no filter.
				Namespace: "acme", After: "unit-05", Limit: 10,
			},
			want: []string{"unit-06", "unit-07"},
		},
		{
			name: "a limit of zero reads one",
			q:    inventory.UnitQuery{Namespace: "acme"}, //nolint:exhaustruct_v5 // No filter.
			want: []string{"unit-00"},
		},
		{
			name: "another namespace",
			q:    inventory.UnitQuery{Namespace: "other", Limit: 10}, //nolint:exhaustruct_v5 // No filter.
			want: []string{},
		},
		{
			name: "a label",
			q: inventory.UnitQuery{ //nolint:exhaustruct_v5 // A label only.
				Namespace: "acme", Labels: []inventory.UnitLabel{{Key: "team", Value: "a"}}, Limit: 2, After: "unit-02",
			},
			want: []string{"unit-04", "unit-06"},
		},
		{
			name: "units by tool and name",
			q: inventory.UnitQuery{ //nolint:exhaustruct_v5 // Units only.
				Namespace: "acme", RestrictUnits: true, Limit: 10,
				Units: []inventory.UnitName{{Tool: "pulumi", Name: "web/s05"}, {Tool: "opentofu", Name: "web/s01"}},
			},
			want: []string{"unit-05"},
		},
		{
			name: "an empty unit filter",
			q: inventory.UnitQuery{ //nolint:exhaustruct_v5 // An empty unit filter only.
				Namespace: "acme", RestrictUnits: true, Limit: 10,
			},
			want: []string{},
		},
		{
			name: "the locked state",
			q: inventory.UnitQuery{ //nolint:exhaustruct_v5 // States only.
				Namespace: "acme", RestrictStates: true, Limit: 10,
				States: []inventory.State{inventory.StateDrifted, inventory.StateLocked},
			},
			want: []string{"unit-03"},
		},
		{
			name: "a state that the server does not derive",
			q: inventory.UnitQuery{ //nolint:exhaustruct_v5 // States only.
				Namespace: "acme", RestrictStates: true, Limit: 10, States: []inventory.State{inventory.StateDrifted},
			},
			want: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := inventory.PageUnits(all, tc.q)
			if !slices.Equal(ids(got), tc.want) {
				t.Fatalf("PageUnits = %v, want %v", ids(got), tc.want)
			}

			if tc.q.MatchesNothing() && len(got) != 0 {
				t.Fatalf("MatchesNothing is true, but PageUnits = %v", ids(got))
			}
		})
	}
}

// TestUnitQueryLimits proves the floor and the cap of the limit, and when a
// state filter reads the lock columns.
func TestUnitQueryLimits(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ limit, want int }{
		{-5, 1},
		{0, 1},
		{1, 1},
		{inventory.MaxUnitQueryLimit, inventory.MaxUnitQueryLimit},
		{inventory.MaxUnitQueryLimit + 1, inventory.MaxUnitQueryLimit},
	} {
		q := inventory.UnitQuery{Limit: tc.limit} //nolint:exhaustruct_v5 // The limit only.
		if got := q.PageLimit(); got != tc.want {
			t.Errorf("PageLimit of %d = %d, want %d", tc.limit, got, tc.want)
		}
	}

	locked := inventory.UnitQuery{ //nolint:exhaustruct_v5 // States only.
		RestrictStates: true, States: []inventory.State{inventory.StateLocked},
	}
	if !locked.LockedOnly() || locked.MatchesNothing() {
		t.Error("a filter of the locked state must read the lock columns and can match")
	}

	none := inventory.UnitQuery{RestrictStates: true} //nolint:exhaustruct_v5 // States only.
	if none.LockedOnly() || !none.MatchesNothing() {
		t.Error("an empty state filter matches nothing")
	}
}
