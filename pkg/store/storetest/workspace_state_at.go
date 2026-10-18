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

	pending := createPendingVersion(t, s, w.ns, newPendingVersion(
		t, w.locked, w.locked.Lock.ID, stateInput("lin-1", "v", serial),
	))
	done := finalizedVersion(pending, at, "v")

	if err := finalizeRecorded(t, s, w.ns.Fence(), done); err != nil {
		t.Fatalf("finalize state version of serial %d: %v", serial, err)
	}

	return done
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
