// Package inventorytest is the conformance suite for the inventory change log
// port, inventory.HistoryReader. Every built-in store runs it, and a custom
// store should too:
//
//	func TestInventoryHistoryConformance(t *testing.T) {
//		t.Parallel()
//		inventorytest.RunHistory(t, func(t *testing.T) inventorytest.Fixture { return newMyFixture(t) })
//	}
//
// The cases that need the tool tables, where each tool appends its change in
// its own write transaction, are in the store suite.
package inventorytest

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// suiteFenceVersion is the failover version of the namespaces of the suite.
// A fence with a lower version is stale.
const suiteFenceVersion = 2

// tools of the rows that the suite appends.
const (
	toolPulumi   = "pulumi"
	toolOpenTofu = "opentofu"
)

// base returns the time that the rows of the suite count from.
func base() time.Time {
	return time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC)
}

// at returns the time ms milliseconds after base.
func at(ms int) time.Time {
	return base().Add(time.Duration(ms) * time.Millisecond)
}

// makeNamespace creates the namespace called name and returns its fence.
func makeNamespace(t *testing.T, f Fixture, name namespace.Name) namespace.Fence {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           base(),
		Name:                name,
		PrimaryInstallation: "local",
		FailoverVersion:     suiteFenceVersion,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := f.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %s: %v", name, err)
	}

	return namespace.Fence{Namespace: name, FailoverVersion: suiteFenceVersion}
}

// row returns a change log row of the unit id of tool, with the sequence 999:
// a store must ignore it, and it sets the namespace of the fence.
func row(tool, id, name string, kind inventory.ChangeKind, when time.Time) inventory.Change {
	return inventory.Change{
		At:            when,
		Seq:           999,
		Namespace:     "",
		Tool:          tool,
		UnitID:        id,
		Name:          name,
		DisplayName:   name,
		Status:        "",
		Collection:    "",
		Kind:          kind,
		ResourceCount: nil,
	}
}

// appendAll appends the rows in order and returns what the store stored.
func appendAll(t *testing.T, f Fixture, fence namespace.Fence, rows ...inventory.Change) []inventory.Change {
	t.Helper()

	out := make([]inventory.Change, 0, len(rows))

	for i := range rows {
		stored, err := f.AppendChange(t.Context(), rows[i], fence)
		if err != nil {
			t.Fatalf("append row %d (%s %s): %v", i, rows[i].Kind, rows[i].Name, err)
		}

		out = append(out, stored)
	}

	return out
}

// listAll returns every row of ns, in the order of the port.
func listAll(t *testing.T, f Fixture, ns namespace.Name) []inventory.Change {
	t.Helper()

	rows, err := f.ListChanges(t.Context(), ns, at(1<<40), nil, 100000)
	if err != nil {
		t.Fatalf("list changes of %s: %v", ns, err)
	}

	return rows
}

// seqs returns the sequences of rows.
func seqs(rows []inventory.Change) []int64 {
	out := make([]int64, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].Seq)
	}

	return out
}

// increasing reports whether the sequences strictly increase.
func increasing(s []int64) bool {
	for i := 1; i < len(s); i++ {
		if s[i] <= s[i-1] {
			return false
		}
	}

	return true
}

// names renders the entries of a view for a comparison: tool:name:unit.
func names(units []inventory.UnitAt) []string {
	out := make([]string, 0, len(units))
	for i := range units {
		out = append(out, units[i].Entry.Tool+":"+units[i].Entry.Name+":"+units[i].Entry.UnitID)
	}

	return out
}

// entryNames renders entries for a comparison: tool:name:unit.
func entryNames(entries []inventory.Entry) []string {
	out := make([]string, 0, len(entries))
	for i := range entries {
		out = append(out, entries[i].Tool+":"+entries[i].Name+":"+entries[i].UnitID)
	}

	return out
}

// RunHistory runs the conformance suite of the inventory change log against
// fixtures that newFixture returns. Each case gets a new fixture, so it sees
// no namespace of another case.
func RunHistory(t *testing.T, newFixture func(t *testing.T) Fixture) {
	t.Helper()

	cases := []struct {
		run  func(t *testing.T, f Fixture)
		name string
	}{
		{name: "AppendAssignsSeq", run: appendAssignsSeq},
		{name: "SeqPerNamespace", run: seqPerNamespace},
		{name: "ConcurrentAppends", run: concurrentAppends},
		{name: "ConcurrentNamespaces", run: concurrentNamespaces},
		{name: "PagingUnderAppends", run: pagingUnderAppends},
		{name: "Until", run: until},
		{name: "CursorAndLimit", run: cursorAndLimit},
		{name: "UnknownNamespace", run: unknownNamespace},
		{name: "UnitsAtMatchesReplay", run: unitsAtMatchesReplay},
		{name: "SameMillisecond", run: sameMillisecond},
		{name: "ClockSkew", run: clockSkew},
		{name: "DeletedAndPurged", run: deletedAndPurged},
		{name: "PurgeKeepsRows", run: purgeKeepsRows},
		{name: "NamespaceRecreated", run: namespaceRecreated},
		{name: "StaleFence", run: staleFence},
		{name: "ViewAtMatchesReplayView", run: viewAtMatchesReplayView},
		{name: "ViewAtLifeCycle", run: viewAtLifeCycle},
		{name: "ViewAtBeforeFirstChange", run: viewAtBeforeFirstChange},
		{name: "ViewAtRenameChain", run: viewAtRenameChain},
		{name: "ViewAtNameReuse", run: viewAtNameReuse},
		{name: "UnitNamesRenameChain", run: unitNamesRenameChain},
		{name: "UnitNamesLifeCycle", run: unitNamesLifeCycle},
		{name: "UnitNamesScope", run: unitNamesScope},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.run(t, newFixture(t))
		})
	}
}

// appendAssignsSeq: the store sets Seq and ignores the value of the caller.
func appendAssignsSeq(t *testing.T, f Fixture) {
	t.Helper()

	fence := makeNamespace(t, f, "acme")
	stored := appendAll(t, f, fence,
		row(toolPulumi, "stk-a", "web/prod", inventory.ChangeCreated, at(1)),
		row(toolPulumi, "stk-a", "web/prod", inventory.ChangeChanged, at(2)),
		row(toolOpenTofu, "ws-b", "network", inventory.ChangeCreated, at(3)),
	)

	if !increasing(seqs(stored)) {
		t.Fatalf("sequences %v do not increase", seqs(stored))
	}

	for i := range stored {
		if stored[i].Seq == 999 {
			t.Fatalf("row %d kept the sequence of the caller", i)
		}
	}

	listed := listAll(t, f, "acme")
	if !slices.Equal(seqs(listed), seqs(stored)) {
		t.Fatalf("listed sequences %v, appended %v", seqs(listed), seqs(stored))
	}

	if listed[0].Name != "web/prod" || listed[2].Tool != toolOpenTofu || !listed[1].At.Equal(at(2)) {
		t.Fatalf("listed rows %+v do not hold the fields that were appended", listed)
	}
}

// seqPerNamespace: two namespaces have their own sequences and never read
// the rows of each other.
func seqPerNamespace(t *testing.T, f Fixture) {
	t.Helper()

	one := makeNamespace(t, f, "one")
	two := makeNamespace(t, f, "two")

	appendAll(t, f, one,
		row(toolPulumi, "stk-a", "web/prod", inventory.ChangeCreated, at(1)),
		row(toolPulumi, "stk-b", "web/dev", inventory.ChangeCreated, at(2)),
	)
	appendAll(t, f, two, row(toolPulumi, "stk-c", "api/prod", inventory.ChangeCreated, at(3)))

	rowsOne := listAll(t, f, "one")
	rowsTwo := listAll(t, f, "two")

	if len(rowsOne) != 2 || len(rowsTwo) != 1 {
		t.Fatalf("got %d and %d rows, want 2 and 1", len(rowsOne), len(rowsTwo))
	}

	for i := range rowsOne {
		if rowsOne[i].Namespace != "one" || rowsOne[i].UnitID == "stk-c" {
			t.Fatalf("namespace one read the row %+v", rowsOne[i])
		}
	}

	if rowsTwo[0].UnitID != "stk-c" || rowsTwo[0].Namespace != "two" {
		t.Fatalf("namespace two read the row %+v", rowsTwo[0])
	}
}

// concurrentAppends: 16 goroutines append 20 rows each in one namespace. The
// 320 sequences are distinct, and ListChanges returns all 320 in order.
func concurrentAppends(t *testing.T, f Fixture) {
	t.Helper()

	const (
		writers = 16
		each    = 20
	)

	fence := makeNamespace(t, f, "acme")
	got := appendConcurrently(t, f, map[namespace.Fence]int{fence: writers}, each)

	assertDistinctAndListed(t, f, fence.Namespace, got[fence], writers*each)
}

// concurrentNamespaces: writers in two namespaces append at the same time.
// Each namespace keeps its own distinct, ordered sequences, and neither reads
// a row of the other.
func concurrentNamespaces(t *testing.T, f Fixture) {
	t.Helper()

	const (
		writers = 8
		each    = 20
	)

	one := makeNamespace(t, f, "one")
	two := makeNamespace(t, f, "two")
	got := appendConcurrently(t, f, map[namespace.Fence]int{one: writers, two: writers}, each)

	assertDistinctAndListed(t, f, "one", got[one], writers*each)
	assertDistinctAndListed(t, f, "two", got[two], writers*each)

	rows := listAll(t, f, "one")
	for i := range rows {
		if rows[i].Namespace != "one" {
			t.Fatalf("namespace one read the row %+v", rows[i])
		}
	}
}

// appendConcurrently starts, for each fence, writers goroutines that append
// each rows, and returns the stored sequences for each fence.
func appendConcurrently(
	t *testing.T,
	f Fixture,
	writers map[namespace.Fence]int,
	each int,
) map[namespace.Fence][]int64 {
	t.Helper()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
		out  = map[namespace.Fence][]int64{}
	)

	for fence, count := range writers {
		for w := range count {
			wg.Go(func() {
				for n := range each {
					id := fmt.Sprintf("stk-%d-%d", w, n)
					c := row(toolPulumi, id, "web/"+id, inventory.ChangeCreated, at(n))

					stored, err := f.AppendChange(t.Context(), c, fence)

					mu.Lock()
					if err != nil {
						errs = append(errs, err)
					} else {
						out[fence] = append(out[fence], stored.Seq)
					}
					mu.Unlock()
				}
			})
		}
	}

	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		t.Fatalf("concurrent append: %v", err)
	}

	return out
}

// assertDistinctAndListed checks that appended holds total distinct
// sequences and that ListChanges returns exactly them, in order.
func assertDistinctAndListed(t *testing.T, f Fixture, ns namespace.Name, appended []int64, total int) {
	t.Helper()

	sorted := slices.Sorted(slices.Values(appended))
	if len(sorted) != total || !increasing(sorted) {
		t.Fatalf("namespace %s: %d appended sequences, want %d distinct", ns, len(sorted), total)
	}

	listed := listAll(t, f, ns)
	if !slices.Equal(seqs(listed), sorted) {
		t.Fatalf("namespace %s: listed %d rows, want the %d appended rows in order", ns, len(listed), total)
	}
}

// pagingUnderAppends: a reader pages with the cursor and a limit of 7 while
// appends run, and reads on after they end. It reads each row one time and
// skips none.
func pagingUnderAppends(t *testing.T, f Fixture) {
	t.Helper()

	const (
		writers = 4
		each    = 25
	)

	fence := makeNamespace(t, f, "acme")
	done := make(chan struct{})

	var (
		wg       sync.WaitGroup
		appended map[namespace.Fence][]int64
	)

	wg.Go(func() {
		defer close(done)

		appended = appendConcurrently(t, f, map[namespace.Fence]int{fence: writers}, each)
	})

	var (
		read   []int64
		cursor *inventory.ChangeCursor
		ended  bool
	)

	for {
		select {
		case <-done:
			ended = true
		default:
		}

		page, err := f.ListChanges(t.Context(), "acme", at(1<<40), cursor, 7)
		if err != nil {
			t.Fatalf("list changes: %v", err)
		}

		for i := range page {
			read = append(read, page[i].Seq)
		}

		if len(page) > 0 {
			cursor = &inventory.ChangeCursor{Seq: page[len(page)-1].Seq}

			continue
		}

		if ended {
			break
		}
	}

	wg.Wait()

	want := slices.Sorted(slices.Values(appended[fence]))
	if !increasing(read) || !slices.Equal(read, want) {
		t.Fatalf("paging read %d rows (%v), want each of the %d appended rows one time", len(read), read, len(want))
	}
}

// until: a row at until is in the answer. A row after it is not.
func until(t *testing.T, f Fixture) {
	t.Helper()

	fence := makeNamespace(t, f, "acme")
	appendAll(t, f, fence,
		row(toolPulumi, "stk-a", "web/prod", inventory.ChangeCreated, at(10)),
		row(toolPulumi, "stk-a", "web/prod", inventory.ChangeChanged, at(20)),
		row(toolPulumi, "stk-a", "web/prod", inventory.ChangeChanged, at(30)),
	)

	cases := []struct {
		name string
		when time.Time
		want int
	}{
		{name: "before the first row", when: at(9), want: 0},
		{name: "at a row", when: at(20), want: 2},
		{name: "after a row", when: at(29), want: 2},
		{name: "after the last row", when: at(31), want: 3},
	}

	for _, tc := range cases {
		rows, err := f.ListChanges(t.Context(), "acme", tc.when, nil, 100)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		if len(rows) != tc.want {
			t.Fatalf("%s: got %d rows, want %d", tc.name, len(rows), tc.want)
		}
	}
}

// cursorAndLimit: the limit holds, the cursor excludes its own row, and the
// page after the last row is empty.
func cursorAndLimit(t *testing.T, f Fixture) {
	t.Helper()

	fence := makeNamespace(t, f, "acme")

	rows := make([]inventory.Change, 0, 5)
	for n := range 5 {
		rows = append(rows, row(toolPulumi, fmt.Sprintf("stk-%d", n), fmt.Sprintf("web/%d", n),
			inventory.ChangeCreated, at(n)))
	}

	stored := appendAll(t, f, fence, rows...)

	page, err := f.ListChanges(t.Context(), "acme", at(1000), nil, 2)
	if err != nil || len(page) != 2 || page[0].Seq != stored[0].Seq {
		t.Fatalf("first page: %v rows %v, err %v", len(page), seqs(page), err)
	}

	next, err := f.ListChanges(t.Context(), "acme", at(1000), &inventory.ChangeCursor{Seq: page[1].Seq}, 2)
	if err != nil || !slices.Equal(seqs(next), seqs(stored[2:4])) {
		t.Fatalf("second page: got %v, want %v, err %v", seqs(next), seqs(stored[2:4]), err)
	}

	last, err := f.ListChanges(t.Context(), "acme", at(1000), &inventory.ChangeCursor{Seq: stored[4].Seq}, 2)
	if err != nil || len(last) != 0 {
		t.Fatalf("page after the last row: got %v rows, err %v", len(last), err)
	}
}

// unknownNamespace: every read answers an empty result and no error.
func unknownNamespace(t *testing.T, f Fixture) {
	t.Helper()

	rows, err := f.ListChanges(t.Context(), "ghost", at(10), nil, 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("ListChanges: %d rows, err %v", len(rows), err)
	}

	entries, err := f.UnitsAt(t.Context(), "ghost", at(10))
	if err != nil || len(entries) != 0 {
		t.Fatalf("UnitsAt: %d entries, err %v", len(entries), err)
	}

	view, err := f.ViewAt(t.Context(), "ghost", at(10))
	if err != nil || len(view.Units) != 0 || view.Purged != 0 {
		t.Fatalf("ViewAt: %+v, err %v", view, err)
	}

	got, err := f.UnitNames(t.Context(), "ghost", toolPulumi, "stk-a", at(10))
	if err != nil || got != (inventory.Names{AtTime: "", Current: ""}) {
		t.Fatalf("UnitNames: %+v, err %v", got, err)
	}
}

// lifeCycleRows appends a log with a rename chain, a delete and a restore, a
// purge, and a unit that stays, and returns the stored rows.
func lifeCycleRows(t *testing.T, f Fixture, fence namespace.Fence) []inventory.Change {
	t.Helper()

	return appendAll(t, f, fence,
		row(toolPulumi, "stk-a", "web/prod", inventory.ChangeCreated, at(100)),
		row(toolOpenTofu, "ws-b", "network", inventory.ChangeCreated, at(200)),
		row(toolPulumi, "stk-a", "web/live", inventory.ChangeRenamed, at(300)),
		row(toolOpenTofu, "ws-b", "network", inventory.ChangeDeleted, at(400)),
		row(toolOpenTofu, "ws-b", "network", inventory.ChangeRestored, at(500)),
		row(toolPulumi, "stk-c", "api/prod", inventory.ChangeCreated, at(600)),
		row(toolPulumi, "stk-c", "api/prod", inventory.ChangePurged, at(700)),
		row(toolPulumi, "stk-d", "web/prod", inventory.ChangeCreated, at(800)),
		row(toolPulumi, "stk-a", "web/live", inventory.ChangeChanged, at(800)),
	)
}

// pointsOf returns the times that the replay cases use: before the first row,
// at each row, 1 ms before each row, and after the last row.
func pointsOf(rows []inventory.Change) []time.Time {
	points := []time.Time{at(0)}

	for i := range rows {
		points = append(points, rows[i].At, rows[i].At.Add(-time.Millisecond))
	}

	return append(points, at(5000))
}

// unitsAtMatchesReplay: for a time before the first row, at each row, 1 ms
// before each row, and after the last row, UnitsAt equals ReplayAt.
func unitsAtMatchesReplay(t *testing.T, f Fixture) {
	t.Helper()

	fence := makeNamespace(t, f, "acme")
	stored := lifeCycleRows(t, f, fence)

	for _, when := range pointsOf(stored) {
		got, err := f.UnitsAt(t.Context(), "acme", when)
		if err != nil {
			t.Fatalf("UnitsAt %s: %v", when, err)
		}

		want := inventory.ReplayAt(stored, when)
		if !slices.Equal(entryNames(got), entryNames(want)) {
			t.Fatalf("UnitsAt %s: got %v, want %v", when, entryNames(got), entryNames(want))
		}

		for i := range got {
			if got[i].Name != want[i].Name || got[i].Namespace != "acme" ||
				!got[i].LastChanged.Equal(want[i].LastChanged) {
				t.Fatalf("UnitsAt %s: entry %+v, want %+v", when, got[i], want[i])
			}
		}
	}
}

// viewAtMatchesReplayView: ViewAt equals ReplayView at the same points.
func viewAtMatchesReplayView(t *testing.T, f Fixture) {
	t.Helper()

	fence := makeNamespace(t, f, "acme")
	stored := lifeCycleRows(t, f, fence)

	for _, when := range pointsOf(stored) {
		got, err := f.ViewAt(t.Context(), "acme", when)
		if err != nil {
			t.Fatalf("ViewAt %s: %v", when, err)
		}

		want := inventory.ReplayView(stored, when)
		if got.Purged != want.Purged || !slices.Equal(names(got.Units), names(want.Units)) {
			t.Fatalf("ViewAt %s: got %v purged %d, want %v purged %d",
				when, names(got.Units), got.Purged, names(want.Units), want.Purged)
		}

		for i := range got.Units {
			if got.Units[i].CurrentName != want.Units[i].CurrentName ||
				!got.Units[i].DeletedAt.Equal(want.Units[i].DeletedAt) {
				t.Fatalf("ViewAt %s: unit %+v, want %+v", when, got.Units[i], want.Units[i])
			}
		}
	}
}

// sameMillisecond: rows with the same time replay in commit order.
func sameMillisecond(t *testing.T, f Fixture) {
	t.Helper()

	fence := makeNamespace(t, f, "acme")
	appendAll(t, f, fence,
		row(toolPulumi, "stk-a", "web/one", inventory.ChangeCreated, at(50)),
		row(toolPulumi, "stk-a", "web/two", inventory.ChangeRenamed, at(50)),
		row(toolPulumi, "stk-a", "web/three", inventory.ChangeRenamed, at(50)),
		row(toolPulumi, "stk-e", "web/gone", inventory.ChangeCreated, at(50)),
		row(toolPulumi, "stk-e", "web/gone", inventory.ChangeDeleted, at(50)),
	)

	entries, err := f.UnitsAt(t.Context(), "acme", at(50))
	if err != nil {
		t.Fatalf("UnitsAt: %v", err)
	}

	if !slices.Equal(entryNames(entries), []string{"pulumi:web/three:stk-a"}) {
		t.Fatalf("got %v, want the last row of each unit", entryNames(entries))
	}

	got, err := f.UnitNames(t.Context(), "acme", toolPulumi, "stk-a", at(50))
	if err != nil || got != (inventory.Names{AtTime: "web/three", Current: "web/three"}) {
		t.Fatalf("UnitNames: %+v, err %v", got, err)
	}
}

// clockSkew: a row with an earlier time and a higher sequence wins for its
// unit.
func clockSkew(t *testing.T, f Fixture) {
	t.Helper()

	fence := makeNamespace(t, f, "acme")
	appendAll(t, f, fence,
		row(toolPulumi, "stk-a", "web/old", inventory.ChangeCreated, at(200)),
		row(toolPulumi, "stk-a", "web/new", inventory.ChangeRenamed, at(100)),
	)

	entries, err := f.UnitsAt(t.Context(), "acme", at(300))
	if err != nil || !slices.Equal(entryNames(entries), []string{"pulumi:web/new:stk-a"}) {
		t.Fatalf("got %v, err %v, want the row with the higher sequence", entryNames(entries), err)
	}

	// At 150 only the skewed row is at or before the time.
	entries, err = f.UnitsAt(t.Context(), "acme", at(150))
	if err != nil || !slices.Equal(entryNames(entries), []string{"pulumi:web/new:stk-a"}) {
		t.Fatalf("at 150: got %v, err %v", entryNames(entries), err)
	}
}

// deletedAndPurged: a deleted or purged unit leaves UnitsAt, a restored unit
// comes back, and a renamed unit has its new name.
func deletedAndPurged(t *testing.T, f Fixture) {
	t.Helper()

	fence := makeNamespace(t, f, "acme")
	lifeCycleRows(t, f, fence)

	cases := []struct {
		name string
		when time.Time
		want []string
	}{
		{name: "before the first row", when: at(50), want: []string{}},
		{name: "after a create", when: at(100), want: []string{"pulumi:web/prod:stk-a"}},
		{name: "a rename", when: at(300), want: []string{"opentofu:network:ws-b", "pulumi:web/live:stk-a"}},
		{name: "a deleted unit is left out", when: at(400), want: []string{"pulumi:web/live:stk-a"}},
		{
			name: "a restored unit comes back",
			when: at(500),
			want: []string{"opentofu:network:ws-b", "pulumi:web/live:stk-a"},
		},
		{
			name: "a unit before its purge",
			when: at(600),
			want: []string{"opentofu:network:ws-b", "pulumi:api/prod:stk-c", "pulumi:web/live:stk-a"},
		},
		{
			name: "a purged unit is left out",
			when: at(700),
			want: []string{"opentofu:network:ws-b", "pulumi:web/live:stk-a"},
		},
	}

	for _, tc := range cases {
		got, err := f.UnitsAt(t.Context(), "acme", tc.when)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		if !slices.Equal(entryNames(got), tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, entryNames(got), tc.want)
		}
	}
}

// purgeKeepsRows: after a purged row, ListChanges still returns every row of
// the unit.
func purgeKeepsRows(t *testing.T, f Fixture) {
	t.Helper()

	fence := makeNamespace(t, f, "acme")
	stored := appendAll(t, f, fence,
		row(toolPulumi, "stk-a", "web/prod", inventory.ChangeCreated, at(1)),
		row(toolPulumi, "stk-a", "web/prod", inventory.ChangeDeleted, at(2)),
		row(toolPulumi, "stk-a", "web/prod", inventory.ChangePurged, at(3)),
	)

	listed := listAll(t, f, "acme")
	if !slices.Equal(seqs(listed), seqs(stored)) {
		t.Fatalf("got %v, want every row %v", seqs(listed), seqs(stored))
	}

	if listed[2].Kind != inventory.ChangePurged || listed[2].Name != "web/prod" {
		t.Fatalf("the purged row is %+v", listed[2])
	}
}

// namespaceRecreated: after a purge of the namespace, a new namespace with
// the same name reads no row of the old one.
func namespaceRecreated(t *testing.T, f Fixture) {
	t.Helper()

	fence := makeNamespace(t, f, "acme")
	appendAll(t, f, fence, row(toolPulumi, "stk-a", "web/prod", inventory.ChangeCreated, at(1)))

	if err := f.PurgeNamespace(t.Context(), "acme"); err != nil {
		t.Fatalf("purge namespace: %v", err)
	}

	fresh := makeNamespace(t, f, "acme")

	if rows := listAll(t, f, "acme"); len(rows) != 0 {
		t.Fatalf("the new namespace reads %d rows of the old one", len(rows))
	}

	if entries, err := f.UnitsAt(t.Context(), "acme", at(10)); err != nil || len(entries) != 0 {
		t.Fatalf("UnitsAt: %d entries, err %v", len(entries), err)
	}

	stored := appendAll(t, f, fresh, row(toolPulumi, "stk-z", "web/new", inventory.ChangeCreated, at(2)))

	rows := listAll(t, f, "acme")
	if len(rows) != 1 || rows[0].UnitID != "stk-z" || rows[0].Seq != stored[0].Seq {
		t.Fatalf("the new namespace reads %+v, want only its own row", rows)
	}

	view, err := f.ViewAt(t.Context(), "acme", at(10))
	if err != nil || len(view.Units) != 1 || view.Purged != 0 {
		t.Fatalf("ViewAt: %+v, err %v", view, err)
	}
}

// staleFence: an append with a stale fence answers namespace.ErrFenced and
// writes no row.
func staleFence(t *testing.T, f Fixture) {
	t.Helper()

	fence := makeNamespace(t, f, "acme")
	stale := namespace.Fence{Namespace: fence.Namespace, FailoverVersion: fence.FailoverVersion - 1}

	_, err := f.AppendChange(t.Context(), row(toolPulumi, "stk-a", "web/prod", inventory.ChangeCreated, at(1)), stale)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("got %v, want namespace.ErrFenced", err)
	}

	if rows := listAll(t, f, "acme"); len(rows) != 0 {
		t.Fatalf("a stale append wrote %d rows", len(rows))
	}
}

// viewAtLifeCycle: the view names a soft-deleted unit with its delete time,
// names a restored unit as live, and counts a purged unit without naming it.
func viewAtLifeCycle(t *testing.T, f Fixture) {
	t.Helper()

	fence := makeNamespace(t, f, "acme")
	appendAll(t, f, fence,
		row(toolPulumi, "stk-a", "web/prod", inventory.ChangeCreated, at(100)),
		row(toolOpenTofu, "ws-b", "network", inventory.ChangeCreated, at(110)),
		row(toolOpenTofu, "ws-r", "restored", inventory.ChangeCreated, at(120)),
		row(toolPulumi, "stk-p", "api/prod", inventory.ChangeCreated, at(130)),
		row(toolPulumi, "stk-a", "web/live", inventory.ChangeRenamed, at(200)),
		row(toolOpenTofu, "ws-b", "network", inventory.ChangeDeleted, at(210)),
		row(toolOpenTofu, "ws-r", "restored", inventory.ChangeDeleted, at(220)),
		row(toolOpenTofu, "ws-r", "restored", inventory.ChangeRestored, at(230)),
		row(toolPulumi, "stk-p", "api/prod", inventory.ChangeDeleted, at(240)),
		row(toolPulumi, "stk-p", "api/prod", inventory.ChangePurged, at(250)),
		row(toolPulumi, "stk-n", "after", inventory.ChangeCreated, at(300)),
	)

	view, err := f.ViewAt(t.Context(), "acme", at(150))
	if err != nil {
		t.Fatalf("ViewAt: %v", err)
	}

	want := []string{"opentofu:network:ws-b", "opentofu:restored:ws-r", "pulumi:web/prod:stk-a"}
	if !slices.Equal(names(view.Units), want) || view.Purged != 1 {
		t.Fatalf("got %v purged %d, want %v purged 1", names(view.Units), view.Purged, want)
	}

	assertLifeCycleUnits(t, view)

	// A unit that is deleted at the time is not live at the time, and a
	// purge that came before the time is not a purge since.
	view, err = f.ViewAt(t.Context(), "acme", at(260))
	if err != nil {
		t.Fatalf("ViewAt 260: %v", err)
	}

	want = []string{"opentofu:restored:ws-r", "pulumi:web/live:stk-a"}
	if !slices.Equal(names(view.Units), want) || view.Purged != 0 {
		t.Fatalf("at 260: got %v purged %d, want %v purged 0", names(view.Units), view.Purged, want)
	}
}

// viewAtBeforeFirstChange: a time before the first row, and a namespace with
// no row, read as an empty view.
func viewAtBeforeFirstChange(t *testing.T, f Fixture) {
	t.Helper()

	fence := makeNamespace(t, f, "acme")

	view, err := f.ViewAt(t.Context(), "acme", at(10))
	if err != nil || len(view.Units) != 0 || view.Purged != 0 {
		t.Fatalf("namespace with no row: %+v, err %v", view, err)
	}

	appendAll(t, f, fence, row(toolPulumi, "stk-a", "web/prod", inventory.ChangeCreated, at(100)))

	view, err = f.ViewAt(t.Context(), "acme", at(99))
	if err != nil || len(view.Units) != 0 || view.Purged != 0 {
		t.Fatalf("before the first row: %+v, err %v", view, err)
	}

	got, err := f.UnitNames(t.Context(), "acme", toolPulumi, "stk-a", at(99))
	if err != nil || got != (inventory.Names{AtTime: "", Current: "web/prod"}) {
		t.Fatalf("names before the first row: %+v, err %v", got, err)
	}
}

// viewAtRenameChain: a unit that was renamed several times has the name of
// the time in the entry and its last name as the current name.
func viewAtRenameChain(t *testing.T, f Fixture) {
	t.Helper()

	fence := makeNamespace(t, f, "acme")
	appendAll(t, f, fence,
		row(toolPulumi, "stk-a", "web/a", inventory.ChangeCreated, at(100)),
		row(toolPulumi, "stk-a", "web/b", inventory.ChangeRenamed, at(200)),
		row(toolPulumi, "stk-a", "web/c", inventory.ChangeRenamed, at(300)),
		row(toolPulumi, "stk-a", "web/a", inventory.ChangeRenamed, at(400)),
	)

	cases := []struct {
		when time.Time
		want string
	}{
		{when: at(100), want: "web/a"},
		{when: at(250), want: "web/b"},
		{when: at(300), want: "web/c"},
		{when: at(1000), want: "web/a"},
	}

	for _, tc := range cases {
		view, err := f.ViewAt(t.Context(), "acme", tc.when)
		if err != nil || len(view.Units) != 1 {
			t.Fatalf("at %s: %+v, err %v", tc.when, view, err)
		}

		if got := view.Units[0]; got.Entry.Name != tc.want || got.CurrentName != "web/a" {
			t.Fatalf("at %s: name %q current %q, want %q and web/a", tc.when, got.Entry.Name, got.CurrentName, tc.want)
		}
	}
}

// viewAtNameReuse: a name that a soft delete freed and a new unit took lists
// the unit that held the name at the time. The deleted unit keeps its name as
// the current name and is deleted.
func viewAtNameReuse(t *testing.T, f Fixture) {
	t.Helper()

	fence := makeNamespace(t, f, "acme")
	appendAll(t, f, fence,
		row(toolPulumi, "stk-a", "web/a", inventory.ChangeCreated, at(100)),
		row(toolPulumi, "stk-a", "web/a", inventory.ChangeDeleted, at(200)),
		row(toolPulumi, "stk-b", "web/a", inventory.ChangeCreated, at(300)),
	)

	cases := []struct {
		when    time.Time
		unitID  string
		deleted bool
	}{
		{when: at(150), unitID: "stk-a", deleted: true},
		{when: at(250), unitID: "", deleted: false},
		{when: at(350), unitID: "stk-b", deleted: false},
	}

	for _, tc := range cases {
		view, err := f.ViewAt(t.Context(), "acme", tc.when)
		if err != nil {
			t.Fatalf("at %s: %v", tc.when, err)
		}

		if tc.unitID == "" {
			if len(view.Units) != 0 {
				t.Fatalf("at %s: %+v, want no unit", tc.when, view.Units)
			}

			continue
		}

		if len(view.Units) != 1 {
			t.Fatalf("at %s: %+v, want one unit", tc.when, view.Units)
		}

		got := view.Units[0]
		if got.Entry.UnitID != tc.unitID || got.Entry.Name != "web/a" || got.CurrentName != "web/a" ||
			got.Deleted() != tc.deleted {
			t.Fatalf("at %s: unit %+v, want %s named web/a, deleted %t", tc.when, got, tc.unitID, tc.deleted)
		}
	}
}

// unitNamesRenameChain: UnitNames follows a rename chain, including a return
// to an earlier name.
func unitNamesRenameChain(t *testing.T, f Fixture) {
	t.Helper()

	fence := makeNamespace(t, f, "acme")
	stored := appendAll(t, f, fence,
		row(toolPulumi, "stk-a", "web/a", inventory.ChangeCreated, at(100)),
		row(toolPulumi, "stk-a", "web/b", inventory.ChangeRenamed, at(200)),
		row(toolPulumi, "stk-a", "web/c", inventory.ChangeRenamed, at(300)),
		row(toolPulumi, "stk-a", "web/a", inventory.ChangeRenamed, at(400)),
	)

	for _, when := range pointsOf(stored) {
		got, err := f.UnitNames(t.Context(), "acme", toolPulumi, "stk-a", when)
		if err != nil {
			t.Fatalf("UnitNames %s: %v", when, err)
		}

		if want := inventory.NamesAt(stored, toolPulumi, "stk-a", when); got != want {
			t.Fatalf("UnitNames %s: got %+v, want %+v", when, got, want)
		}
	}

	got, err := f.UnitNames(t.Context(), "acme", toolPulumi, "stk-a", at(250))
	if err != nil || got != (inventory.Names{AtTime: "web/b", Current: "web/a"}) {
		t.Fatalf("at 250: %+v, err %v", got, err)
	}
}

// unitNamesLifeCycle: a soft-deleted unit keeps its name, a restore under a
// new name changes the current name, and a purged unit has no name now.
func unitNamesLifeCycle(t *testing.T, f Fixture) {
	t.Helper()

	fence := makeNamespace(t, f, "acme")
	appendAll(t, f, fence,
		row(toolOpenTofu, "ws-a", "network", inventory.ChangeCreated, at(100)),
		row(toolOpenTofu, "ws-a", "network", inventory.ChangeDeleted, at(200)),
		row(toolOpenTofu, "ws-a", "network-2", inventory.ChangeRestored, at(300)),
		row(toolOpenTofu, "ws-a", "network-2", inventory.ChangeDeleted, at(400)),
		row(toolOpenTofu, "ws-a", "network-2", inventory.ChangePurged, at(500)),
	)

	cases := []struct {
		when time.Time
		want inventory.Names
	}{
		{when: at(250), want: inventory.Names{AtTime: "network", Current: ""}},
		{when: at(350), want: inventory.Names{AtTime: "network-2", Current: ""}},
		{when: at(499), want: inventory.Names{AtTime: "network-2", Current: ""}},
		{when: at(500), want: inventory.Names{AtTime: "", Current: ""}},
	}

	for _, tc := range cases {
		got, err := f.UnitNames(t.Context(), "acme", toolOpenTofu, "ws-a", tc.when)
		if err != nil || got != tc.want {
			t.Fatalf("at %s: got %+v, want %+v, err %v", tc.when, got, tc.want, err)
		}
	}
}

// unitNamesScope: UnitNames reads one namespace and one tool.
func unitNamesScope(t *testing.T, f Fixture) {
	t.Helper()

	one := makeNamespace(t, f, "one")
	two := makeNamespace(t, f, "two")

	appendAll(t, f, one, row(toolPulumi, "x-1", "web/one", inventory.ChangeCreated, at(1)))
	appendAll(t, f, two, row(toolPulumi, "x-1", "web/two", inventory.ChangeCreated, at(1)))

	cases := []struct {
		ns   namespace.Name
		tool string
		want string
	}{
		{ns: "one", tool: toolPulumi, want: "web/one"},
		{ns: "two", tool: toolPulumi, want: "web/two"},
		{ns: "one", tool: toolOpenTofu, want: ""},
	}

	for _, tc := range cases {
		got, err := f.UnitNames(t.Context(), tc.ns, tc.tool, "x-1", at(10))
		if err != nil || got.Current != tc.want || got.AtTime != tc.want {
			t.Fatalf("%s %s: got %+v, want %q, err %v", tc.ns, tc.tool, got, tc.want, err)
		}
	}
}

// assertLifeCycleUnits checks the units of the view at 150 of viewAtLifeCycle:
// the renamed unit has its current name, the soft-deleted unit has the time of
// its delete, and the restored unit is live.
func assertLifeCycleUnits(t *testing.T, view inventory.UnitsView) {
	t.Helper()

	byID := map[string]inventory.UnitAt{}
	for i := range view.Units {
		byID[view.Units[i].Entry.UnitID] = view.Units[i]
	}

	if u := byID["stk-a"]; u.CurrentName != "web/live" || u.Deleted() {
		t.Fatalf("renamed unit: %+v, want the current name web/live and not deleted", u)
	}

	if u := byID["ws-b"]; !u.Deleted() || !u.DeletedAt.Equal(at(210)) || u.CurrentName != "network" {
		t.Fatalf("soft-deleted unit: %+v, want the delete time %s", u, at(210))
	}

	if u := byID["ws-r"]; u.Deleted() {
		t.Fatalf("restored unit: %+v, want not deleted", u)
	}
}
