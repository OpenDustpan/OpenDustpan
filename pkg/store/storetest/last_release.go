package storetest

import (
	"reflect"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// lastReleaseCases returns the cases of the record of the last released lock
// of a workspace: each write of a workspace stores the record, and each read
// gives it back.
func lastReleaseCases() []workspaceCase {
	return []workspaceCase{
		{name: "the record of the last release round-trips through each read", test: testLastReleaseRoundTrip},
		{name: "each release write stores the record of the release", test: testLastReleaseReleaseWrites},
		{name: "the inventory entry shows the last release of the workspace", test: testLastReleaseInventory},
	}
}

// releaseRecords returns one record of each kind, and no record, with the
// times of the suite.
func releaseRecords() []struct {
	name   string
	record workspace.Release
} {
	return []struct {
		name   string
		record workspace.Release
	}{
		{name: "no record", record: noRelease()},
		{
			name: "normal release",
			record: workspace.Release{
				At: now().Add(2 * time.Hour), LockID: "lock-a", Holder: "alice", By: "alice",
				Kind: workspace.ReleaseNormal,
			},
		},
		{
			name: "forced release",
			record: workspace.Release{
				At: now().Add(3 * time.Hour), LockID: "lock-b", Holder: "alice", By: "admin",
				Kind: workspace.ReleaseForced,
			},
		},
	}
}

// noRelease returns the zero Release: no record.
func noRelease() workspace.Release {
	return workspace.Release{At: time.Time{}, LockID: "", Holder: "", By: "", Kind: ""}
}

// testLastReleaseRoundTrip saves a workspace with each kind of record, and
// expects each read of the port to give the record back.
func testLastReleaseRoundTrip(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")

	for i, tc := range releaseRecords() {
		w := createTestWorkspace(t, s, ns, "unit-"+strconv.Itoa(i))
		saved := w
		saved.LastRelease = tc.record
		saved.UpdatedAt = now().Add(4 * time.Hour)
		saved.Revision++

		if err := s.SaveWorkspace(t.Context(), saved, ns.Fence()); err != nil {
			t.Fatalf("%s: save workspace: %v", tc.name, err)
		}

		assertReadsBack(t, s, saved)
	}
}

// assertReadsBack checks that the read by name, the read by ID, the list and
// the page of the namespace each give want, with its record of the release.
func assertReadsBack(t *testing.T, s WorkspaceStore, want workspace.Workspace) {
	t.Helper()

	byName, err := s.GetWorkspace(t.Context(), want.Ref)
	if err != nil {
		t.Fatalf("get workspace %s: %v", want.Ref, err)
	}

	assertWorkspace(t, byName, want)

	byID, err := s.GetWorkspaceByID(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("get workspace %s by ID: %v", want.ID, err)
	}

	assertWorkspace(t, byID, want)

	listed, err := s.ListWorkspaces(t.Context(), want.Ref.Namespace)
	if err != nil {
		t.Fatalf("list workspaces: %v", err)
	}

	assertListed(t, listed, want)

	page, err := s.QueryWorkspaces(t.Context(), workspace.ListQuery{
		Namespace: want.Ref.Namespace, NameContains: want.Ref.Name.String(), Offset: 0, Limit: 0,
	})
	if err != nil {
		t.Fatalf("query workspaces: %v", err)
	}

	assertListed(t, page.Workspaces, want)
}

// assertListed checks that list holds want.
func assertListed(t *testing.T, list []workspace.Workspace, want workspace.Workspace) {
	t.Helper()

	for i := range list {
		if list[i].ID == want.ID {
			assertWorkspace(t, list[i], want)

			return
		}
	}

	t.Fatalf("list %+v has no workspace %s", list, want.ID)
}

// testLastReleaseReleaseWrites releases a lock through each release write of
// the port, SaveReleased, ReleaseUnfenced and FenceAndUnlock, with a forced
// record, and expects the reads to give the record back.
func testLastReleaseReleaseWrites(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	at := now().Add(2 * time.Hour)

	withoutState := lockTestWorkspace(t, s, ns, createTestWorkspace(t, s, ns, "plain"), "alice")
	saved := forcedRelease(withoutState, at)

	if err := s.SaveReleased(t.Context(), saved, ns.Fence(), releaseEntry(ns.Name)); err != nil {
		t.Fatalf("save released: %v", err)
	}

	assertReadsBack(t, s, saved)

	locked := lockTestWorkspace(t, s, ns, createTestWorkspace(t, s, ns, "unfenced"), "alice")
	on := turnOnSwitches(t, s, ns, locked, namespace.Overrides{Forced: true, Unlocked: false})
	unfenced := forcedRelease(on, at)

	if err := s.ReleaseUnfenced(t.Context(), unfenced, ns.Fence(), releaseEntry(ns.Name)); err != nil {
		t.Fatalf("release unfenced: %v", err)
	}

	assertReadsBack(t, s, unfenced)

	withState, _ := lockedWithState(t, s, ns)
	fenced := forcedRelease(withState, at)
	fence := newFenceVersion(t, withState, 2, at)
	stageFence(t, s, ns, fence)

	err := s.FenceAndUnlock(
		t.Context(), fenced, fence, currentVersionID(t, s, withState.ID), ns.Fence(), releaseEntry(ns.Name),
	)
	if err != nil {
		t.Fatalf("fence and unlock: %v", err)
	}

	assertReadsBack(t, s, fenced)
}

// forcedRelease returns locked as a force-unlock by "admin" at the time at saves it:
// the lock cleared, the revision one on, and the record of a forced release.
func forcedRelease(locked workspace.Workspace, at time.Time) workspace.Workspace {
	released := releasedWorkspace(locked, at)
	released.LastRelease = workspace.Release{
		At: at, LockID: locked.Lock.ID, Holder: locked.Lock.Holder, By: "admin", Kind: workspace.ReleaseForced,
	}

	return released
}

// inventoryLastRelease returns the last release on the inventory entry of the
// OpenTofu workspace name in ns, and from both reads of the port: the list and
// the page. It fails the test when the two reads differ.
func inventoryLastRelease(t *testing.T, s WorkspaceStore, ns namespace.Name, name string) *inventory.LastRelease {
	t.Helper()

	var listed *inventory.Entry

	entries, err := s.ListUnits(t.Context(), ns)
	if err != nil {
		t.Fatalf("list inventory of %q: %v", ns, err)
	}

	for i := range entries {
		if entries[i].Tool == workspace.InventoryTool && entries[i].Name == name {
			listed = &entries[i]
		}
	}

	if listed == nil {
		t.Fatalf("inventory has no opentofu entry %q in %q", name, ns)
	}

	byID, err := s.GetUnit(t.Context(), listed.UnitID)
	if err != nil {
		t.Fatalf("get unit %q: %v", listed.UnitID, err)
	}

	if !reflect.DeepEqual(byID.LastRelease, listed.LastRelease) {
		t.Fatalf("last release by unit ID = %+v, in the list = %+v", byID.LastRelease, listed.LastRelease)
	}

	return listed.LastRelease
}

// testLastReleaseInventory expects the inventory entry of a workspace to show
// the last release in the same transaction as the write of the workspace. The
// steps run in order on one workspace: a workspace that never released a lock,
// a normal release, a repeated read of the same release, and a forced release
// that replaces the record.
func testLastReleaseInventory(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "unit")
	name := w.Ref.Name.String()
	normalAt := now().Add(2 * time.Hour)
	forcedAt := now().Add(5 * time.Hour)

	normal := inventory.LastRelease{At: normalAt, LockID: "lock-alice", Holder: "alice", By: "alice", Kind: "normal"}
	forced := inventory.LastRelease{At: forcedAt, LockID: "lock-alice", Holder: "alice", By: "admin", Kind: "forced"}

	steps := []struct {
		act  func(t *testing.T) workspace.Workspace
		want *inventory.LastRelease
		name string
	}{
		{
			name: "a lock never released has no record",
			act:  func(*testing.T) workspace.Workspace { return w },
			want: nil,
		},
		{
			name: "a normal release shows its record",
			act: func(t *testing.T) workspace.Workspace {
				t.Helper()

				locked := lockTestWorkspace(t, s, ns, w, "alice")
				released := releasedWorkspace(locked, normalAt)
				released.LastRelease = workspace.Release{
					At: normalAt, LockID: "lock-alice", Holder: "alice", By: "alice", Kind: workspace.ReleaseNormal,
				}

				if err := s.SaveReleased(t.Context(), released, ns.Fence(), releaseEntry(ns.Name)); err != nil {
					t.Fatalf("save released: %v", err)
				}

				return released
			},
			want: &normal,
		},
		{
			name: "a repeated read shows the same record",
			act:  func(*testing.T) workspace.Workspace { return workspace.Workspace{} },
			want: &normal,
		},
		{
			name: "a forced release replaces the record",
			act: func(t *testing.T) workspace.Workspace {
				t.Helper()

				current, err := s.GetWorkspace(t.Context(), w.Ref)
				if err != nil {
					t.Fatalf("get workspace: %v", err)
				}

				locked := lockTestWorkspace(t, s, ns, current, "alice")
				released := forcedRelease(locked, forcedAt)

				if err := s.SaveReleased(t.Context(), released, ns.Fence(), releaseEntry(ns.Name)); err != nil {
					t.Fatalf("save released: %v", err)
				}

				return released
			},
			want: &forced,
		},
	}

	for _, step := range steps {
		step.act(t)

		got := inventoryLastRelease(t, s, ns.Name, name)
		if !reflect.DeepEqual(got, step.want) {
			t.Fatalf("%s: last release = %+v, want %+v", step.name, got, step.want)
		}
	}
}
