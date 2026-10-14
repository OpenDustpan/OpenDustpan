package audittest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
)

// EntryStore is every port the entry reader cases exercise.
type EntryStore interface {
	Store
	audit.ServerStore
	audit.EntryReader
}

// EntryFactory returns a fresh, empty store for one entry reader subtest.
type EntryFactory func(t *testing.T) EntryStore

// RunEntryReader executes the conformance cases of audit.EntryReader against
// stores produced by newStore.
func RunEntryReader(t *testing.T, newStore EntryFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s EntryStore)
		name string
	}{
		{name: "entry at answers the entry of that sequence", test: testEntryAt},
		{name: "entry at a missing sequence is not found", test: testEntryAtMissing},
		{name: "last entry answers the newest entry", test: testLastEntry},
		{name: "last entry of an empty scope is not found", test: testLastEntryEmpty},
		{name: "the server scope reads as a scope", test: testEntryReaderServerScope},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// appendThree appends three entries to a new namespace acme.
func appendThree(t *testing.T, s EntryStore) []audit.Entry {
	t.Helper()

	ns := newNamespace(t, s)

	out := make([]audit.Entry, 0, 3)
	for _, req := range []string{"req-1", "req-2", "req-3"} {
		out = append(out, appendEntry(t, s, ns.Fence(), newEntry(ns.Name, "admin", req, now())))
	}

	return out
}

func testEntryAt(t *testing.T, s EntryStore) {
	t.Helper()

	stored := appendThree(t, s)

	got, err := s.EntryAt(t.Context(), "acme", 2)
	if err != nil {
		t.Fatalf("entry at 2: %v", err)
	}

	if got.Sequence != 2 || got.RequestID != "req-2" || got.Hash != stored[1].Hash {
		t.Fatalf("entry at 2: got %+v, want %+v", got, stored[1])
	}
}

func testEntryAtMissing(t *testing.T, s EntryStore) {
	t.Helper()

	appendThree(t, s)

	for _, seq := range []int64{0, 4} {
		if _, err := s.EntryAt(t.Context(), "acme", seq); !errors.Is(err, audit.ErrEntryNotFound) {
			t.Fatalf("entry at %d: got %v, want ErrEntryNotFound", seq, err)
		}
	}

	if _, err := s.EntryAt(t.Context(), "globex", 1); !errors.Is(err, audit.ErrEntryNotFound) {
		t.Fatalf("entry of another scope: got %v, want ErrEntryNotFound", err)
	}
}

func testLastEntry(t *testing.T, s EntryStore) {
	t.Helper()

	stored := appendThree(t, s)

	got, err := s.LastEntry(t.Context(), "acme")
	if err != nil {
		t.Fatalf("last entry: %v", err)
	}

	if got.Sequence != 3 || got.Hash != stored[2].Hash {
		t.Fatalf("last entry: got %+v, want %+v", got, stored[2])
	}
}

func testLastEntryEmpty(t *testing.T, s EntryStore) {
	t.Helper()

	newNamespace(t, s)

	if _, err := s.LastEntry(t.Context(), "acme"); !errors.Is(err, audit.ErrEntryNotFound) {
		t.Fatalf("last entry of an empty scope: got %v, want ErrEntryNotFound", err)
	}
}

func testEntryReaderServerScope(t *testing.T, s EntryStore) {
	t.Helper()

	e := newEntry(audit.ServerScope, "admin", "req-server", now())
	e.Kind = audit.KindChange
	e.Object = audit.ServerScope.String()

	stored, err := s.AppendServer(t.Context(), e)
	if err != nil {
		t.Fatalf("append server entry: %v", err)
	}

	got, err := s.EntryAt(t.Context(), audit.ServerScope, 1)
	if err != nil || got.RequestID != stored.RequestID {
		t.Fatalf("server entry at 1: got %+v, %v, want %+v", got, err, stored)
	}

	last, err := s.LastEntry(t.Context(), audit.ServerScope)
	if err != nil || last.Sequence != 1 {
		t.Fatalf("last server entry: got %+v, %v, want sequence 1", last, err)
	}
}
