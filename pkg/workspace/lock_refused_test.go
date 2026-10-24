package workspace_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestLockRefusalsNameTheLock proves that each refusal of a call because of
// the current lock names that lock, and still matches its sentinel: a second
// lock, an unlock by another holder, and an unlock that names another lock
// ID.
func TestLockRefusalsNameTheLock(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	held, err := f.svc.LockWithID(ctx, f.ws.ID, "alice", "apply", "client-lock-1")
	if err != nil {
		t.Fatalf("lock: %v", err)
	}

	_, lockErr := f.svc.Lock(ctx, f.ws.ID, "bob", "plan")
	_, otherErr := f.svc.Unlock(ctx, f.ws.ID, "bob")
	_, mismatchErr := f.svc.UnlockLock(ctx, f.ws.ID, "client-lock-2", "alice")

	cases := []struct {
		name     string
		err      error
		sentinel error
	}{
		{"second lock", lockErr, workspace.ErrLocked},
		{"unlock by another holder", otherErr, workspace.ErrLockedByOther},
		{"unlock of another lock ID", mismatchErr, workspace.ErrLockMismatch},
	}

	for _, tc := range cases {
		if !errors.Is(tc.err, tc.sentinel) {
			t.Errorf("%s: %v, want %v", tc.name, tc.err, tc.sentinel)

			continue
		}

		got, ok := workspace.RefusingLock(tc.err)
		if !ok || got.ID != held.Lock.ID || got.Holder != "alice" {
			t.Errorf("%s: refusing lock = %+v %t, want the lock of alice %q", tc.name, got, ok, held.Lock.ID)
		}
	}
}

// TestRefusingLockOfOtherErrors proves that an error without a lock refusal
// names no lock.
func TestRefusingLockOfOtherErrors(t *testing.T) {
	t.Parallel()

	for _, err := range []error{nil, workspace.ErrLocked, workspace.ErrConcurrentModification} {
		if got, ok := workspace.RefusingLock(err); ok || got.Locked() {
			t.Errorf("RefusingLock(%v) = %+v %t, want no lock", err, got, ok)
		}
	}
}
