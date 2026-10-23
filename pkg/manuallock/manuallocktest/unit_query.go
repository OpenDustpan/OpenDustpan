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
	}
}
