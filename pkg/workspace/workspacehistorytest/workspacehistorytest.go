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
	"context"
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
// fence, the creation of workspaces that the rows belong to, and
// workspace.BlameStore under test.
type Store interface {
	namespace.Store
	// CreateWorkspace is the method of workspace.Store that makes a
	// workspace. The suite needs no other method of that port.
	CreateWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error
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
		{name: "a record for an unknown workspace is refused", test: testUnknownWorkspace},
		{name: "a record for a workspace of another namespace is refused", test: testOtherNamespace},
		{name: "a malformed blame is refused and changes nothing", test: testInvalidBlame},
		{name: "a record keeps the rows of each workspace apart", test: testWorkspacesApart},
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

// makeNamespace creates the namespace "acme" with the workspaces "ws-one" and
// "ws-two", and returns its fence.
func makeNamespace(t *testing.T, s Store) namespace.Fence {
	t.Helper()

	f := makeNamespaceNamed(t, s, "acme")
	makeWorkspace(t, s, f, "ws-one")
	makeWorkspace(t, s, f, "ws-two")

	return f
}

// makeNamespaceNamed creates the namespace name and returns its fence.
func makeNamespaceNamed(t *testing.T, s Store, name namespace.Name) namespace.Fence {
	t.Helper()

	err := s.CreateNamespace(t.Context(), namespace.Namespace{
		ID:        namespace.NewID(),
		CreatedAt: suiteTime(), Name: name, HomeRegion: "local", FailoverVersion: fenceVersion,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	})
	if err != nil {
		t.Fatalf("create namespace %q: %v", name, err)
	}

	return namespace.Fence{Namespace: name, FailoverVersion: fenceVersion}
}

// makeWorkspace creates a workspace with the ID id in the namespace of f. Its
// name is the ID.
func makeWorkspace(t *testing.T, s Store, f namespace.Fence, id workspace.ID) {
	t.Helper()

	name, err := workspace.ParseName(id.String())
	if err != nil {
		t.Fatalf("workspace name %q: %v", id, err)
	}

	err = s.CreateWorkspace(t.Context(), workspace.Workspace{
		CreatedAt: suiteTime(),
		UpdatedAt: suiteTime(),
		Lock:      workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		ID:        id,
		Ref:       workspace.Ref{Namespace: f.Namespace, Name: name},
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
		Revision:  1,
	}, f)
	if err != nil {
		t.Fatalf("create workspace %q: %v", id, err)
	}
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

func testUnknownWorkspace(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)

	err := s.RecordResourceChanges(
		t.Context(),
		"acme",
		blame("ws-missing", 1, row("ws-missing", 1, addrVPC, workspace.ResourceCreated)),
		f,
	)
	if !errors.Is(err, workspace.ErrNotFound) {
		t.Errorf("record for an unknown workspace err = %v, want ErrNotFound", err)
	}

	if cursor, err := s.BlameCursor(t.Context(), "ws-missing"); err != nil || cursor != 0 {
		t.Errorf("cursor of an unknown workspace = %d, %v, want 0", cursor, err)
	}
}

func testOtherNamespace(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)
	other := makeNamespaceNamed(t, s, "beta")
	makeWorkspace(t, s, other, "ws-beta")

	err := s.RecordResourceChanges(
		t.Context(),
		"acme",
		blame("ws-beta", 1, row("ws-beta", 1, addrVPC, workspace.ResourceCreated)),
		f,
	)
	if !errors.Is(err, workspace.ErrNotFound) {
		t.Errorf("record with the fence of another namespace err = %v, want ErrNotFound", err)
	}

	if cursor, err := s.BlameCursor(t.Context(), "ws-beta"); err != nil || cursor != 0 {
		t.Errorf("cursor of the workspace of another namespace = %d, %v, want 0", cursor, err)
	}
}

func testInvalidBlame(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)

	wrongOrdinal := row("ws-one", 2, addrVPC, workspace.ResourceCreated)
	wrongOrdinal.Ordinal = 3

	wrongVersion := row("ws-one", 2, addrVPC, workspace.ResourceCreated)
	wrongVersion.StateVersion = svOf(9)

	wrongWorkspace := row("ws-two", 2, addrVPC, workspace.ResourceCreated)

	badKind := row("ws-one", 2, addrVPC, "moved")
	emptyAddress := row("ws-one", 2, "", workspace.ResourceCreated)

	opaqueWithRow := blame("ws-one", 2, row("ws-one", 2, addrVPC, workspace.ResourceCreated))
	opaqueWithRow.Opaque = true

	noVersion := blame("ws-one", 2)
	noVersion.StateVersion = ""

	cases := []struct {
		name string
		b    workspace.ItemBlame
	}{
		{name: "a row of another ordinal", b: blame("ws-one", 2, wrongOrdinal)},
		{name: "a row of another state version", b: blame("ws-one", 2, wrongVersion)},
		{name: "a row of another workspace", b: blame("ws-one", 2, wrongWorkspace)},
		{name: "an unknown kind", b: blame("ws-one", 2, badKind)},
		{name: "an empty address", b: blame("ws-one", 2, emptyAddress)},
		{
			name: "two rows with one address",
			b: blame(
				"ws-one",
				2,
				row("ws-one", 2, addrVPC, workspace.ResourceCreated),
				row("ws-one", 2, addrVPC, workspace.ResourceUpdated),
			),
		},
		{name: "an opaque blame with a row", b: opaqueWithRow},
		{name: "an ordinal below 1", b: blame("ws-one", 0)},
		{name: "no state version", b: noVersion},
	}

	for _, tc := range cases {
		err := s.RecordResourceChanges(t.Context(), "acme", tc.b, f)
		if !errors.Is(err, workspace.ErrInvalidBlame) {
			t.Errorf("%s: err = %v, want ErrInvalidBlame", tc.name, err)
		}
	}

	if cursor, err := s.BlameCursor(t.Context(), "ws-one"); err != nil || cursor != 0 {
		t.Errorf("a refused record moved the cursor to %d, %v", cursor, err)
	}

	if b, err := s.ItemChanges(t.Context(), "ws-one", svOf(2)); err != nil || len(b.Changes) != 0 {
		t.Errorf("a refused record left %d rows, %v", len(b.Changes), err)
	}
}

func testWorkspacesApart(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)
	mustRecord(t, s, f, blame("ws-one", 1, row("ws-one", 1, addrVPC, workspace.ResourceCreated)))
	mustRecord(t, s, f, blame("ws-two", 1, row("ws-two", 1, addrSubnet, workspace.ResourceCreated)))

	got, err := s.ItemChanges(t.Context(), "ws-two", svOf(1))
	if err != nil || len(got.Changes) != 1 || got.Changes[0].Address != addrSubnet || got.Workspace != "ws-two" {
		t.Errorf("blame of ws-two = %+v, %v, want its one row", got, err)
	}

	if cursor, err := s.BlameCursor(t.Context(), "ws-two"); err != nil || cursor != 1 {
		t.Errorf("cursor of ws-two = %d, %v, want 1", cursor, err)
	}
}
