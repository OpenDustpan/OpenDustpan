package workspace_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestLockWithIDKeepsTheCallerID proves a lock taken with LockWithID keeps the
// ID the caller chose, so a client that names its lock in each write finds it
// (ADR 49, decision 6). It refuses an ID outside the lock ID set and a lock
// taken while another holds one.
func TestLockWithIDKeepsTheCallerID(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	locked, err := f.svc.LockWithID(ctx, f.ws.ID, "alice", "apply", "client-lock-1")
	if err != nil {
		t.Fatalf("lock with id: %v", err)
	}

	if locked.Lock.ID != "client-lock-1" {
		t.Fatalf("lock ID = %q, want the caller's ID", locked.Lock.ID)
	}

	if _, err := f.svc.LockWithID(ctx, f.ws.ID, "bob", "plan", "client-lock-2"); !errors.Is(err, workspace.ErrLocked) {
		t.Fatalf("second lock with id: %v, want ErrLocked", err)
	}

	if _, _, err := f.svc.ForceUnlock(ctx, f.ws.ID, "admin", noOverrides(), nil, forceEntry()); err != nil {
		t.Fatalf("force unlock: %v", err)
	}
}

// TestLockWithIDRefusesBadIDs proves an ID that is empty, too long or outside
// the lock ID set is refused before the workspace changes.
func TestLockWithIDRefusesBadIDs(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	for _, id := range []string{"", "has space", "slash/path", "semi;colon", strings.Repeat("a", 129)} {
		if _, err := f.svc.LockWithID(ctx, f.ws.ID, "alice", "apply", id); !errors.Is(err, workspace.ErrInvalidLockID) {
			t.Errorf("lock with id %q: %v, want ErrInvalidLockID", id, err)
		}
	}

	if _, err := f.svc.Unlock(ctx, f.ws.ID, "alice"); !errors.Is(err, workspace.ErrNotLocked) {
		t.Fatalf("workspace after refused IDs: unlock = %v, want ErrNotLocked (still unlocked)", err)
	}
}
