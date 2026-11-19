package audittest

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// All returns every entry of scope, newest first. It follows More with the
// sequence of the last entry of each page, in pages of audit.MaxPageSize, so
// the helper never runs a count and never reads an offset.
func All(t testing.TB, s audit.Store, scope namespace.Name) []audit.Entry {
	t.Helper()

	var (
		out    []audit.Entry
		before int64
	)

	for {
		listing, err := s.List(t.Context(), scope, audit.Page{Size: audit.MaxPageSize, Before: before})
		if err != nil {
			t.Fatalf("list audit entries of %s below %d: %v", scope, before, err)
		}

		out = append(out, listing.Entries...)

		if !listing.More {
			return out
		}

		if len(listing.Entries) == 0 {
			t.Fatalf("list audit entries of %s below %d: More is true on an empty page", scope, before)
		}

		next := listing.Entries[len(listing.Entries)-1].Sequence
		if before != 0 && next >= before {
			t.Fatalf("list audit entries of %s below %d: the page ends at %d, so the walk does not advance", scope, before, next)
		}

		before = next
	}
}

// appendN appends n entries to ns with the subjects user-1 to user-n.
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

	first := listPage(t, s, ns.Name, audit.Page{Size: 2})

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

	first := listPage(t, s, ns.Name, audit.Page{Size: 2})
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

	got := listPage(t, s, ns.Name, audit.Page{Size: 4})

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
		got := listPage(t, s, scope, audit.Page{})

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
		got := listPage(t, s, ns.Name, audit.Page{Size: size})

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

	counter := &countingStore{Store: s}
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

	first := listPage(t, s, ns.Name, audit.Page{Size: 2})

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

	first := listPage(t, s, ns.Name, audit.Page{Size: 2})
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

// testListHostilePageRows is the hostile table of the store layer. Each
// row sends a page straight to the adapter, as if the handler and the
// domain function had failed to stop it. A row that names an error must get
// audit.ErrInvalidPage. A row that names no error must read the entries of
// its own scope only, at most Size of them.
func testListHostilePageRows(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
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
		appendEntry(t, s, small.Fence(), newEntry(small.Name, fmt.Sprintf("small-%d", i), fmt.Sprintf("req-s%d", i), now()))
	}

	for i := 1; i <= 3; i++ {
		if _, err := s.AppendServer(t.Context(), newServerEntry(fmt.Sprintf("server-%d", i), fmt.Sprintf("req-v%d", i))); err != nil {
			t.Fatalf("append server entry: %v", err)
		}
	}

	cases := []struct {
		name      string
		scope     namespace.Name
		wantErr   bool
		page      audit.Page
		wantCount int
		wantOwn   string
	}{
		{name: "before -1", scope: big.Name, page: audit.Page{Before: -1}, wantErr: true},
		{name: "before smallest int64", scope: big.Name, page: audit.Page{Before: math.MinInt64}, wantErr: true},
		{name: "size -1", scope: big.Name, page: audit.Page{Size: -1}, wantErr: true},
		{name: "size 201", scope: big.Name, page: audit.Page{Size: audit.MaxPageSize + 1}, wantErr: true},
		{name: "size smallest int", scope: big.Name, page: audit.Page{Size: math.MinInt}, wantErr: true},
		{name: "size largest int", scope: big.Name, page: audit.Page{Size: math.MaxInt}, wantErr: true},
		{name: "before of a larger scope", scope: small.Name, page: audit.Page{Size: 200, Before: 30}, wantCount: 3, wantOwn: "small-"},
		{name: "before of a larger scope above the namespace", scope: small.Name, page: audit.Page{Size: 200, Before: 40}, wantCount: 3, wantOwn: "small-"},
		{name: "server scope with the sequences of a namespace", scope: audit.ServerScope, page: audit.Page{Size: 200, Before: 30}, wantCount: 3, wantOwn: "server-"},
		{name: "namespace with the sequences of the server scope", scope: big.Name, page: audit.Page{Size: 200, Before: 3}, wantCount: 2, wantOwn: "user-"},
		{name: "before largest int64", scope: small.Name, page: audit.Page{Size: 200, Before: math.MaxInt64}, wantCount: 3, wantOwn: "small-"},
		{name: "size 1 holds one entry", scope: big.Name, page: audit.Page{Size: 1}, wantCount: 1, wantOwn: "user-"},
		{name: "scope with SQL text", scope: namespace.Name("x' OR '1'='1"), page: audit.Page{Size: 200}, wantCount: 0},
		{name: "scope with a comment and a union", scope: namespace.Name("a' UNION SELECT * FROM audit_entries --"), page: audit.Page{Size: 200}, wantCount: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			listing, err := s.List(t.Context(), tc.scope, tc.page)

			if tc.wantErr {
				if !errors.Is(err, audit.ErrInvalidPage) {
					t.Fatalf("List(%+v) err = %v, want %v", tc.page, err, audit.ErrInvalidPage)
				}

				if len(listing.Entries) != 0 {
					t.Fatalf("List(%+v) read %d entries with an error", tc.page, len(listing.Entries))
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
				if e.Namespace != tc.scope || len(e.Subject) < len(tc.wantOwn) || e.Subject[:len(tc.wantOwn)] != tc.wantOwn {
					t.Fatalf("List(%+v) read %s entry %q of scope %q", tc.page, tc.scope, e.Subject, e.Namespace)
				}
			}
		})
	}
}
