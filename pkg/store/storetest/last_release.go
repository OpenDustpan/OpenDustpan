package storetest

import (
	"strconv"
	"testing"
	"time"

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
