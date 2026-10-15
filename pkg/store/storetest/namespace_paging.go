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
// letters, so paging must still match ListNamespaces under any collation.
var pagingSeed = []string{ //nolint:gochecknoglobals // Fixed fixture of the suite, never written.
	"0", "00", "0-0",
	"a", "a-", "a-0", "a0", "a00", "a1", "a2", "a10", "a-1", "a-10", "a-2",
	"Z", "Z-1", "Z10", "Z2",
	"acme", "acme-2", "acme-10", "acme1", "acme10", "acme2",
	"omega",
}

// storeOrder returns the names of ListNamespaces in the order the store
// gave them. Every page must follow this order.
func storeOrder(t *testing.T, s PagingStore) []namespace.Name {
	t.Helper()

	if s == nil {
		t.Fatal("paging store is nil")
	}

	all, err := s.ListNamespaces(t.Context())
	if err != nil {
		t.Fatalf("list namespaces: %v", err)
	}

	out := make([]namespace.Name, 0, len(all))
	for i := range all {
		out = append(out, all[i].Name)
	}

	return out
}

// seedPagingNamespaces creates one namespace per seed name and returns the
// names in the order of ListNamespaces.
func seedPagingNamespaces(t *testing.T, s PagingStore) []namespace.Name {
	t.Helper()

	if s == nil {
		t.Fatal("paging store is nil")
	}

	for _, name := range pagingSeed {
		newNamespace(t, s, name)
	}

	return storeOrder(t, s)
}

// filterOrder returns the names of want that are in keep, keeping the order
// of want. Unknown names in keep never match, so they stay out.
func filterOrder(t *testing.T, want, keep []namespace.Name) []namespace.Name {
	t.Helper()

	held := make(map[namespace.Name]struct{}, len(keep))
	for _, name := range keep {
		held[name] = struct{}{}
	}

	var out []namespace.Name

	for _, name := range want {
		if _, ok := held[name]; ok {
			out = append(out, name)
		}
	}

	return out
}

// afterOrder returns the names of want past the cursor name in the order
// of want. An empty cursor gives all of want. It finds the cursor by index
// in want.
func afterOrder(t *testing.T, want []namespace.Name, after namespace.Name) []namespace.Name {
	t.Helper()

	if after == "" {
		out := make([]namespace.Name, 0, len(want))
		out = append(out, want...)

		return out
	}

	at := -1

	for i, name := range want {
		if name == after {
			at = i

			break
		}
	}

	if at < 0 {
		t.Fatalf("cursor %q is not in the list %v", after, want)
	}

	out := make([]namespace.Name, 0, len(want)-at-1)
	out = append(out, want[at+1:]...)

	return out
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

	if s == nil {
		t.Fatal("paging store is nil")
	}

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
// walk gives each namespace once, in the order of ListNamespaces.
func testPagingAllInOrder(t *testing.T, s PagingStore) {
	t.Helper()

	if s == nil {
		t.Fatal("paging store is nil")
	}

	want := seedPagingNamespaces(t, s)

	got := collectAll(t, s, namespace.PageQuery{After: "", Limit: 2, Filter: namespace.Filter{All: true, Names: nil}})
	if !slices.Equal(got, want) {
		t.Fatalf("paged namespaces = %v, want %v", got, want)
	}
}

// testPagingAllPastEnd checks a cursor on the last name gives no page: no
// name comes after the last name of ListNamespaces.
func testPagingAllPastEnd(t *testing.T, s PagingStore) {
	t.Helper()

	if s == nil {
		t.Fatal("paging store is nil")
	}

	want := seedPagingNamespaces(t, s)
	if len(want) == 0 {
		t.Fatal("seeded list is empty, want seeded names")
	}

	after := want[len(want)-1]
	if tail := afterOrder(t, want, after); len(tail) != 0 {
		t.Fatalf("names after %q = %v, want none", after, tail)
	}

	page, err := s.PageNamespaces(t.Context(), namespace.PageQuery{
		After:  after,
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

// testPagingSubset checks only the named namespaces appear, in the order
// of ListNamespaces, and unknown names are ignored in the page and count.
func testPagingSubset(t *testing.T, s PagingStore) {
	t.Helper()

	if s == nil {
		t.Fatal("paging store is nil")
	}

	want := seedPagingNamespaces(t, s)
	names := []namespace.Name{want[3], want[10], want[20], "ghost"}
	filtered := filterOrder(t, want, names)

	page, err := s.PageNamespaces(t.Context(), namespace.PageQuery{
		After:  "",
		Limit:  100,
		Filter: namespace.Filter{All: false, Names: names},
	})
	if err != nil {
		t.Fatalf("page a subset: %v", err)
	}

	if got := pageNames(page); !slices.Equal(got, filtered) {
		t.Fatalf("subset page = %v, want %v", got, filtered)
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

	if s == nil {
		t.Fatal("paging store is nil")
	}

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
// after it, and a cursor on the last member of the set gives no page.
func testPagingSubsetAfter(t *testing.T, s PagingStore) {
	t.Helper()

	if s == nil {
		t.Fatal("paging store is nil")
	}

	want := seedPagingNamespaces(t, s)
	set := []namespace.Name{want[2], want[5], want[8]}
	filtered := filterOrder(t, afterOrder(t, want, set[0]), set)

	page, err := s.PageNamespaces(t.Context(), namespace.PageQuery{
		After:  set[0],
		Limit:  10,
		Filter: namespace.Filter{All: false, Names: set},
	})
	if err != nil {
		t.Fatalf("page a subset after a member: %v", err)
	}

	if got := pageNames(page); !slices.Equal(got, filtered) {
		t.Fatalf("subset page after %q = %v, want %v", set[0], got, filtered)
	}

	tail := filterOrder(t, afterOrder(t, want, set[2]), set)
	if len(tail) != 0 {
		t.Fatalf("names after %q = %v, want none", set[2], tail)
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

// testPagingLargeLimit checks a limit larger than the set reads the set in
// the order of ListNamespaces.
func testPagingLargeLimit(t *testing.T, s PagingStore) {
	t.Helper()

	if s == nil {
		t.Fatal("paging store is nil")
	}

	want := seedPagingNamespaces(t, s)
	set := []namespace.Name{want[1], want[7], want[13]}
	filtered := filterOrder(t, want, set)

	page, err := s.PageNamespaces(t.Context(), namespace.PageQuery{
		After:  "",
		Limit:  100,
		Filter: namespace.Filter{All: false, Names: set},
	})
	if err != nil {
		t.Fatalf("page a subset with a large limit: %v", err)
	}

	if got := pageNames(page); !slices.Equal(got, filtered) {
		t.Fatalf("subset page = %v, want %v", got, filtered)
	}
}

// testPagingCounts checks the counts of all namespaces and of a subset,
// with unknown names ignored.
func testPagingCounts(t *testing.T, s PagingStore) {
	t.Helper()

	if s == nil {
		t.Fatal("paging store is nil")
	}

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
