package manuallocktest

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// lockedQuery returns the query of the units of the fixture that have the
// locked state, with pages of limit units.
func (f *fixture) lockedQuery(limit int) inventory.UnitQuery {
	return inventory.UnitQuery{ //nolint:exhaustruct_v5 // A state filter only.
		Namespace:      f.ns.Name,
		States:         []inventory.State{inventory.StateLocked},
		RestrictStates: true,
		Limit:          limit,
	}
}

// lockedNames walks every page of the locked units with pages of 1, checks
// the walk against inventory.PageUnits over ListUnits, and returns the names
// in the order of the walk.
func (f *fixture) lockedNames(t *testing.T) []string {
	t.Helper()

	q := f.lockedQuery(1)

	var got []inventory.Entry

	for range 10 {
		page, err := f.s.QueryUnits(t.Context(), q)
		if err != nil {
			t.Fatalf("QueryUnits: %v", err)
		}

		got = append(got, page...)

		if len(page) == 0 {
			break
		}

		q.After = page[len(page)-1].UnitID
	}

	all, err := f.s.ListUnits(t.Context(), f.ns.Name)
	if err != nil {
		t.Fatalf("ListUnits: %v", err)
	}

	want := inventory.PageUnits(all, f.lockedQuery(inventory.MaxUnitQueryLimit))
	if len(got) != len(want) {
		t.Fatalf("locked units = %d, want %d", len(got), len(want))
	}

	names := make([]string, len(got))

	for i := range got {
		if got[i].UnitID != want[i].UnitID || (got[i].ManualLock == nil) != (want[i].ManualLock == nil) ||
			(got[i].Lock == nil) != (want[i].Lock == nil) {
			t.Fatalf("locked unit %d = %+v, want %+v", i, got[i], want[i])
		}

		names[i] = got[i].Name
	}

	slices.Sort(names)

	return names
}

// testQueryUnitsLocked checks that the state filter of QueryUnits keeps the
// units with a tool lock or a manual lock on the unit, its collection or its
// namespace, and no other unit.
func testQueryUnitsLocked(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	db := createWorkspace(t, s, f.ns, "db")
	f.put(t, f.web, f.payments)

	steps := []struct {
		act  func()
		name string
		want []string
	}{
		{name: "no lock", act: func() {}, want: []string{}},
		{
			name: "a tool lock",
			act: func() {
				w, err := s.GetWorkspace(
					t.Context(),
					workspace.Ref{Namespace: f.ns.Name, Name: mustWorkspaceName(t, "db")},
				)
				if err != nil {
					t.Fatalf("GetWorkspace: %v", err)
				}

				w.Revision++
				w.Lock = workspace.Lock{LockedAt: now(), ID: "lock-1", Holder: "ci", Reason: "plan"}

				if err := s.SaveWorkspace(t.Context(), w, f.ns.Fence()); err != nil {
					t.Fatalf("SaveWorkspace with a tool lock: %v", err)
				}
			},
			want: []string{db.Name},
		},
		{
			name: "a collection lock",
			act:  func() { f.lock(t, manuallock.CollectionTarget(f.payments.Ref), alice, testReason) },
			want: []string{db.Name, f.web.Name},
		},
		{
			name: "a unit lock",
			act:  func() { f.lock(t, manuallock.UnitTarget(f.api), alice, testReason) },
			want: []string{f.api.Name, db.Name, f.web.Name},
		},
		{
			name: "a namespace lock alone",
			act: func() {
				f.forceRemove(t, manuallock.UnitTarget(f.api))
				f.forceRemove(t, manuallock.CollectionTarget(f.payments.Ref))
				f.lock(t, manuallock.NamespaceTarget(f.ns.Name), bob, testReason)
			},
			want: []string{f.api.Name, db.Name, f.web.Name},
		},
	}

	for _, step := range steps {
		step.act()

		if got := f.lockedNames(t); !slices.Equal(got, step.want) {
			t.Fatalf("%s: locked units = %v, want %v", step.name, got, step.want)
		}

		f.checkFilterCombinations(t, step.name)
	}
}

// checkFilterCombinations walks the pages of each combination of the unit
// filter and the state filter, with pages of 1, 2 and the cap, and checks each
// walk against inventory.PageUnits over ListUnits. It proves that the locked
// filter, which reads the lock sources first, gives the same units as the
// reference for every combination.
func (f *fixture) checkFilterCombinations(t *testing.T, step string) {
	t.Helper()

	all, err := f.s.ListUnits(t.Context(), f.ns.Name)
	if err != nil {
		t.Fatalf("%s: ListUnits: %v", step, err)
	}

	for _, units := range subsetsOf(all) {
		for _, locked := range []bool{false, true} {
			for _, limit := range []int{1, 2, inventory.MaxUnitQueryLimit} {
				q := inventory.UnitQuery{
					Namespace: f.ns.Name, After: "", Labels: nil, Units: units, RestrictUnits: true,
					States: []inventory.State{inventory.StateLocked}, RestrictStates: locked, Limit: limit,
				}
				want := unitIDs(inventory.PageUnits(all, withLimit(q, inventory.MaxUnitQueryLimit)))

				if got := f.walkIDs(t, q); !slices.Equal(got, want) {
					t.Fatalf("%s: units %v locked %v limit %d: walked %v, want %v",
						step, units, locked, limit, got, want)
				}
			}
		}
	}
}

// walkIDs reads every page of q and returns the unit IDs in the order of the
// walk.
func (f *fixture) walkIDs(t *testing.T, q inventory.UnitQuery) []string {
	t.Helper()

	var got []string

	for range 10 {
		page, err := f.s.QueryUnits(t.Context(), q)
		if err != nil {
			t.Fatalf("QueryUnits: %v", err)
		}

		if len(page) == 0 {
			break
		}

		got = append(got, unitIDs(page)...)
		q.After = page[len(page)-1].UnitID
	}

	return got
}

// subsetsOf returns each subset of the units of entries, as unit filters.
func subsetsOf(entries []inventory.Entry) [][]inventory.UnitName {
	out := make([][]inventory.UnitName, 0, 1<<len(entries))

	for mask := range 1 << len(entries) {
		units := []inventory.UnitName{}

		for i := range entries {
			if mask&(1<<i) != 0 {
				units = append(units, inventory.UnitName{Tool: entries[i].Tool, Name: entries[i].Name})
			}
		}

		out = append(out, units)
	}

	return out
}

// unitIDs returns the unit IDs of entries, in order.
func unitIDs(entries []inventory.Entry) []string {
	ids := make([]string, len(entries))
	for i := range entries {
		ids[i] = entries[i].UnitID
	}

	return ids
}

// withLimit returns q with a new limit.
func withLimit(q inventory.UnitQuery, limit int) inventory.UnitQuery {
	q.Limit = limit

	return q
}
