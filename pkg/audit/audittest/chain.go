package audittest

import (
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ChainStore is every port the chain cases exercise: the audit store, the
// server scope and the chain switch under test.
type ChainStore interface {
	Store
	audit.ServerStore
	audit.ChainSwitch
}

// ChainTestStore is one fresh, empty store with the tamper hook of
// TestStore, so a case can change an entry behind the back of the store.
type ChainTestStore struct {
	Store       ChainStore
	TamperEntry func(t *testing.T, ns namespace.Name, seq int64, field, value string)
}

// ChainFactory returns a fresh, empty store for one chain subtest.
type ChainFactory func(t *testing.T) ChainTestStore

// RunChain executes the conformance cases of audit.ChainSwitch and of the
// chain check across runs of chained and unchained entries (ADR 60
// decision 17) against stores produced by newStore.
func RunChain(t *testing.T, newStore ChainFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s ChainTestStore)
		name string
	}{
		{name: "with the chain off an entry holds no hash", test: testChainOff},
		{name: "a chained run after an unchained run checks as valid", test: testChainRuns},
		{name: "a changed field in a later chained run is found", test: testChainLaterRunTamper},
		{name: "a hash removed from a chained log is found", test: testChainStrippedHash},
		{name: "a chain turned off without a change entry is found", test: testChainOffWithoutEntry},
		{name: "chained entries are found in any scope", test: testHasChainedEntries},
		{name: "the chain record round-trips", test: testChainRecordRoundTrip},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// appendRun appends n entries to ns with request IDs that start at first.
func appendRun(t *testing.T, s ChainStore, ns namespace.Namespace, first, n int) []audit.Entry {
	t.Helper()

	out := make([]audit.Entry, 0, n)

	for i := range n {
		out = append(
			out,
			appendEntry(t, s, ns.Fence(), newEntry(ns.Name, "admin", fmt.Sprintf("req-%d", first+i), now())),
		)
	}

	return out
}

// turnOff turns the chain of s off as the server does: it first appends a
// chained entry in ServerScope that records the change.
func turnOff(t *testing.T, s ChainStore) {
	t.Helper()

	s.SetChain(true)

	e := newEntry(audit.ServerScope, "opendustpan serve", "", now())
	e.Kind = audit.KindChange
	e.Action = audit.ActionSetChain
	e.Object = audit.ServerScope.String()
	e.ValuePath = "/audit/chain/off"

	if _, err := s.AppendServer(t.Context(), e); err != nil {
		t.Fatalf("append the change entry: %v", err)
	}

	s.SetChain(false)
}

// check runs Check on ns and fails the test on error.
func check(t *testing.T, s ChainStore, ns namespace.Name) audit.CheckResult {
	t.Helper()

	got, err := s.Check(t.Context(), ns)
	if err != nil {
		t.Fatalf("check %s: %v", ns, err)
	}

	return got
}

func testChainOff(t *testing.T, ts ChainTestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	s.SetChain(false)

	run := appendRun(t, s, ns, 1, 2)
	for i := range run {
		if run[i].Hash != "" || run[i].PrevHash != "" {
			t.Fatalf("entry %d with the chain off: hash %q, previous hash %q, want both empty",
				run[i].Sequence, run[i].Hash, run[i].PrevHash)
		}
	}

	got := check(t, s, ns.Name)
	if !got.Valid || got.Unchained != 2 || got.Checked != 2 {
		t.Fatalf("check: got %+v, want valid with 2 checked and 2 unchained", got)
	}
}

func testChainRuns(t *testing.T, ts ChainTestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	appendRun(t, s, ns, 1, 2)
	turnOff(t, s)
	appendRun(t, s, ns, 3, 2)
	s.SetChain(true)

	run := appendRun(t, s, ns, 5, 2)
	if run[0].PrevHash != "" || run[0].Hash == "" {
		t.Fatalf("first entry of the new run: previous hash %q, hash %q, want an empty link and a hash",
			run[0].PrevHash, run[0].Hash)
	}

	if run[1].PrevHash != run[0].Hash {
		t.Fatal("second entry of the new run does not link to the first")
	}

	got := check(t, s, ns.Name)
	if !got.Valid || got.Unchained != 2 || got.Checked != 6 {
		t.Fatalf("check: got %+v, want valid with 6 checked and 2 unchained", got)
	}
}

func testChainLaterRunTamper(t *testing.T, ts ChainTestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	s.SetChain(false)
	appendRun(t, s, ns, 1, 1)
	s.SetChain(true)
	appendRun(t, s, ns, 2, 2)

	ts.TamperEntry(t, ns.Name, 3, "subject", "mallory")

	got := check(t, s, ns.Name)
	if got.Valid || got.BadSequence != 3 {
		t.Fatalf("check after a changed field: got %+v, want invalid at sequence 3", got)
	}
}

func testChainStrippedHash(t *testing.T, ts ChainTestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	appendRun(t, s, ns, 1, 3)

	// Someone removes the hash of the last entry of a log whose chain was
	// always on. No change entry turned the chain off.
	ts.TamperEntry(t, ns.Name, 3, "hash", "")
	ts.TamperEntry(t, ns.Name, 3, "prev_hash", "")

	got := check(t, s, ns.Name)
	if got.Valid || got.BadSequence != 3 {
		t.Fatalf("check after a removed hash: got %+v, want invalid at sequence 3", got)
	}
}

func testChainOffWithoutEntry(t *testing.T, ts ChainTestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	appendRun(t, s, ns, 1, 2)
	s.SetChain(false)
	appendRun(t, s, ns, 3, 1)

	got := check(t, s, ns.Name)
	if got.Valid || got.BadSequence != 3 {
		t.Fatalf("check after the chain went off with no change entry: got %+v, want invalid at sequence 3", got)
	}
}

func testHasChainedEntries(t *testing.T, ts ChainTestStore) {
	t.Helper()

	s := ts.Store
	has := func() bool {
		t.Helper()

		found, err := s.HasChainedEntries(t.Context())
		if err != nil {
			t.Fatalf("has chained entries: %v", err)
		}

		return found
	}

	if has() {
		t.Fatal("an empty store reports chained entries")
	}

	ns := newNamespace(t, s)

	s.SetChain(false)
	appendRun(t, s, ns, 1, 1)

	if has() {
		t.Fatal("a store with only unchained entries reports chained entries")
	}

	s.SetChain(true)

	e := newEntry(audit.ServerScope, "admin", "req-server", now())
	e.Kind = audit.KindChange
	e.Object = audit.ServerScope.String()

	if _, err := s.AppendServer(t.Context(), e); err != nil {
		t.Fatalf("append server entry: %v", err)
	}

	if !has() {
		t.Fatal("a chained entry in the server scope is not found")
	}
}

func testChainRecordRoundTrip(t *testing.T, ts ChainTestStore) {
	t.Helper()

	s := ts.Store

	read := func() audit.ChainRecord {
		t.Helper()

		got, err := s.ChainRecord(t.Context())
		if err != nil {
			t.Fatalf("chain record: %v", err)
		}

		return got
	}

	if got := read(); got.State != audit.ChainUnknown || got.EverChained {
		t.Fatalf("chain record of a new store: got %+v, want no record", got)
	}

	for _, want := range []audit.ChainRecord{
		{State: audit.ChainStateOff, EverChained: false},
		{State: audit.ChainStateOn, EverChained: true},
		{State: audit.ChainStateOff, EverChained: true},
	} {
		if err := s.SaveChainRecord(t.Context(), want); err != nil {
			t.Fatalf("save chain record %+v: %v", want, err)
		}

		if got := read(); got != want {
			t.Fatalf("chain record: got %+v, want %+v", got, want)
		}
	}
}
