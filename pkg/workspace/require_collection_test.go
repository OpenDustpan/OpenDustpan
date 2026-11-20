package workspace_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// putInCollection makes the store report the workspace of f as a member of a
// collection.
func putInCollection(f fixture) {
	f.store.mu.Lock()
	defer f.store.mu.Unlock()

	f.store.placements[f.ws.ID] = memPlacement("acme")
}

// removeFromCollection makes the store report the workspace of f as a member
// of no collection.
func removeFromCollection(f fixture) {
	f.store.mu.Lock()
	defer f.store.mu.Unlock()

	delete(f.store.placements, f.ws.ID)
}

// TestLockForJobInCollection proves the lock of a workspace that a collection
// must hold. A workspace in no collection takes no lock and the refusal writes
// nothing. The lock is the only write that the case reaches, so removing the
// membership check makes the first case fail.
func TestLockForJobInCollection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		member   bool
		holder   string
		wantErr  error
		wantHeld bool
	}{
		{name: "no collection", member: false, holder: "alice", wantErr: collection.ErrNoCollection, wantHeld: false},
		{
			name:     "no collection and no holder",
			member:   false,
			holder:   "",
			wantErr:  workspace.ErrNotLocked,
			wantHeld: false,
		},
		{name: "in a collection", member: true, holder: "alice", wantErr: nil, wantHeld: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			if tc.member {
				putInCollection(f)
			} else {
				removeFromCollection(f)
			}

			_, err := f.svc.LockForJobInCollection(t.Context(), f.ws.ID, tc.holder, "plan", "")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}

			got, getErr := f.svc.GetByID(t.Context(), f.ws.ID)
			if getErr != nil {
				t.Fatalf("get: %v", getErr)
			}

			if got.Lock.Locked() != tc.wantHeld {
				t.Errorf("locked = %v, want %v", got.Lock.Locked(), tc.wantHeld)
			}
		})
	}
}

// TestBeginStateRequireCollection proves a declare that requires a collection
// writes no state version for a workspace in no collection, and that a declare
// that does not require one is unchanged.
func TestBeginStateRequireCollection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		member  bool
		require bool
		wantErr error
	}{
		{name: "required, no collection", member: false, require: true, wantErr: collection.ErrNoCollection},
		{name: "required, in a collection", member: true, require: true, wantErr: nil},
		{name: "not required, no collection", member: false, require: false, wantErr: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := lockedFixture(t)
			if tc.member {
				putInCollection(f)
			} else {
				removeFromCollection(f)
			}

			in := newState("l1", 1, "{\"v\":1}")
			begin := f.svc.BeginState
			if tc.require {
				begin = f.svc.BeginStateInCollection
			}

			_, err := begin(t.Context(), f.ws.ID, "alice", in, authority(forcedOnly(), nil))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}

			if tc.wantErr != nil {
				requireNoPending(t, f)
			}
		})
	}
}

// requireNoPending fails the test when the workspace of f holds a pending
// state version.
func requireNoPending(t *testing.T, f fixture) {
	t.Helper()

	if n, err := f.store.CountPendingStateVersions(t.Context(), f.ws.ID); err != nil || n != 0 {
		t.Errorf("pending versions = %d, %v, want 0", n, err)
	}
}
