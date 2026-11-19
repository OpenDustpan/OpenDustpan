package audittest

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// All returns every entry of scope, newest first. It follows More with the
// sequence of the last entry of each page, in pages of audit.MaxPageSize, so
// the helper never runs a count and never reads an offset.
func All(tb testing.TB, s audit.Store, scope namespace.Name) []audit.Entry {
	tb.Helper()

	var (
		out    []audit.Entry
		before int64
	)

	for {
		listing, err := s.List(tb.Context(), scope, audit.Page{Size: audit.MaxPageSize, Before: before})
		if err != nil {
			tb.Fatalf("list audit entries of %s below %d: %v", scope, before, err)
		}

		out = append(out, listing.Entries...)

		if !listing.More {
			return out
		}

		if len(listing.Entries) == 0 {
			tb.Fatalf("list audit entries of %s below %d: More is true on an empty page", scope, before)
		}

		next := listing.Entries[len(listing.Entries)-1].Sequence
		if before != 0 && next >= before {
			tb.Fatalf(
				"list audit entries of %s below %d: the page ends at %d, so the walk does not advance",
				scope,
				before,
				next,
			)
		}

		before = next
	}
}

// appendUsers appends n entries to ns with the subjects user-1 to user-n.
func appendUsers(t *testing.T, s Store, ns namespace.Namespace, n int) {
	t.Helper()

	for i := 1; i <= n; i++ {
		appendEntry(t, s, ns.Fence(), newEntry(ns.Name, fmt.Sprintf("user-%d", i), fmt.Sprintf("req-%d", i), now()))
	}
}

// listPage reads one page and fails the test on an error.
func listPage(t *testing.T, s Store, scope namespace.Name, page audit.Page) audit.Listing {
	t.Helper()

	listing, err := s.List(t.Context(), scope, page)
	if err != nil {
		t.Fatalf("list %s with %+v: %v", scope, page, err)
	}

	return listing
}

// checkSubjects fails the test unless entries hold the subjects in order.
func checkSubjects(t *testing.T, entries []audit.Entry, want ...string) {
	t.Helper()

	got := make([]string, 0, len(entries))
	for i := range entries {
		got = append(got, entries[i].Subject)
	}

	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("subjects = %v, want %v", got, want)
	}
}

// testListFirstPage proves that a list with no Before holds the newest
// entries first and says that more exist.
func testListFirstPage(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)
	appendUsers(t, s, ns, 5)

	first := listPage(t, s, ns.Name, audit.Page{Size: 2, Before: 0})

	checkSubjects(t, first.Entries, "user-5", "user-4")

	if !first.More {
		t.Fatal("first page More = false, want true")
	}
}

// testListNextPageByBefore proves that the sequence of the last entry of a
// page starts the next page.
func testListNextPageByBefore(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)
	appendUsers(t, s, ns, 5)

	first := listPage(t, s, ns.Name, audit.Page{Size: 2, Before: 0})
	second := listPage(t, s, ns.Name, audit.Page{Size: 2, Before: first.Entries[1].Sequence})

	checkSubjects(t, second.Entries, "user-3", "user-2")

	if !second.More {
		t.Fatal("second page More = false, want true")
	}
}

// testListLastPageMoreFalse proves that the page with the oldest entry
// answers More false.
func testListLastPageMoreFalse(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)
	appendUsers(t, s, ns, 5)

	last := listPage(t, s, ns.Name, audit.Page{Size: 2, Before: 3})

	checkSubjects(t, last.Entries, "user-2", "user-1")

	if last.More {
		t.Fatal("last page More = true, want false")
	}

	short := listPage(t, s, ns.Name, audit.Page{Size: 2, Before: 2})

	checkSubjects(t, short.Entries, "user-1")

	if short.More {
		t.Fatal("short last page More = true, want false")
	}
}

// testListExactFitHasNoEmptyPage proves that a log of exactly one page
// answers More false, so no empty page follows it.
func testListExactFitHasNoEmptyPage(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)
	appendUsers(t, s, ns, 4)

	got := listPage(t, s, ns.Name, audit.Page{Size: 4, Before: 0})

	if len(got.Entries) != 4 || got.More {
		t.Fatalf("exact fit = %d entries, More %t, want 4 entries, More false", len(got.Entries), got.More)
	}
}

// testListEmptyScope proves that a scope with no entries lists an empty
// page, for a namespace and for the server scope.
func testListEmptyScope(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	for _, scope := range []namespace.Name{ns.Name, audit.ServerScope} {
		got := listPage(t, s, scope, audit.Page{Size: 0, Before: 0})

		if len(got.Entries) != 0 || got.More {
			t.Fatalf("empty %s = %d entries, More %t, want none", scope, len(got.Entries), got.More)
		}
	}
}

// testListStartAboveNewestReadsFirstPage proves that a Before above the
// newest sequence reads the newest entries.
func testListStartAboveNewestReadsFirstPage(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)
	appendUsers(t, s, ns, 3)

	got := listPage(t, s, ns.Name, audit.Page{Size: 2, Before: 1000})

	checkSubjects(t, got.Entries, "user-3", "user-2")

	if !got.More {
		t.Fatal("More = false, want true")
	}
}

// testListStartBelowOldestIsEmpty proves that a Before at the oldest
// sequence reads nothing.
func testListStartBelowOldestIsEmpty(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)
	appendUsers(t, s, ns, 3)

	got := listPage(t, s, ns.Name, audit.Page{Size: 2, Before: 1})

	if len(got.Entries) != 0 || got.More {
		t.Fatalf("below the oldest = %d entries, More %t, want none", len(got.Entries), got.More)
	}
}

// testListNeverReturnsTheProbeRow proves that a page holds at most Size
// entries for each size, so the extra row that decides More never reaches a
// caller.
func testListNeverReturnsTheProbeRow(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)
	appendUsers(t, s, ns, 6)

	for size := 1; size <= 7; size++ {
		got := listPage(t, s, ns.Name, audit.Page{Size: size, Before: 0})

		want := min(size, 6)
		if len(got.Entries) != want {
			t.Fatalf("size %d holds %d entries, want %d", size, len(got.Entries), want)
		}

		if got.More != (size < 6) {
			t.Fatalf("size %d More = %t, want %t", size, got.More, size < 6)
		}
	}
}

// countingStore counts the List calls of the store that it wraps.
type countingStore struct {
	audit.Store

	calls int
}

func (c *countingStore) List(ctx context.Context, ns namespace.Name, page audit.Page) (audit.Listing, error) {
	c.calls++

	return c.Store.List(ctx, ns, page)
}

// testAllFollowsMoreAndStops proves that All reads a log of 450 entries in
// three pages of 200, newest first, with no entry twice.
func testAllFollowsMoreAndStops(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	const total = 450

	appendUsers(t, s, ns, total)

	counter := &countingStore{Store: s, calls: 0}
	entries := All(t, counter, ns.Name)

	if len(entries) != total {
		t.Fatalf("All holds %d entries, want %d", len(entries), total)
	}

	if counter.calls != 3 {
		t.Fatalf("All made %d reads, want 3", counter.calls)
	}

	for i := range entries {
		if want := int64(total - i); entries[i].Sequence != want {
			t.Fatalf("entry %d has sequence %d, want %d", i, entries[i].Sequence, want)
		}
	}
}

// testListStableWhileLogGrows proves that an entry appended between two
// pages is in no later page: the next page starts below a sequence, so a
// new entry above it cannot move an entry from one page to the next.
func testListStableWhileLogGrows(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)
	appendUsers(t, s, ns, 5)

	first := listPage(t, s, ns.Name, audit.Page{Size: 2, Before: 0})

	appendEntry(t, s, ns.Fence(), newEntry(ns.Name, "late", "req-late", now()))

	second := listPage(t, s, ns.Name, audit.Page{Size: 2, Before: first.Entries[1].Sequence})

	checkSubjects(t, second.Entries, "user-3", "user-2")
}

// testListPagesOverAGap proves that a deleted entry leaves no duplicate and
// no miss in the pages, and that the chain check still reports the gap.
func testListPagesOverAGap(t *testing.T, ts TestStore) {
	t.Helper()

	if ts.DeleteEntry == nil {
		t.Fatal("suite needs DeleteEntry to prove the paging over a gap")
	}

	s := ts.Store
	ns := newNamespace(t, s)
	appendUsers(t, s, ns, 6)

	if err := ts.DeleteEntry(t.Context(), ns.Name, 4); err != nil {
		t.Fatalf("delete entry 4: %v", err)
	}

	first := listPage(t, s, ns.Name, audit.Page{Size: 2, Before: 0})
	checkSubjects(t, first.Entries, "user-6", "user-5")

	second := listPage(t, s, ns.Name, audit.Page{Size: 2, Before: first.Entries[1].Sequence})
	checkSubjects(t, second.Entries, "user-3", "user-2")

	third := listPage(t, s, ns.Name, audit.Page{Size: 2, Before: second.Entries[1].Sequence})
	checkSubjects(t, third.Entries, "user-1")

	if third.More {
		t.Fatal("last page More = true, want false")
	}

	check, err := s.Check(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("check audit chain: %v", err)
	}

	if check.Valid {
		t.Fatalf("check = %+v, want the gap reported", check)
	}
}

// refused is a hostile row that the store must refuse with ErrInvalidPage.
func refused(name string, scope namespace.Name, page audit.Page) hostileCase {
	return hostileCase{name: name, scope: scope, page: page, wantOwn: "", wantCount: 0, wantErr: true}
}

// reads is a hostile row that must read count entries of scope, each with a
// subject that starts with own.
func reads(name string, scope namespace.Name, page audit.Page, count int, own string) hostileCase {
	return hostileCase{name: name, scope: scope, page: page, wantOwn: own, wantCount: count, wantErr: false}
}

// hostileCase is one row of the hostile table of the store layer.
type hostileCase struct {
	name      string
	scope     namespace.Name
	wantOwn   string
	page      audit.Page
	wantCount int
	wantErr   bool
}

// hostileScopes holds the two namespaces of the hostile table.
type hostileScopes struct {
	big, small namespace.Namespace
}

// seedHostileScopes makes the scopes that the hostile table reads: the
// namespace acme with 40 entries, the namespace small with 3, and the server
// scope with 3. It returns acme and small.
func seedHostileScopes(t *testing.T, s Store) hostileScopes {
	t.Helper()

	big := newNamespace(t, s)

	small := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now(),
		Name:                "small",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(t.Context(), small); err != nil {
		t.Fatalf("create namespace %q: %v", small.Name, err)
	}

	appendUsers(t, s, big, 40)

	for i := 1; i <= 3; i++ {
		appendEntry(
			t,
			s,
			small.Fence(),
			newEntry(small.Name, fmt.Sprintf("small-%d", i), fmt.Sprintf("req-s%d", i), now()),
		)

		_, err := s.AppendServer(t.Context(), newServerEntry(fmt.Sprintf("server-%d", i), fmt.Sprintf("req-v%d", i)))
		if err != nil {
			t.Fatalf("append server entry: %v", err)
		}
	}

	return hostileScopes{big: big, small: small}
}

// hostileCases returns the rows of the hostile table for the scopes of
// seedHostileScopes.
func hostileCases(big, small namespace.Name) []hostileCase {
	all := audit.Page{Size: audit.MaxPageSize, Before: 0}

	at := func(before int64) audit.Page { return audit.Page{Size: all.Size, Before: before} }

	return []hostileCase{
		refused("before -1", big, audit.Page{Size: 0, Before: -1}),
		refused("before smallest int64", big, audit.Page{Size: 0, Before: math.MinInt64}),
		refused("size -1", big, audit.Page{Size: -1, Before: 0}),
		refused("size 201", big, audit.Page{Size: audit.MaxPageSize + 1, Before: 0}),
		refused("size smallest int", big, audit.Page{Size: math.MinInt, Before: 0}),
		refused("size largest int", big, audit.Page{Size: math.MaxInt, Before: 0}),
		reads("before of a larger scope", small, at(30), 3, "small-"),
		reads("before above the small namespace", small, at(40), 3, "small-"),
		reads("server scope with namespace sequences", audit.ServerScope, at(30), 3, "server-"),
		reads("namespace with server sequences", big, at(3), 2, "user-"),
		reads("before largest int64", small, at(math.MaxInt64), 3, "small-"),
		reads("size 1 holds one entry", big, audit.Page{Size: 1, Before: 0}, 1, "user-"),
		reads("scope with SQL text", "x' OR '1'='1", all, 0, ""),
		reads("scope with a comment and a union", "a' UNION SELECT hash FROM audit_entries --", all, 0, ""),
	}
}

// checkHostileRow sends the page of tc straight to the store.
func checkHostileRow(t *testing.T, s Store, tc hostileCase) {
	t.Helper()

	listing, err := s.List(t.Context(), tc.scope, tc.page)

	if tc.wantErr {
		if !errors.Is(err, audit.ErrInvalidPage) || len(listing.Entries) != 0 {
			t.Fatalf(
				"List(%+v) = %d entries, %v; want no entry and %v",
				tc.page,
				len(listing.Entries),
				err,
				audit.ErrInvalidPage,
			)
		}

		return
	}

	if err != nil {
		t.Fatalf("List(%+v): %v", tc.page, err)
	}

	if len(listing.Entries) != tc.wantCount {
		t.Fatalf("List(%+v) = %d entries, want %d", tc.page, len(listing.Entries), tc.wantCount)
	}

	for i := range listing.Entries {
		e := listing.Entries[i]
		if e.Namespace != tc.scope || !strings.HasPrefix(e.Subject, tc.wantOwn) {
			t.Fatalf("List(%+v) read %s entry %q of scope %q", tc.page, tc.scope, e.Subject, e.Namespace)
		}
	}
}

// testListHostilePageRows is the hostile table of the store layer. Each
// row sends a page straight to the adapter, as if the handler and the
// domain function had failed to stop it. A row that names an error must get
// audit.ErrInvalidPage. A row that names no error must read the entries of
// its own scope only, at most Size of them.
func testListHostilePageRows(t *testing.T, ts TestStore) {
	t.Helper()

	scopes := seedHostileScopes(t, ts.Store)

	for _, tc := range hostileCases(scopes.big.Name, scopes.small.Name) {
		t.Run(tc.name, func(t *testing.T) {
			checkHostileRow(t, ts.Store, tc)
		})
	}
}
