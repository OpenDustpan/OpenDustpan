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
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
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
		{name: "a fence of another namespace refuses a record", test: testFenceOfOtherNamespace},
		{name: "a record that skips an ordinal is refused", test: testSkippedOrdinal},
		{name: "a state version with a blame is refused at another ordinal", test: testSecondBlame},
		{name: "concurrent records of the same blame all succeed and store it once", test: testConcurrentRecord},
		{name: "summaries count the rows by kind and leave out a state version without a blame", test: testSummaries},
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
		CreatedAt: suiteTime(), Name: name, PrimaryInstallation: "local", FailoverVersion: fenceVersion,
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
		NamespaceID: "",
		CreatedAt:   suiteTime(),
		UpdatedAt:   suiteTime(),
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          id,
		Ref:         workspace.Ref{Namespace: f.Namespace, Name: name},
		Overrides:   namespace.Overrides{Forced: false, Unlocked: false},
		Revision:    1, Origin: identity.StateOrigin{Job: "", Commit: ""},
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
		ResourceCount:  0,
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
	counted := blame(
		"ws-one",
		1,
		row("ws-one", 1, addrVPC, workspace.ResourceCreated),
		row("ws-one", 1, addrSubnet, workspace.ResourceCreated),
	)
	counted.ResourceCount = 7
	mustRecord(t, s, f, counted)

	b, err := s.ItemChanges(t.Context(), "ws-one", svOf(1))
	if err != nil {
		t.Fatalf("item changes: %v", err)
	}

	if b.ResourceCount != 7 {
		t.Errorf("resource count = %d, want 7", b.ResourceCount)
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
			Workspace: "ws-one", StateVersion: svOf(1), Changes: nil, Ordinal: 1, ResourceCount: 0,
			Opaque: true, LineageChanged: false,
		},
	)

	b, err := s.ItemChanges(t.Context(), "ws-one", svOf(1))
	if err != nil || !b.Opaque || len(b.Changes) != 0 || b.ResourceCount != 0 {
		t.Errorf("blame = %+v, %v, want opaque with no row and no resource count", b, err)
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
	first := blame("ws-one", 1, row("ws-one", 1, addrSubnet, workspace.ResourceCreated))
	first.ResourceCount = 4
	second := blame("ws-one", 2, row("ws-one", 2, addrVPC, workspace.ResourceCreated))
	second.ResourceCount = 5
	mustRecord(t, s, f, first)
	mustRecord(t, s, f, second)

	again := blame("ws-one", 2, row("ws-one", 2, addrVPC, workspace.ResourceCreated))
	again.ResourceCount = 9
	mustRecord(t, s, f, again)
	mustRecord(t, s, f, first)

	b, err := s.ItemChanges(t.Context(), "ws-one", svOf(2))
	if err != nil || len(b.Changes) != 1 || b.ResourceCount != 5 {
		t.Errorf("blame of ordinal 2 after a repeat = %+v, %v, want 1 row and the first resource count 5", b, err)
	}

	b, err = s.ItemChanges(t.Context(), "ws-one", svOf(1))
	if err != nil || len(b.Changes) != 1 || b.ResourceCount != 4 {
		t.Errorf("blame of ordinal 1 after a repeat = %+v, %v, want 1 row and resource count 4", b, err)
	}

	if cursor, err := s.BlameCursor(t.Context(), "ws-one"); err != nil || cursor != 2 {
		t.Errorf("cursor = %d, %v, want 2", cursor, err)
	}
}

func testResourceHistory(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)
	mustRecord(t, s, f, blame("ws-one", 1, row("ws-one", 1, addrVPC, workspace.ResourceCreated)))
	mustRecord(t, s, f, blame("ws-one", 2, row("ws-one", 2, addrVPC, workspace.ResourceUpdated)))
	mustRecord(t, s, f, blame("ws-one", 3, row("ws-one", 3, addrSubnet, workspace.ResourceCreated)))
	mustRecord(t, s, f, blame("ws-one", 4, row("ws-one", 4, addrVPC, workspace.ResourceDeleted)))
	mustRecord(t, s, f, blame("ws-two", 1, row("ws-two", 1, addrVPC, workspace.ResourceCreated)))

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

	negativeCount := blame("ws-one", 1)
	negativeCount.ResourceCount = -1

	opaqueWithCount := blame("ws-one", 1)
	opaqueWithCount.Opaque = true
	opaqueWithCount.ResourceCount = 3

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
		{name: "a negative resource count", b: negativeCount},
		{name: "an opaque blame with a resource count", b: opaqueWithCount},
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

func testFenceOfOtherNamespace(t *testing.T, s Store) {
	t.Helper()
	makeNamespace(t, s)
	other := makeNamespaceNamed(t, s, "beta")

	err := s.RecordResourceChanges(
		t.Context(),
		"acme",
		blame("ws-one", 1, row("ws-one", 1, addrVPC, workspace.ResourceCreated)),
		other,
	)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("record with the current fence of another namespace err = %v, want ErrFenced", err)
	}

	if cursor, err := s.BlameCursor(t.Context(), "ws-one"); err != nil || cursor != 0 {
		t.Errorf("a fenced record moved the cursor to %d, %v", cursor, err)
	}
}

func testSkippedOrdinal(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)
	mustRecord(t, s, f, blame("ws-one", 1, row("ws-one", 1, addrVPC, workspace.ResourceCreated)))

	err := s.RecordResourceChanges(
		t.Context(), "acme", blame("ws-one", 3, row("ws-one", 3, addrVPC, workspace.ResourceUpdated)), f,
	)
	if !errors.Is(err, workspace.ErrInvalidBlame) {
		t.Errorf("record that skips ordinal 2 err = %v, want ErrInvalidBlame", err)
	}

	if cursor, err := s.BlameCursor(t.Context(), "ws-one"); err != nil || cursor != 1 {
		t.Errorf("cursor = %d, %v, want 1", cursor, err)
	}

	if b, err := s.ItemChanges(t.Context(), "ws-one", svOf(3)); err != nil || b.Ordinal != 0 {
		t.Errorf("blame of the skipped record = %+v, %v, want ordinal 0", b, err)
	}
}

func testSecondBlame(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)
	mustRecord(t, s, f, blame("ws-one", 1, row("ws-one", 1, addrVPC, workspace.ResourceCreated)))

	again := blame("ws-one", 2, row("ws-one", 2, addrVPC, workspace.ResourceUpdated))
	again.StateVersion = svOf(1)

	for i := range again.Changes {
		again.Changes[i].StateVersion = svOf(1)
	}

	err := s.RecordResourceChanges(t.Context(), "acme", again, f)
	if !errors.Is(err, workspace.ErrInvalidBlame) {
		t.Errorf("second blame of one state version err = %v, want ErrInvalidBlame", err)
	}

	if cursor, err := s.BlameCursor(t.Context(), "ws-one"); err != nil || cursor != 1 {
		t.Errorf("cursor = %d, %v, want 1", cursor, err)
	}

	if b, err := s.ItemChanges(t.Context(), "ws-one", svOf(1)); err != nil || b.Ordinal != 1 || len(b.Changes) != 1 {
		t.Errorf("blame of the state version = %+v, %v, want the first blame", b, err)
	}
}

func testConcurrentRecord(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)

	const writers = 8

	for ordinal := int64(1); ordinal <= 5; ordinal++ {
		var wg sync.WaitGroup

		errs := make([]error, writers)

		for i := range writers {
			wg.Go(func() {
				b := blame("ws-one", ordinal, row("ws-one", ordinal, addrVPC, workspace.ResourceCreated))
				errs[i] = s.RecordResourceChanges(t.Context(), "acme", b, f)
			})
		}

		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Errorf("ordinal %d, writer %d: err = %v, want nil", ordinal, i, err)
			}
		}

		got, err := s.ItemChanges(t.Context(), "ws-one", svOf(ordinal))
		if err != nil || got.Ordinal != ordinal || len(got.Changes) != 1 {
			t.Fatalf("blame of ordinal %d = %+v, %v, want one row at that ordinal", ordinal, got, err)
		}
	}

	if cursor, err := s.BlameCursor(t.Context(), "ws-one"); err != nil || cursor != 5 {
		t.Errorf("cursor = %d, %v, want 5", cursor, err)
	}
}

func testSummaries(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)

	mixed := blame(
		"ws-one",
		1,
		row("ws-one", 1, "a", workspace.ResourceCreated),
		row("ws-one", 1, "b", workspace.ResourceCreated),
		row("ws-one", 1, "c", workspace.ResourceUpdated),
		row("ws-one", 1, "d", workspace.ResourceReplaced),
		row("ws-one", 1, "e", workspace.ResourceDeleted),
	)
	mixed.ResourceCount = 6
	mustRecord(t, s, f, mixed)

	opaque := workspace.ItemBlame{
		Workspace: "ws-one", StateVersion: svOf(2), Changes: nil, Ordinal: 2, ResourceCount: 0,
		Opaque: true, LineageChanged: true,
	}
	mustRecord(t, s, f, opaque)
	mustRecord(t, s, f, blame("ws-two", 1, row("ws-two", 1, "z", workspace.ResourceCreated)))

	got, err := s.ItemSummaries(t.Context(), "ws-one", []workspace.StateVersionID{svOf(1), svOf(2), svOf(3)})
	if err != nil {
		t.Fatalf("item summaries: %v", err)
	}

	want := map[workspace.StateVersionID]workspace.ItemSummary{
		svOf(1): {
			Ordinal:        1,
			ResourceCount:  6,
			Created:        2,
			Updated:        1,
			Replaced:       1,
			Deleted:        1,
			Opaque:         false,
			LineageChanged: false,
		},
		svOf(2): {
			Ordinal:        2,
			ResourceCount:  0,
			Created:        0,
			Updated:        0,
			Replaced:       0,
			Deleted:        0,
			Opaque:         true,
			LineageChanged: true,
		},
	}
	if len(got) != len(want) || got[svOf(1)] != want[svOf(1)] || got[svOf(2)] != want[svOf(2)] {
		t.Errorf("summaries = %+v, want %+v with no entry for a state version without a blame", got, want)
	}

	// The row of ws-two is under the same state version ID, and must not show.
	other, err := s.ItemSummaries(t.Context(), "ws-two", []workspace.StateVersionID{svOf(1), svOf(2)})
	if err != nil || len(other) != 1 || other[svOf(1)].Created != 1 {
		t.Errorf("summaries of ws-two = %+v, %v, want its one item", other, err)
	}

	checkSummaryEdges(t, s, want[svOf(1)])

	none, err := s.ItemSummaries(t.Context(), "ws-one", nil)
	if err != nil || len(none) != 0 {
		t.Errorf("summaries of no state version = %+v, %v, want none", none, err)
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

// checkSummaryEdges checks ItemSummaries for an unknown workspace and for a
// repeated state version. first is the summary of svOf(1) in ws-one.
func checkSummaryEdges(t *testing.T, s Store, first workspace.ItemSummary) {
	t.Helper()

	unknown, err := s.ItemSummaries(t.Context(), "ws-missing", []workspace.StateVersionID{svOf(1)})
	if err != nil || unknown == nil || len(unknown) != 0 {
		t.Errorf("summaries of an unknown workspace = %+v, %v, want an empty map and nil", unknown, err)
	}

	twice, err := s.ItemSummaries(t.Context(), "ws-one", []workspace.StateVersionID{svOf(1), svOf(1)})
	if err != nil || len(twice) != 1 || twice[svOf(1)] != first {
		t.Errorf("summaries with a repeated state version = %+v, %v, want one entry", twice, err)
	}
}
