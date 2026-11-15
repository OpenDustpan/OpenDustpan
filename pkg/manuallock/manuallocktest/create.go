package manuallocktest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// newWorkspaceValue returns the workspace called name in ns, with no store
// write.
func newWorkspaceValue(t *testing.T, ns namespace.Namespace, name string) workspace.Workspace {
	t.Helper()

	return workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   now(),
		UpdatedAt:   now(),
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          workspace.ID(ident.NewID("ws-")),
		Ref:         workspace.Ref{Namespace: ns.Name, Name: mustWorkspaceName(t, name)},
		Revision:    1,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
}

// testCreateUnderNamespaceLock proves that the create of a workspace is refused,
// in the transaction of the create, while the namespace has a manual lock. The
// refusal leaves no workspace and no inventory entry, matches both lock
// sentinels, and names the lock. The create works again after the lock ends.
func testCreateUnderNamespaceLock(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	f.lock(t, manuallock.NamespaceTarget(f.ns.Name), alice, "freeze")

	w := newWorkspaceValue(t, f.ns, "fresh")

	err := s.CreateWorkspace(t.Context(), w, f.ns.Fence())
	if !errors.Is(err, workspace.ErrLocked) || !errors.Is(err, manuallock.ErrLocked) {
		t.Fatalf("create under a namespace lock = %v, want workspace.ErrLocked and manuallock.ErrLocked", err)
	}

	var locked *manuallock.LockedError
	if !errors.As(err, &locked) || locked.Lock.Holder != alice || locked.Lock.Reason != "freeze" {
		t.Fatalf("create under a namespace lock = %v, want the lock of %q for %q", err, alice, "freeze")
	}

	want := collection.UnitRef{Namespace: f.ns.Name, Tool: workspace.InventoryTool, Name: w.Ref.Name.String()}
	if locked.Unit != want {
		t.Fatalf("refused unit = %v, want %v", locked.Unit, want)
	}

	if _, getErr := s.GetWorkspace(t.Context(), w.Ref); !errors.Is(getErr, workspace.ErrNotFound) {
		t.Fatalf("GetWorkspace after the refused create = %v, want workspace.ErrNotFound", getErr)
	}

	f.forceRemove(t, manuallock.NamespaceTarget(f.ns.Name))

	if err := s.CreateWorkspace(t.Context(), w, f.ns.Fence()); err != nil {
		t.Fatalf("create after the lock ends: %v", err)
	}
}

// testCreatePhasePass proves the rule of the job for the create under a
// namespace lock. The context carries the start of the phase of the job: a
// phase that started before the lock passes it, and the create works. A phase
// at or after the lock, and the zero time of a request with no job, meet it.
func testCreatePhasePass(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	f.lock(t, manuallock.NamespaceTarget(f.ns.Name), alice, "freeze")

	lockedAt := now()

	cases := []struct {
		name    string
		started time.Time
		want    bool
	}{
		{"phase that started after the lock", lockedAt.Add(time.Hour), false},
		{"phase that started at the time of the lock", lockedAt, false},
		{"request with no job", time.Time{}, false},
		{"phase that started before the lock", lockedAt.Add(-time.Hour), true},
	}

	for i, tc := range cases {
		w := newWorkspaceValue(t, f.ns, "job-"+string(rune('a'+i)))
		ctx := workspace.WithLockPass(t.Context(), tc.started)

		err := s.CreateWorkspace(ctx, w, f.ns.Fence())
		if tc.want {
			if err != nil {
				t.Fatalf("%s: create = %v, want it to pass the lock", tc.name, err)
			}

			continue
		}

		if !errors.Is(err, workspace.ErrLocked) {
			t.Fatalf("%s: create = %v, want workspace.ErrLocked", tc.name, err)
		}

		if _, getErr := s.GetWorkspace(t.Context(), w.Ref); !errors.Is(getErr, workspace.ErrNotFound) {
			t.Fatalf("%s: GetWorkspace after the refused create = %v, want ErrNotFound", tc.name, getErr)
		}
	}
}

// testCreateIgnoresOtherLocks proves that the manual lock of a collection or of
// another unit does not refuse the create of a new workspace: a new unit is in
// no collection yet, and only the lock of the namespace covers it.
func testCreateIgnoresOtherLocks(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	f.lock(t, manuallock.CollectionTarget(f.payments.Ref), alice, "collection")
	f.lock(t, manuallock.UnitTarget(f.web), alice, "unit")

	if err := s.CreateWorkspace(t.Context(), newWorkspaceValue(t, f.ns, "fresh"), f.ns.Fence()); err != nil {
		t.Fatalf("create with a lock on a collection and on another unit: %v", err)
	}
}
