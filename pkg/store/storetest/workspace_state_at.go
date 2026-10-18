package storetest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// WorkspaceStateAtStore is the workspace ports plus the port under test.
type WorkspaceStateAtStore interface {
	WorkspaceStore
	workspace.StateAtReader
}

// WorkspaceStateAtFactory returns a fresh, empty store for one subtest.
type WorkspaceStateAtFactory func(t *testing.T) WorkspaceStateAtStore

// RunWorkspaceStateAt runs the conformance cases of workspace.StateAtReader
// against stores produced by newStore.
func RunWorkspaceStateAt(t *testing.T, newStore WorkspaceStateAtFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s WorkspaceStateAtStore)
		name string
	}{
		{name: "state at a time follows the finalize time", test: testWorkspaceStateAt},
		{name: "state at a time ignores a pending state version", test: testWorkspaceStateAtPending},
		{name: "state at a time after finalizes that ask for one millisecond", test: testWorkspaceStateAtTie},
		{name: "state at a time after a fence in the current millisecond", test: testWorkspaceStateAtFenceTie},
		{name: "state version list after finalizes in one millisecond", test: testWorkspaceListOrderTie},
		{name: "state at a time of a deleted and of an unknown workspace", test: testWorkspaceStateAtDeleted},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// workspaceStateAtBase is the start of the timeline of the state-at cases.
func workspaceStateAtBase() time.Time {
	return time.Date(2026, time.October, 9, 10, 0, 0, 0, time.UTC)
}

// stateAtWorkspace is a locked workspace of the state-at cases.
type stateAtWorkspace struct {
	ns     namespace.Namespace
	locked workspace.Workspace
}

// finalizeAt declares a state version under the lock of the workspace and
// finalizes it at the time at.
func (w stateAtWorkspace) finalizeAt(
	t *testing.T, s WorkspaceStateAtStore, serial int64, at time.Time,
) workspace.StateVersion {
	t.Helper()

	return w.finalizeCreatedAt(t, s, serial, now(), at)
}

// finalizeCreatedAt is finalizeAt for a state version that the store creates
// at the time created.
func (w stateAtWorkspace) finalizeCreatedAt(
	t *testing.T, s WorkspaceStateAtStore, serial int64, created, at time.Time,
) workspace.StateVersion {
	t.Helper()

	sv := newPendingVersion(t, w.locked, w.locked.Lock.ID, stateInput("lin-1", "v", serial))
	sv.CreatedAt = created

	pending := createPendingVersion(t, s, w.ns, sv)
	done := finalizedVersion(pending, at, "v")

	if err := finalizeRecorded(t, s, w.ns.Fence(), done); err != nil {
		t.Fatalf("finalize state version of serial %d: %v", serial, err)
	}

	return storedStateVersion(t, s, done.ID)
}

// storedStateVersion reads the state version id back from the store, with
// the finalize time that the store stamped.
func storedStateVersion(t *testing.T, s WorkspaceStateAtStore, id workspace.StateVersionID) workspace.StateVersion {
	t.Helper()

	sv, err := s.GetStateVersion(t.Context(), id)
	if err != nil {
		t.Fatalf("get state version %s: %v", id, err)
	}

	return sv
}

// stateAtQuestion is one question of the state-at cases and its answer. With
// the zero want, the answer is ErrNoStateAt, and first is the First of the
// NoStateAtError.
type stateAtQuestion struct {
	at    time.Time
	first time.Time
	want  workspace.StateAt
}

// wantStateAt checks one answer of WorkspaceStateAt for the workspace id.
func wantStateAt(t *testing.T, s WorkspaceStateAtStore, id workspace.ID, q stateAtQuestion) {
	t.Helper()

	at, want, first := q.at, q.want, q.first

	got, err := s.WorkspaceStateAt(t.Context(), id, at)

	if want.StateVersion == "" {
		none, ok := errors.AsType[*workspace.NoStateAtError](err)
		if !ok || !errors.Is(err, workspace.ErrNoStateAt) || !none.First.Equal(first) {
			t.Fatalf("state at %s: err = %v, want ErrNoStateAt with first %s", at, err, first)
		}

		if got != (workspace.StateAt{FinalizedAt: time.Time{}, By: "", StateVersion: ""}) {
			t.Fatalf("state at %s: result = %+v with an error, want the zero value", at, got)
		}

		return
	}

	if err != nil || got.StateVersion != want.StateVersion || got.By != want.By ||
		!got.FinalizedAt.Equal(want.FinalizedAt) {
		t.Fatalf("state at %s = %+v, %v; want %+v", at, got, err, want)
	}
}

// stateAtAnswer is the answer of a state version at its own time.
func stateAtAnswer(sv workspace.StateVersion) workspace.StateAt {
	return workspace.StateAt{FinalizedAt: sv.FinalizedAt, By: "alice", StateVersion: sv.ID}
}

// noStateAt is the zero answer that comes with ErrNoStateAt.
func noStateAt() workspace.StateAt {
	return workspace.StateAt{FinalizedAt: time.Time{}, By: "", StateVersion: ""}
}

// newStateAtWorkspace creates the workspace "acme/network" locked by alice.
func newStateAtWorkspace(t *testing.T, s WorkspaceStateAtStore) (stateAtWorkspace, workspace.Workspace) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	return stateAtWorkspace{ns: ns, locked: lockTestWorkspace(t, s, ns, w, "alice")}, w
}

// testWorkspaceStateAt proves that a time picks the state version with the
// latest finalize time at or before it, with its time and its holder.
func testWorkspaceStateAt(t *testing.T, s WorkspaceStateAtStore) {
	t.Helper()

	w, ws := newStateAtWorkspace(t, s)
	base := workspaceStateAtBase()
	first := w.finalizeAt(t, s, 1, base.Add(time.Minute))
	second := w.finalizeAt(t, s, 2, base.Add(3*time.Minute))

	for _, q := range []stateAtQuestion{
		{at: base, first: first.FinalizedAt, want: noStateAt()},
		{at: first.FinalizedAt.Add(-time.Millisecond), first: first.FinalizedAt, want: noStateAt()},
		{at: first.FinalizedAt, first: time.Time{}, want: stateAtAnswer(first)},
		{at: base.Add(2 * time.Minute), first: time.Time{}, want: stateAtAnswer(first)},
		{at: second.FinalizedAt.Add(-time.Millisecond), first: time.Time{}, want: stateAtAnswer(first)},
		{at: second.FinalizedAt, first: time.Time{}, want: stateAtAnswer(second)},
		{at: base.Add(time.Hour), first: time.Time{}, want: stateAtAnswer(second)},
	} {
		wantStateAt(t, s, ws.ID, q)
	}
}

// testWorkspaceStateAtPending proves that a pending state version does not
// count, neither as the answer nor as the first state version.
func testWorkspaceStateAtPending(t *testing.T, s WorkspaceStateAtStore) {
	t.Helper()

	w, ws := newStateAtWorkspace(t, s)
	late := workspaceStateAtBase().Add(time.Hour)

	createPendingVersion(t, s, w.ns, newPendingVersion(
		t, w.locked, w.locked.Lock.ID, stateInput("lin-1", "v", 1),
	))

	wantStateAt(t, s, ws.ID, stateAtQuestion{at: late, first: time.Time{}, want: noStateAt()})

	done := w.finalizeAt(t, s, 2, workspaceStateAtBase().Add(time.Minute))

	createPendingVersion(t, s, w.ns, newPendingVersion(
		t, w.locked, w.locked.Lock.ID, stateInput("lin-1", "v", 3),
	))

	wantStateAt(t, s, ws.ID, stateAtQuestion{at: late, first: time.Time{}, want: stateAtAnswer(done)})
}

// testWorkspaceStateAtDeleted proves that a soft-deleted workspace answers as
// before its delete, and that an unknown workspace is ErrNotFound.
func testWorkspaceStateAtDeleted(t *testing.T, s WorkspaceStateAtStore) {
	t.Helper()

	w, ws := newStateAtWorkspace(t, s)
	done := w.finalizeAt(t, s, 1, workspaceStateAtBase().Add(time.Minute))

	unlockTestWorkspace(t, s, w.ns, ws.Ref)

	err := s.DeleteWorkspace(t.Context(), ws.Ref, w.ns.Fence(),
		inventory.Deletion{Entry: deleteEntry(w.ns.Name), PurgeAfter: purgeAt()})
	if err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	wantStateAt(t, s, ws.ID, stateAtQuestion{
		at: workspaceStateAtBase().Add(time.Hour), first: time.Time{}, want: stateAtAnswer(done),
	})

	_, err = s.WorkspaceStateAt(t.Context(), "ws-unknown", workspaceStateAtBase())
	if !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("state at of an unknown workspace: err = %v, want ErrNotFound", err)
	}
}

// testWorkspaceStateAtTie proves that a finalize stamps a time after every
// earlier finalize of the workspace, so a read after the latest finalize
// agrees with the current state version: when two finalizes ask for the same
// millisecond, when an earlier-created forced version finalizes last in that
// millisecond, and when a finalize asks for a time before the latest one.
func testWorkspaceStateAtTie(t *testing.T, s WorkspaceStateAtStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	ws := turnOnSwitches(t, s, ns, createTestWorkspace(t, s, ns, "network"),
		namespace.Overrides{Forced: true, Unlocked: false})
	w := stateAtWorkspace{ns: ns, locked: lockTestWorkspace(t, s, ns, ws, "alice")}
	tie := workspaceStateAtBase().Add(time.Minute)
	created := now()

	first := w.finalizeCreatedAt(t, s, 1, created, tie)
	second := w.finalizeCreatedAt(t, s, 2, created.Add(time.Second), tie)

	wantStamped(t, second, tie.Add(time.Millisecond))
	wantStateAt(t, s, ws.ID, stateAtQuestion{at: tie, first: time.Time{}, want: stateAtAnswer(first)})
	wantStateAtNowIsCurrent(t, s, ws.ID, second)

	// The forced version is created before the version that finalizes first
	// in the same millisecond, and it finalizes last.
	tie2 := tie.Add(time.Minute)
	early := createPendingVersion(t, s, ns, newPendingVersion(
		t, w.locked, w.locked.Lock.ID, forcedInput("lin-1", "f", 1),
	))
	late := w.finalizeCreatedAt(t, s, 3, created.Add(2*time.Second), tie2)
	if err := finalizeRecorded(t, s, ns.Fence(), finalizedVersion(early, tie2, "f")); err != nil {
		t.Fatalf("finalize forced state version: %v", err)
	}

	forced := storedStateVersion(t, s, early.ID)

	wantStamped(t, forced, tie2.Add(time.Millisecond))
	wantStateAt(t, s, ws.ID, stateAtQuestion{at: tie2, first: time.Time{}, want: stateAtAnswer(late)})
	wantStateAtNowIsCurrent(t, s, ws.ID, forced)

	// A finalize that asks for a time before the latest one, as after a
	// clock step back, gets one millisecond after the latest one.
	back := w.finalizeCreatedAt(t, s, 4, created.Add(3*time.Second), tie)

	wantStamped(t, back, tie2.Add(2*time.Millisecond))
	wantStateAtNowIsCurrent(t, s, ws.ID, back)
}

// testWorkspaceListOrderTie proves that the state version list follows the
// order in which the versions became current, newest first, when the
// finalizes ask for one millisecond: also for a forced version that is
// created before the others and finalizes last, where an order by version
// ID would differ.
func testWorkspaceListOrderTie(t *testing.T, s WorkspaceStateAtStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	ws := turnOnSwitches(t, s, ns, createTestWorkspace(t, s, ns, "network"),
		namespace.Overrides{Forced: true, Unlocked: false})
	w := stateAtWorkspace{ns: ns, locked: lockTestWorkspace(t, s, ns, ws, "alice")}
	tie := workspaceStateAtBase().Add(time.Minute)
	created := now()

	early := createPendingVersion(t, s, ns, newPendingVersion(
		t, w.locked, w.locked.Lock.ID, forcedInput("lin-1", "f", 1),
	))
	first := w.finalizeCreatedAt(t, s, 1, created, tie)
	second := w.finalizeCreatedAt(t, s, 2, created.Add(time.Second), tie)

	if err := finalizeRecorded(t, s, ns.Fence(), finalizedVersion(early, tie, "f")); err != nil {
		t.Fatalf("finalize forced state version: %v", err)
	}

	forced := storedStateVersion(t, s, early.ID)
	want := []workspace.StateVersionID{forced.ID, second.ID, first.ID}

	list, total, err := s.ListStateVersions(t.Context(), ws.ID, workspace.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("list state versions: %v", err)
	}

	if total != len(want) || len(list) != len(want) {
		t.Fatalf("list holds %d of %d versions, want %d", len(list), total, len(want))
	}

	for i, id := range want {
		if list[i].ID != id {
			t.Fatalf("list[%d] is %s, want %s (the order in which the versions became current, newest first)",
				i, list[i].ID, id)
		}
	}
}

// testWorkspaceStateAtFenceTie proves that FenceAndUnlock also stamps a time
// after every earlier finalize of the workspace, when the fence version asks
// for the millisecond of the current state version.
func testWorkspaceStateAtFenceTie(t *testing.T, s WorkspaceStateAtStore) {
	t.Helper()

	w, ws := newStateAtWorkspace(t, s)
	tie := workspaceStateAtBase().Add(time.Minute)
	v1 := w.finalizeAt(t, s, 1, tie)

	fence := newFenceVersion(t, w.locked, 2, tie)
	stageFence(t, s, w.ns, fence)

	err := s.FenceAndUnlock(t.Context(), releasedWorkspace(w.locked, tie), fence, v1.ID, w.ns.Fence())
	if err != nil {
		t.Fatalf("fence and unlock: %v", err)
	}

	stamped := storedStateVersion(t, s, fence.ID)

	wantStamped(t, stamped, tie.Add(time.Millisecond))
	wantStateAt(t, s, ws.ID, stateAtQuestion{at: tie, first: time.Time{}, want: stateAtAnswer(v1)})
	wantStateAtNowIsCurrent(t, s, ws.ID, stamped)
}

// wantStamped checks that the stored finalize time of sv is want.
func wantStamped(t *testing.T, sv workspace.StateVersion, want time.Time) {
	t.Helper()

	if sv.FinalizedAt.Equal(want) {
		return
	}

	t.Fatalf("finalize time of %s = %s, want %s", sv.ID, sv.FinalizedAt, want)
}

// wantStateAtNowIsCurrent checks that a read after every finalize of the
// state-at cases returns the current state version of the workspace id, and
// that it is want.
func wantStateAtNowIsCurrent(t *testing.T, s WorkspaceStateAtStore, id workspace.ID, want workspace.StateVersion) {
	t.Helper()

	cur, err := s.CurrentStateVersion(t.Context(), id)
	if err != nil {
		t.Fatalf("current state version: %v", err)
	}

	if cur.ID != want.ID {
		t.Fatalf("current state version = %s, want %s", cur.ID, want.ID)
	}

	got, err := s.WorkspaceStateAt(t.Context(), id, workspaceStateAtBase().Add(24*time.Hour))
	if err != nil || got.StateVersion != cur.ID || !got.FinalizedAt.Equal(cur.FinalizedAt) {
		t.Fatalf("state at the end of the timeline = %+v, %v; want the current state version %s finalized at %s",
			got, err, cur.ID, cur.FinalizedAt)
	}
}
