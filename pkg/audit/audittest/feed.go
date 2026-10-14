package audittest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// FeedStore is every port the feed cases exercise: namespaces for fixtures,
// the audit store to append entries, and the feed under test.
type FeedStore interface {
	Store
	audit.ServerStore
	audit.Feed
}

// FeedFactory returns a fresh, empty store for one feed subtest.
type FeedFactory func(t *testing.T) FeedStore

// feedEntries is the count of entries the feed cases append: more than one
// page of the limit the cases read with.
const feedEntries = 7

// feedLimit is the limit the paging case reads with.
const feedLimit = 3

// RunFeed executes the conformance cases of audit.Feed against stores
// produced by newStore.
func RunFeed(t *testing.T, newStore FeedFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s FeedStore)
		name string
	}{
		{name: "after answers entries above a sequence oldest first", test: testAfterPages},
		{name: "after on a namespace with no entries is empty", test: testAfterEmpty},
		{name: "after answers only entries of its namespace", test: testAfterIsolation},
		{name: "a sink with no cursor reads zero", test: testCursorZero},
		{name: "a cursor keeps its sink and namespace", test: testCursorKeys},
		{name: "a cursor never moves back", test: testCursorNeverBack},
		{name: "advance with a stale fence fails", test: testCursorStaleFence},
		{name: "advance in a missing namespace fails", test: testCursorMissingNamespace},
		{name: "the server scope reads and keeps a cursor with no fence", test: testServerScopeFeed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// appendN appends n entries to ns and answers them in order.
func appendN(t *testing.T, s FeedStore, ns namespace.Namespace, n int) []audit.Entry {
	t.Helper()

	out := make([]audit.Entry, 0, n)

	for i := range n {
		out = append(out, appendEntry(t, s, ns.Fence(), newEntry(ns.Name, "alice", "req-"+string(rune('a'+i)), now())))
	}

	return out
}

// after reads at most feedLimit entries and fails the test on error.
func after(t *testing.T, s FeedStore, ns namespace.Name, seq int64) []audit.Entry {
	t.Helper()

	got, err := s.After(t.Context(), ns, seq, feedLimit)
	if err != nil {
		t.Fatalf("after %d: %v", seq, err)
	}

	return got
}

// cursor reads a cursor and fails the test on error.
func cursor(t *testing.T, s FeedStore, sink string, ns namespace.Name) int64 {
	t.Helper()

	got, err := s.SinkCursor(t.Context(), sink, ns)
	if err != nil {
		t.Fatalf("sink cursor %s/%s: %v", sink, ns, err)
	}

	return got
}

// advance moves a cursor and fails the test on error.
func advance(t *testing.T, s FeedStore, fence namespace.Fence, sink string, seq int64) {
	t.Helper()

	if err := s.AdvanceSinkCursor(t.Context(), fence, sink, seq); err != nil {
		t.Fatalf("advance %s to %d: %v", sink, seq, err)
	}
}

func testAfterPages(t *testing.T, s FeedStore) {
	t.Helper()

	ns := newNamespace(t, s)
	want := appendN(t, s, ns, feedEntries)

	var got []audit.Entry

	var seq int64

	for {
		page := after(t, s, ns.Name, seq)
		if len(page) > feedLimit {
			t.Fatalf("page holds %d entries, limit %d", len(page), feedLimit)
		}

		if len(page) == 0 {
			break
		}

		got = append(got, page...)
		seq = page[len(page)-1].Sequence
	}

	if len(got) != len(want) {
		t.Fatalf("read %d entries, want %d", len(got), len(want))
	}

	for i := range want {
		if got[i].Sequence != want[i].Sequence || got[i].Hash != want[i].Hash || got[i].RequestID != want[i].RequestID {
			t.Fatalf("entry %d: got sequence %d request %q, want %d %q",
				i, got[i].Sequence, got[i].RequestID, want[i].Sequence, want[i].RequestID)
		}
	}
}

func testAfterEmpty(t *testing.T, s FeedStore) {
	t.Helper()

	ns := newNamespace(t, s)

	if got := after(t, s, ns.Name, 0); len(got) != 0 {
		t.Fatalf("after on an empty namespace: got %d entries", len(got))
	}

	appendN(t, s, ns, 2)

	if got := after(t, s, ns.Name, 2); len(got) != 0 {
		t.Fatalf("after the last sequence: got %d entries", len(got))
	}
}

func testAfterIsolation(t *testing.T, s FeedStore) {
	t.Helper()

	ns := newNamespace(t, s)
	other := otherNamespace(t, s)

	appendN(t, s, ns, 2)
	appendN(t, s, other, 1)

	got := after(t, s, ns.Name, 0)
	for i := range got {
		if got[i].Namespace != ns.Name {
			t.Fatalf("after %s answered an entry of %s", ns.Name, got[i].Namespace)
		}
	}

	if got := after(t, s, other.Name, 0); len(got) != 1 {
		t.Fatalf("after %s: got %d entries, want 1", other.Name, len(got))
	}
}

func testCursorZero(t *testing.T, s FeedStore) {
	t.Helper()

	ns := newNamespace(t, s)

	if got := cursor(t, s, "siem", ns.Name); got != 0 {
		t.Fatalf("cursor of a new sink: got %d, want 0", got)
	}
}

func testCursorKeys(t *testing.T, s FeedStore) {
	t.Helper()

	ns := newNamespace(t, s)
	other := otherNamespace(t, s)

	advance(t, s, ns.Fence(), "siem", 4)
	advance(t, s, ns.Fence(), "archive", 2)
	advance(t, s, other.Fence(), "siem", 9)

	checks := []struct {
		sink string
		ns   namespace.Name
		want int64
	}{
		{sink: "siem", ns: ns.Name, want: 4},
		{sink: "archive", ns: ns.Name, want: 2},
		{sink: "siem", ns: other.Name, want: 9},
		{sink: "archive", ns: other.Name, want: 0},
	}

	for _, c := range checks {
		if got := cursor(t, s, c.sink, c.ns); got != c.want {
			t.Fatalf("cursor %s/%s: got %d, want %d", c.sink, c.ns, got, c.want)
		}
	}
}

func testCursorNeverBack(t *testing.T, s FeedStore) {
	t.Helper()

	ns := newNamespace(t, s)

	advance(t, s, ns.Fence(), "siem", 5)
	advance(t, s, ns.Fence(), "siem", 3)

	if got := cursor(t, s, "siem", ns.Name); got != 5 {
		t.Fatalf("cursor after a lower advance: got %d, want 5", got)
	}

	advance(t, s, ns.Fence(), "siem", 6)

	if got := cursor(t, s, "siem", ns.Name); got != 6 {
		t.Fatalf("cursor after a higher advance: got %d, want 6", got)
	}
}

func testCursorStaleFence(t *testing.T, s FeedStore) {
	t.Helper()

	ns := newNamespace(t, s)
	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	err := s.AdvanceSinkCursor(t.Context(), stale, "siem", 1)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("advance with a stale fence: got %v, want ErrFenced", err)
	}

	if got := cursor(t, s, "siem", ns.Name); got != 0 {
		t.Fatalf("cursor after a fenced advance: got %d, want 0", got)
	}
}

func testCursorMissingNamespace(t *testing.T, s FeedStore) {
	t.Helper()

	missing := namespace.Fence{Namespace: "ghost", FailoverVersion: 1}

	err := s.AdvanceSinkCursor(t.Context(), missing, "siem", 1)
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("advance in a missing namespace: got %v, want namespace.ErrNotFound", err)
	}
}

func testServerScopeFeed(t *testing.T, s FeedStore) {
	t.Helper()

	e := newEntry(audit.ServerScope, "alice", "req-server", now())
	e.Kind = audit.KindChange
	e.Object = audit.ServerScope.String()

	stored, err := s.AppendServer(t.Context(), e)
	if err != nil {
		t.Fatalf("append server entry: %v", err)
	}

	got := after(t, s, audit.ServerScope, 0)
	if len(got) != 1 || got[0].Sequence != stored.Sequence || got[0].Hash != stored.Hash {
		t.Fatalf("after on the server scope: got %d entries, want the one appended", len(got))
	}

	advance(t, s, namespace.Fence{Namespace: audit.ServerScope, FailoverVersion: 0}, "siem", stored.Sequence)

	if c := cursor(t, s, "siem", audit.ServerScope); c != stored.Sequence {
		t.Fatalf("server scope cursor: got %d, want %d", c, stored.Sequence)
	}
}

// otherNamespace creates the namespace globex homed in eu-west.
func otherNamespace(t *testing.T, s Store) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		CreatedAt:       now(),
		Name:            "globex",
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %q: %v", ns.Name, err)
	}

	return ns
}
