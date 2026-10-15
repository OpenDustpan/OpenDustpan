package storetest

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// PagingStore is every port the namespace paging suite exercises: namespaces
// for fixtures, and namespace.Pager under test.
type PagingStore interface {
	namespace.Store
	namespace.Pager
}

// PagingFactory returns a fresh, empty store for one subtest.
type PagingFactory func(t *testing.T) PagingStore

// RunNamespacePaging executes the conformance suite of the namespace paging
// port (namespace.Pager) against stores produced by newStore.
func RunNamespacePaging(t *testing.T, newStore PagingFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s PagingStore)
		name string
	}{
		{name: "all namespaces page in name order", test: testPagingAllInOrder},
		{name: "all with after past the end gives none", test: testPagingAllPastEnd},
		{name: "a subset keeps only the named namespaces", test: testPagingSubset},
		{name: "empty names give none and count zero", test: testPagingEmptyNames},
		{name: "a subset page starts after the cursor", test: testPagingSubsetAfter},
		{name: "a limit larger than the set reads the set", test: testPagingLargeLimit},
		{name: "counts follow the same rule as pages", test: testPagingCounts},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// pagingSeed holds 25 names with digits, a hyphen and upper- and lowercase
// letters, so a collation that is not byte order would show in the order.
var pagingSeed = []string{ //nolint:gochecknoglobals // Fixed fixture of the suite, never written.
	"0", "00", "0-0",
	"a", "a-", "a-0", "a0", "a00", "a1", "a2", "a10", "a-1", "a-10", "a-2",
	"Z", "Z-1", "Z10", "Z2",
	"acme", "acme-2", "acme-10", "acme1", "acme10", "acme2",
	"omega",
}

// seedPagingNamespaces creates one namespace per seed name and returns the
// names in byte order, which is the order every page must follow.
func seedPagingNamespaces(t *testing.T, s PagingStore) []namespace.Name {
	t.Helper()

	for _, name := range pagingSeed {
		newNamespace(t, s, name)
	}

	want := make([]namespace.Name, 0, len(pagingSeed))
	for _, name := range pagingSeed {
		want = append(want, namespace.Name(name))
	}

	slices.Sort(want)

	return want
}

// pageNames returns the names of a page, in order.
func pageNames(page []namespace.Namespace) []namespace.Name {
	out := make([]namespace.Name, 0, len(page))
	for i := range page {
		out = append(out, page[i].Name)
	}

	return out
}

// collectAll walks every page from After with limit and returns the names
// in the order the store gave them.
func collectAll(t *testing.T, s PagingStore, q namespace.PageQuery) []namespace.Name {
	t.Helper()

	var out []namespace.Name

	for after := q.After; ; {
		page, err := s.PageNamespaces(t.Context(), namespace.PageQuery{
			After:  after,
			Limit:  q.Limit,
			Filter: q.Filter,
		})
		if err != nil {
			t.Fatalf("page namespaces after %q: %v", after, err)
		}

		if len(page) == 0 {
			return out
		}

		if len(page) > q.Limit {
			t.Fatalf("page after %q holds %d namespaces, want at most %d", after, len(page), q.Limit)
		}

		out = append(out, pageNames(page)...)
		after = page[len(page)-1].Name
	}
}

// testPagingAllInOrder pages every namespace with limit 2 and checks the
// walk gives each namespace once, in name order.
func testPagingAllInOrder(t *testing.T, s PagingStore) {
	t.Helper()

	want := seedPagingNamespaces(t, s)

	got := collectAll(t, s, namespace.PageQuery{After: "", Limit: 2, Filter: namespace.Filter{All: true, Names: nil}})
	if !slices.Equal(got, want) {
		t.Fatalf("paged namespaces = %v, want %v", got, want)
	}
}

// testPagingAllPastEnd checks a cursor past the last name gives no page.
func testPagingAllPastEnd(t *testing.T, s PagingStore) {
	t.Helper()

	seedPagingNamespaces(t, s)

	page, err := s.PageNamespaces(t.Context(), namespace.PageQuery{
		After:  "zzzz",
		Limit:  10,
		Filter: namespace.Filter{All: true, Names: nil},
	})
	if err != nil {
		t.Fatalf("page namespaces past the end: %v", err)
	}

	if len(page) != 0 {
		t.Fatalf("page past the end = %v, want none", pageNames(page))
	}
}

// testPagingSubset checks only the named namespaces appear, unknown names
// are ignored, and the count skips them too.
func testPagingSubset(t *testing.T, s PagingStore) {
	t.Helper()

	want := seedPagingNamespaces(t, s)
	names := []namespace.Name{want[3], want[10], want[20], "ghost"}

	page, err := s.PageNamespaces(t.Context(), namespace.PageQuery{
		After:  "",
		Limit:  100,
		Filter: namespace.Filter{All: false, Names: names},
	})
	if err != nil {
		t.Fatalf("page a subset: %v", err)
	}

	if got := pageNames(page); !slices.Equal(got, names[:3]) {
		t.Fatalf("subset page = %v, want %v", got, names[:3])
	}

	total, err := s.CountNamespaces(t.Context(), namespace.Filter{All: false, Names: names})
	if err != nil {
		t.Fatalf("count a subset: %v", err)
	}

	if total != 3 {
		t.Fatalf("subset count = %d, want 3", total)
	}
}

// testPagingEmptyNames checks an empty set gives no page and count zero.
func testPagingEmptyNames(t *testing.T, s PagingStore) {
	t.Helper()

	seedPagingNamespaces(t, s)

	page, err := s.PageNamespaces(t.Context(), namespace.PageQuery{
		After:  "",
		Limit:  10,
		Filter: namespace.Filter{All: false, Names: nil},
	})
	if err != nil {
		t.Fatalf("page an empty set: %v", err)
	}

	if len(page) != 0 {
		t.Fatalf("empty set page = %v, want none", pageNames(page))
	}

	total, err := s.CountNamespaces(t.Context(), namespace.Filter{All: false, Names: nil})
	if err != nil {
		t.Fatalf("count an empty set: %v", err)
	}

	if total != 0 {
		t.Fatalf("empty set count = %d, want 0", total)
	}
}

// testPagingSubsetAfter checks a cursor inside the set starts the page
// after it, and a cursor between members starts at the next member.
func testPagingSubsetAfter(t *testing.T, s PagingStore) {
	t.Helper()

	want := seedPagingNamespaces(t, s)
	set := []namespace.Name{want[2], want[5], want[8]}

	page, err := s.PageNamespaces(t.Context(), namespace.PageQuery{
		After:  set[0],
		Limit:  10,
		Filter: namespace.Filter{All: false, Names: set},
	})
	if err != nil {
		t.Fatalf("page a subset after a member: %v", err)
	}

	if got := pageNames(page); !slices.Equal(got, set[1:]) {
		t.Fatalf("subset page after %q = %v, want %v", set[0], got, set[1:])
	}

	page, err = s.PageNamespaces(t.Context(), namespace.PageQuery{
		After:  set[2],
		Limit:  10,
		Filter: namespace.Filter{All: false, Names: set},
	})
	if err != nil {
		t.Fatalf("page a subset after its last member: %v", err)
	}

	if len(page) != 0 {
		t.Fatalf("subset page after %q = %v, want none", set[2], pageNames(page))
	}
}

// testPagingLargeLimit checks a limit larger than the set reads the set.
func testPagingLargeLimit(t *testing.T, s PagingStore) {
	t.Helper()

	want := seedPagingNamespaces(t, s)
	set := []namespace.Name{want[1], want[7], want[13]}

	page, err := s.PageNamespaces(t.Context(), namespace.PageQuery{
		After:  "",
		Limit:  100,
		Filter: namespace.Filter{All: false, Names: set},
	})
	if err != nil {
		t.Fatalf("page a subset with a large limit: %v", err)
	}

	if got := pageNames(page); !slices.Equal(got, set) {
		t.Fatalf("subset page = %v, want %v", got, set)
	}
}

// testPagingCounts checks the counts of all namespaces and of a subset,
// with unknown names ignored.
func testPagingCounts(t *testing.T, s PagingStore) {
	t.Helper()

	want := seedPagingNamespaces(t, s)

	total, err := s.CountNamespaces(t.Context(), namespace.Filter{All: true, Names: nil})
	if err != nil {
		t.Fatalf("count all namespaces: %v", err)
	}

	if total != int64(len(want)) {
		t.Fatalf("all count = %d, want %d", total, len(want))
	}

	total, err = s.CountNamespaces(
		t.Context(),
		namespace.Filter{All: false, Names: []namespace.Name{want[0], want[1], "ghost"}},
	)
	if err != nil {
		t.Fatalf("count a subset: %v", err)
	}

	if total != 2 {
		t.Fatalf("subset count = %d, want 2", total)
	}
}
