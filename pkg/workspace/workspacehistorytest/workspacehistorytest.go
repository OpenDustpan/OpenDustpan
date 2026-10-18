// Package workspacehistorytest is the conformance suite for
// workspace.BlameStore. Every built-in store runs it, and a custom store
// should too:
//
//	func TestWorkspaceBlameConformance(t *testing.T) {
//		t.Parallel()
//		workspacehistorytest.Run(t, func(t *testing.T) workspacehistorytest.Store { return newMyStore(t) })
//	}
//
// The cases of workspace.StateAtReader need the state version tables, so
// they are part of the store suite of state versions.
package workspacehistorytest

import (
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// Store is every persistence port the suite exercises: namespaces for the
// fence, and workspace.BlameStore under test.
type Store interface {
	namespace.Store
	workspace.BlameStore
}

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) Store

// Run runs every case of the suite against the stores that newStore returns.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s Store)
		name string
	}{
		{name: "a new workspace has the cursor 0 and an empty blame", test: testEmpty},
		{name: "rows of a state version read back ordered by address", test: testItemChanges},
		{name: "an opaque state version keeps its mark and has no row", test: testOpaque},
		{name: "a lineage change keeps its mark", test: testLineageChanged},
		{name: "a record at or below the cursor changes nothing", test: testRepeat},
		{name: "resource history answers newest first in pages", test: testResourceHistory},
		{name: "a stale fence refuses a record", test: testFenced},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// suiteTime() returns the fixed time of the suite.
func suiteTime() time.Time {
	return time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC)
}

// fenceVersion is the failover version of the namespace of the suite.
const fenceVersion = 2

// makeNamespace creates the namespace "acme" and returns its fence.
func makeNamespace(t *testing.T, s Store) namespace.Fence {
	t.Helper()

	err := s.CreateNamespace(t.Context(), namespace.Namespace{
		ID:        namespace.NewID(),
		CreatedAt: suiteTime(), Name: "acme", HomeRegion: "local", FailoverVersion: fenceVersion,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return namespace.Fence{Namespace: "acme", FailoverVersion: fenceVersion}
}

// svOf returns the state version ID of ordinal.
func svOf(ordinal int64) workspace.StateVersionID {
	return workspace.StateVersionID("sv-" + strconv.FormatInt(ordinal, 10))
}

// row returns a resource change of the workspace id.
func row(id workspace.ID, ordinal int64, address string, kind workspace.ResourceChangeKind) workspace.ResourceChange {
	return workspace.ResourceChange{
		At:             suiteTime().Add(time.Duration(ordinal) * time.Minute),
		Workspace:      id,
		StateVersion:   svOf(ordinal),
		Address:        address,
		Kind:           kind,
		By:             history.Actor{Subject: "ana", TokenID: "tok-1", JobID: ""},
		Ordinal:        ordinal,
		ChangedPaths:   2,
		Restored:       false,
		Forced:         false,
		LineageChanged: false,
	}
}

// mustRecord records b.
func mustRecord(t *testing.T, s Store, f namespace.Fence, b workspace.ItemBlame) {
	t.Helper()

	if err := s.RecordResourceChanges(t.Context(), "acme", b, f); err != nil {
		t.Fatalf("record ordinal %d: %v", b.Ordinal, err)
	}
}

// blame returns the readable blame of the state version at ordinal of the
// workspace id, with rows.
func blame(id workspace.ID, ordinal int64, rows ...workspace.ResourceChange) workspace.ItemBlame {
	return workspace.ItemBlame{
		Workspace:      id,
		StateVersion:   svOf(ordinal),
		Changes:        rows,
		Ordinal:        ordinal,
		Opaque:         false,
		LineageChanged: false,
	}
}

const (
	addrSubnet = "module.net.aws_subnet.a[0]"
	addrVPC    = "module.net.aws_vpc.main"
)

func testEmpty(t *testing.T, s Store) {
	t.Helper()
	makeNamespace(t, s)

	if cursor, err := s.BlameCursor(t.Context(), "ws-one"); err != nil || cursor != 0 {
		t.Errorf("cursor = %d, %v, want 0", cursor, err)
	}

	b, err := s.ItemChanges(t.Context(), "ws-one", svOf(1))
	if err != nil || len(b.Changes) != 0 || b.Opaque {
		t.Errorf("blame = %+v, %v, want empty", b, err)
	}
}

func testItemChanges(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)
	mustRecord(
		t,
		s,
		f,
		blame(
			"ws-one",
			1,
			row("ws-one", 1, addrVPC, workspace.ResourceCreated),
			row("ws-one", 1, addrSubnet, workspace.ResourceCreated),
		),
	)

	b, err := s.ItemChanges(t.Context(), "ws-one", svOf(1))
	if err != nil {
		t.Fatalf("item changes: %v", err)
	}

	addrs := make([]string, 0, len(b.Changes))
	for i := range b.Changes {
		r := b.Changes[i]
		addrs = append(addrs, r.Address)
	}

	if !slices.Equal(addrs, []string{addrSubnet, addrVPC}) {
		t.Errorf("addresses = %v, want ordered by address", addrs)
	}

	if b.Changes[0].ChangedPaths != 2 || b.Changes[0].By.Subject != "ana" || b.Changes[0].Ordinal != 1 {
		t.Errorf("row = %+v", b.Changes[0])
	}
}

func testOpaque(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)
	mustRecord(
		t,
		s,
		f,
		workspace.ItemBlame{
			Workspace: "ws-one", StateVersion: svOf(1), Changes: nil, Ordinal: 1, Opaque: true, LineageChanged: false,
		},
	)

	b, err := s.ItemChanges(t.Context(), "ws-one", svOf(1))
	if err != nil || !b.Opaque || len(b.Changes) != 0 {
		t.Errorf("blame = %+v, %v, want opaque with no row", b, err)
	}

	if cursor, err := s.BlameCursor(t.Context(), "ws-one"); err != nil || cursor != 1 {
		t.Errorf("cursor = %d, %v, want 1", cursor, err)
	}
}

func testLineageChanged(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)
	mustRecord(t, s, f, blame("ws-one", 1, row("ws-one", 1, addrVPC, workspace.ResourceCreated)))

	changed := row("ws-one", 2, addrVPC, workspace.ResourceReplaced)
	changed.LineageChanged = true
	b := blame("ws-one", 2, changed)
	b.LineageChanged = true
	mustRecord(t, s, f, b)

	got, err := s.ItemChanges(t.Context(), "ws-one", svOf(2))
	if err != nil {
		t.Fatalf("item changes: %v", err)
	}

	if !got.LineageChanged || len(got.Changes) != 1 || !got.Changes[0].LineageChanged {
		t.Errorf("blame = %+v, want the lineage change on the blame and on its row", got)
	}

	if before, err := s.ItemChanges(t.Context(), "ws-one", svOf(1)); err != nil || before.LineageChanged {
		t.Errorf("blame of ordinal 1 = %+v, %v, want no lineage change", before, err)
	}
}

func testRepeat(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)
	mustRecord(t, s, f, blame("ws-one", 2, row("ws-one", 2, addrVPC, workspace.ResourceCreated)))
	mustRecord(t, s, f, blame("ws-one", 2, row("ws-one", 2, addrVPC, workspace.ResourceCreated)))
	mustRecord(t, s, f, blame("ws-one", 1, row("ws-one", 1, addrSubnet, workspace.ResourceCreated)))

	if b, err := s.ItemChanges(t.Context(), "ws-one", svOf(2)); err != nil || len(b.Changes) != 1 {
		t.Errorf("rows of ordinal 2 after a repeat = %d, %v, want 1", len(b.Changes), err)
	}

	if b, err := s.ItemChanges(t.Context(), "ws-one", svOf(1)); err != nil || len(b.Changes) != 0 {
		t.Errorf("rows of ordinal 1 below the cursor = %d, %v, want none", len(b.Changes), err)
	}
}

func testResourceHistory(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)
	mustRecord(t, s, f, blame("ws-one", 1, row("ws-one", 1, addrVPC, workspace.ResourceCreated)))
	mustRecord(t, s, f, blame("ws-one", 2, row("ws-one", 2, addrVPC, workspace.ResourceUpdated)))
	mustRecord(t, s, f, blame("ws-one", 3, row("ws-one", 3, addrSubnet, workspace.ResourceCreated)))
	mustRecord(t, s, f, blame("ws-one", 4, row("ws-one", 4, addrVPC, workspace.ResourceDeleted)))
	mustRecord(t, s, f, blame("ws-two", 9, row("ws-two", 9, addrVPC, workspace.ResourceCreated)))

	first, err := s.ResourceHistory(t.Context(), "ws-one", addrVPC, 0, 2)
	if err != nil || len(first) != 2 {
		t.Fatalf("first page = %d rows, %v, want 2", len(first), err)
	}

	rest, err := s.ResourceHistory(t.Context(), "ws-one", addrVPC, first[len(first)-1].Ordinal, 10)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}

	items := slices.Concat(first, rest)
	ordinals := make([]int64, 0, len(items))
	for i := range items {
		r := items[i]
		ordinals = append(ordinals, r.Ordinal)
	}

	if !slices.Equal(ordinals, []int64{4, 2, 1}) {
		t.Errorf("ordinals = %v, want [4 2 1]", ordinals)
	}
}

func testFenced(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)
	stale := namespace.Fence{Namespace: f.Namespace, FailoverVersion: f.FailoverVersion - 1}

	err := s.RecordResourceChanges(
		t.Context(),
		"acme",
		blame("ws-one", 1, row("ws-one", 1, addrVPC, workspace.ResourceCreated)),
		stale,
	)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("record with a stale fence err = %v, want ErrFenced", err)
	}

	if cursor, err := s.BlameCursor(t.Context(), "ws-one"); err != nil || cursor != 0 {
		t.Errorf("a fenced record moved the cursor to %d, %v", cursor, err)
	}
}
