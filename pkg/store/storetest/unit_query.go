package storetest

import (
	"fmt"
	"maps"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// RunUnitQuery runs the conformance cases of inventory.UnitQuerier against
// stores produced by newStore.
func RunUnitQuery(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s Store)
		name string
	}{
		{name: "walks every page in the order of the unit ID", test: testQueryUnitsPageBoundaries},
		{name: "orders units with the same name by the unit ID", test: testQueryUnitsTies},
		{name: "answers an empty page", test: testQueryUnitsEmptyPage},
		{name: "skips a unit deleted between pages", test: testQueryUnitsDeletedBetweenPages},
		{name: "pages a label filter", test: testQueryUnitsLabelPages},
		{name: "pages a unit filter", test: testQueryUnitsUnitPages},
		{name: "a state filter keeps the locked units only", test: testQueryUnitsStates},
		{name: "reads the limit after the floor and the cap", test: testQueryUnitsLimit},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// createStacks creates one stack for each project and name pair in ns, with
// the tags of tags for the stack at the same index, and returns the stacks.
func createStacks(
	t *testing.T,
	s Store,
	ns namespace.Namespace,
	names [][2]string,
	tags []map[string]string,
) []stack.Stack {
	t.Helper()

	out := make([]stack.Stack, 0, len(names))

	for i, nn := range names {
		st := newStack(ns.Name, nn[0], nn[1])
		st.Tags = tags[i%len(tags)]

		if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
			t.Fatalf("create stack %s: %v", st.Ref, err)
		}

		out = append(out, st)
	}

	return out
}

// queryUnits runs q and fails the test on an error.
func queryUnits(t *testing.T, s Store, q inventory.UnitQuery) []inventory.Entry {
	t.Helper()

	got, err := s.QueryUnits(t.Context(), q)
	if err != nil {
		t.Fatalf("query units %+v: %v", q, err)
	}

	if got == nil {
		t.Fatalf("query units %+v = nil, want a list", q)
	}

	for i := range got {
		if got[i].Labels == nil {
			t.Fatalf("labels of %q = nil, want a map", got[i].Name)
		}
	}

	return got
}

// walkUnits reads every page of q, from the first, and returns the unit IDs
// in the order of the pages and the size of each page.
func walkUnits(t *testing.T, s Store, q inventory.UnitQuery) ([]string, []int) {
	t.Helper()

	var (
		ids   []string
		sizes []int
	)

	q.After = ""

	for range 100 {
		page := queryUnits(t, s, q)
		sizes = append(sizes, len(page))

		for i := range page {
			ids = append(ids, page[i].UnitID)
		}

		if len(page) < q.PageLimit() {
			return ids, sizes
		}

		q.After = page[len(page)-1].UnitID
	}

	t.Fatalf("query units %+v did not end after 100 pages", q)

	return nil, nil
}

// wantUnits returns the unit IDs that inventory.PageUnits gives for q over
// every entry of the namespace of q, with no limit.
func wantUnits(t *testing.T, s Store, q inventory.UnitQuery) []string {
	t.Helper()

	q.After = ""
	q.Limit = inventory.MaxUnitQueryLimit

	entries := inventory.PageUnits(listInventory(t, s, q.Namespace), q)
	out := make([]string, len(entries))

	for i := range entries {
		out[i] = entries[i].UnitID
	}

	return out
}

// testQueryUnitsPageBoundaries walks 7 units with pages of 3: the pages hold
// 3, 3 and 1 units, the walk gives each unit once in the order of the unit
// ID, and each entry matches the entry that ListUnits reads.
func testQueryUnitsPageBoundaries(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	other := newNamespace(t, s, "other")
	names := [][2]string{
		{"web", "a"}, {"web", "b"}, {"api", "c"}, {"api", "d"}, {"db", "e"}, {"db", "f"}, {"ops", "g"},
	}

	createStacks(t, s, ns, names, []map[string]string{{"team": "platform"}, {}})
	createStacks(t, s, other, names[:2], []map[string]string{{}})

	q := inventory.UnitQuery{Namespace: ns.Name, Limit: 3} //nolint:exhaustruct_v5 // The query sets no filter.

	ids, sizes := walkUnits(t, s, q)
	if !slices.Equal(sizes, []int{3, 3, 1}) {
		t.Fatalf("page sizes = %v, want [3 3 1]", sizes)
	}

	want := wantUnits(t, s, q)
	if !slices.Equal(ids, want) || !slices.IsSorted(ids) || len(ids) != len(names) {
		t.Fatalf("walked unit IDs = %v, want %v", ids, want)
	}

	q.Limit = len(names)
	checkQueryEntries(t, s, q)

	q.After = ids[len(ids)-1]
	if got := queryUnits(t, s, q); len(got) != 0 {
		t.Fatalf("page after the last unit = %+v, want none", got)
	}
}

// checkQueryEntries checks that each entry of q matches the entry of the
// same unit that ListUnits reads.
func checkQueryEntries(t *testing.T, s Store, q inventory.UnitQuery) {
	t.Helper()

	listed := map[string]inventory.Entry{}

	all := listInventory(t, s, q.Namespace)
	for i := range all {
		listed[all[i].UnitID] = all[i]
	}

	got := queryUnits(t, s, q)
	for i := range got {
		e, l := got[i], listed[got[i].UnitID]
		if e.Name != l.Name || e.Tool != l.Tool || e.Namespace != l.Namespace || !maps.Equal(e.Labels, l.Labels) ||
			!e.LastChanged.Equal(l.LastChanged) || e.DisplayName != l.DisplayName {
			t.Fatalf("query entry = %+v, want the listed entry %+v", e, l)
		}
	}
}

// testQueryUnitsTies creates units with the same stack name in several
// projects and the same labels: the page order is the unit ID alone, so the
// walk gives each unit once with pages of 1 and of 2.
func testQueryUnitsTies(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	names := [][2]string{{"a", "prod"}, {"b", "prod"}, {"c", "prod"}, {"d", "prod"}, {"e", "prod"}}
	createStacks(t, s, ns, names, []map[string]string{{"env": "prod"}})

	for _, limit := range []int{1, 2} {
		q := inventory.UnitQuery{ //nolint:exhaustruct_v5 // The query sets one label filter.
			Namespace: ns.Name,
			Labels:    []inventory.UnitLabel{{Key: "env", Value: "prod"}},
			Limit:     limit,
		}

		ids, _ := walkUnits(t, s, q)
		if want := wantUnits(t, s, q); !slices.Equal(ids, want) || len(ids) != len(names) {
			t.Fatalf("limit %d: walked unit IDs = %v, want %v", limit, ids, want)
		}
	}
}

// testQueryUnitsEmptyPage reads a namespace with no unit, a namespace that
// does not exist, and a filter that matches no unit: each answers an empty
// list.
func testQueryUnitsEmptyPage(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	full := newNamespace(t, s, "full")
	createStacks(t, s, full, [][2]string{{"web", "prod"}}, []map[string]string{{"team": "platform"}})

	queries := []inventory.UnitQuery{
		{Namespace: ns.Name, Limit: 10},                        //nolint:exhaustruct_v5 // No filter.
		{Namespace: namespace.Name("missing"), Limit: 10},      //nolint:exhaustruct_v5 // No filter.
		{Namespace: full.Name, After: "\U0010FFFF", Limit: 10}, //nolint:exhaustruct_v5 // A cursor after every ID.
		{ //nolint:exhaustruct_v5 // A label that no unit has.
			Namespace: full.Name, Labels: []inventory.UnitLabel{{Key: "team", Value: "other"}}, Limit: 10,
		},
		{Namespace: full.Name, RestrictUnits: true, Limit: 10}, //nolint:exhaustruct_v5 // An empty unit filter.
		{ //nolint:exhaustruct_v5 // A unit that does not exist.
			Namespace: full.Name, RestrictUnits: true, Units: []inventory.UnitName{{Tool: "pulumi", Name: "web/dev"}},
			Limit: 10,
		},
		{Namespace: full.Name, RestrictStates: true, Limit: 10}, //nolint:exhaustruct_v5 // An empty state filter.
		{ //nolint:exhaustruct_v5 // A state that no unit has.
			Namespace: full.Name, RestrictStates: true, States: []inventory.State{inventory.StateLocked}, Limit: 10,
		},
	}

	for i, q := range queries {
		if got := queryUnits(t, s, q); len(got) != 0 {
			t.Fatalf("query %d = %+v, want none", i, got)
		}
	}
}

// testQueryUnitsDeletedBetweenPages reads the first page, deletes the last
// unit of that page and the first unit of the next page, then reads the next
// page after the cursor: the page starts at the next live unit, and no unit
// is read twice.
func testQueryUnitsDeletedBetweenPages(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	stacks := createStacks(t, s, ns, [][2]string{
		{"web", "a"}, {"web", "b"}, {"web", "c"}, {"web", "d"}, {"web", "e"}, {"web", "f"},
	}, []map[string]string{{}})

	q := inventory.UnitQuery{Namespace: ns.Name, Limit: 2} //nolint:exhaustruct_v5 // The query sets no filter.
	before := wantUnits(t, s, q)

	first := queryUnits(t, s, q)
	if len(first) != 2 || first[0].UnitID != before[0] || first[1].UnitID != before[1] {
		t.Fatalf("first page = %+v, want units %v", first, before[:2])
	}

	for _, id := range []string{before[1], before[2]} {
		for i := range stacks {
			if stacks[i].ID.String() != id {
				continue
			}

			err := s.DeleteStack(t.Context(), stacks[i].Ref, ns.Fence(), stack.EmptyGuard(),
				inventory.Deletion{Entry: deleteEntry(ns.Name), PurgeAfter: purgeAt()})
			if err != nil {
				t.Fatalf("delete stack %s: %v", stacks[i].Ref, err)
			}
		}
	}

	q.After = first[1].UnitID
	q.Limit = 10

	rest := queryUnits(t, s, q)

	got := make([]string, len(rest))
	for i := range rest {
		got[i] = rest[i].UnitID
	}

	if want := before[3:]; !slices.Equal(got, want) {
		t.Fatalf("page after a delete = %v, want %v", got, want)
	}
}

// labelNames returns 9 stack names in one project.
func labelNames() [][2]string {
	out := make([][2]string, 0, 9)
	for i := range 9 {
		out = append(out, [2]string{"web", fmt.Sprintf("s%d", i)})
	}

	return out
}

// testQueryUnitsLabelPages walks label filters with pages of 2: one label,
// two labels, and a key with another value. Each walk gives the units of
// inventory.PageUnits, and no unit of another namespace with the same
// labels.
func testQueryUnitsLabelPages(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	other := newNamespace(t, s, "other")
	createStacks(t, s, ns, labelNames(), []map[string]string{
		{"env": "prod", "team": "a"}, {"env": "prod", "team": "b"}, {"env": "dev", "team": "a"}, {},
	})
	createStacks(t, s, other, labelNames(), []map[string]string{{"env": "prod", "team": "a"}})

	filters := [][]inventory.UnitLabel{
		{{Key: "env", Value: "prod"}},
		{{Key: "env", Value: "prod"}, {Key: "team", Value: "a"}},
		{{Key: "team", Value: "b"}},
		{{Key: "env", Value: "PROD"}},
	}

	for _, labels := range filters {
		q := inventory.UnitQuery{Namespace: ns.Name, Labels: labels, Limit: 2} //nolint:exhaustruct_v5 // Labels only.

		ids, _ := walkUnits(t, s, q)
		if want := wantUnits(t, s, q); !slices.Equal(ids, want) {
			t.Fatalf("labels %v: walked unit IDs = %v, want %v", labels, ids, want)
		}
	}
}

// testQueryUnitsUnitPages walks a filter of 4 units, one of them unknown and
// one in another namespace, with pages of 1 and 2.
func testQueryUnitsUnitPages(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	other := newNamespace(t, s, "other")
	createStacks(t, s, ns, labelNames(), []map[string]string{{}})
	createStacks(t, s, other, [][2]string{{"web", "s4"}}, []map[string]string{{}})

	units := []inventory.UnitName{
		{Tool: "pulumi", Name: "web/s1"},
		{Tool: "pulumi", Name: "web/s5"},
		{Tool: "pulumi", Name: "web/s7"},
		{Tool: "opentofu", Name: "web/s3"},
	}

	for _, limit := range []int{1, 2} {
		q := inventory.UnitQuery{ //nolint:exhaustruct_v5 // A unit filter only.
			Namespace: ns.Name, Units: units, RestrictUnits: true, Limit: limit,
		}

		ids, _ := walkUnits(t, s, q)
		if want := wantUnits(t, s, q); !slices.Equal(ids, want) || len(ids) != 3 {
			t.Fatalf("limit %d: walked unit IDs = %v, want %v", limit, ids, want)
		}
	}
}

// testQueryUnitsStates checks the state filter on units with no lock: a
// filter of states that the server does not derive matches nothing, and a
// filter with the locked state keeps the locked units, here none. The
// suites of the lock writers check the locked units.
func testQueryUnitsStates(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	createStacks(t, s, ns, labelNames(), []map[string]string{{}})

	for _, states := range [][]inventory.State{
		{inventory.StateDrifted, inventory.StateFailed},
		{inventory.StateLocked, inventory.StateInSync},
	} {
		q := inventory.UnitQuery{ //nolint:exhaustruct_v5 // A state filter only.
			Namespace: ns.Name, States: states, RestrictStates: true, Limit: 2,
		}

		ids, _ := walkUnits(t, s, q)
		if want := wantUnits(t, s, q); !slices.Equal(ids, want) || len(ids) != 0 {
			t.Fatalf("states %v: walked unit IDs = %v, want %v", states, ids, want)
		}
	}
}

// testQueryUnitsLimit reads a limit of zero, of one below zero and of one
// above the cap: the first two read one unit, the last reads every unit.
func testQueryUnitsLimit(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	createStacks(t, s, ns, labelNames(), []map[string]string{{}})

	for _, tc := range []struct{ limit, want int }{
		{0, 1}, {-1, 1}, {inventory.MaxUnitQueryLimit + 1, len(labelNames())},
	} {
		q := inventory.UnitQuery{Namespace: ns.Name, Limit: tc.limit} //nolint:exhaustruct_v5 // No filter.
		if got := queryUnits(t, s, q); len(got) != tc.want {
			t.Fatalf("limit %d read %d units, want %d", tc.limit, len(got), tc.want)
		}
	}
}
