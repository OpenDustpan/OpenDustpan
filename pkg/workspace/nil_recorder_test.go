package workspace_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestOverrideNeedsARecorder proves that each write path that uses an override
// refuses a nil record callback with ErrNoRecorder before it changes anything,
// as if the adapter had passed no callback. The three paths are the declare
// (forced and unlocked), the streamed write and the unfenced release.
func TestOverrideNeedsARecorder(t *testing.T) {
	t.Parallel()

	unlockedOnly := namespace.Overrides{Unlocked: true, Forced: false}

	tests := []struct {
		name string
		run  func(t *testing.T, f fixture) error
		// versions is the number of state versions that must remain.
		versions int
		// locked keeps the lock of alice while the write runs.
		locked bool
	}{
		{
			name: "declare with the forced override",
			run: func(t *testing.T, f fixture) error {
				t.Helper()

				forced := newState("lin-old", 2, smallDoc)
				forced.Force = true
				enableForced(t, f)

				_, err := f.svc.BeginState(t.Context(), f.ws.ID, "alice", forced, authority(forcedOnly(), nil))

				return err
			},
			versions: 1,
			locked:   true,
		},
		{
			name: "declare with the unlocked override",
			run: func(t *testing.T, f fixture) error {
				t.Helper()

				_, err := f.svc.BeginState(
					t.Context(), f.ws.ID, "alice", newState("lin-old", 2, smallDoc), authority(unlockedOnly, nil),
				)

				return err
			},
			versions: 1,
		},
		{
			name: "streamed write with the unlocked override",
			run: func(t *testing.T, f fixture) error {
				t.Helper()

				_, err := f.svc.WriteState(
					t.Context(),
					f.ws.ID,
					workspace.StreamInput{Holder: "alice", LockID: "", MD5: ""},
					streamAuthority(unlockedOnly, nil),
					strings.NewReader(streamDoc("lin-old", 2)),
				)

				return err
			},
			versions: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			if _, err := f.svc.Lock(t.Context(), f.ws.ID, "alice", "apply"); err != nil {
				t.Fatalf("lock: %v", err)
			}

			if err := f.push(t, newState("lin-old", 1, smallDoc), smallDoc); err != nil {
				t.Fatalf("first state: %v", err)
			}

			if !tt.locked {
				if _, err := f.svc.Unlock(t.Context(), f.ws.ID, "alice"); err != nil {
					t.Fatalf("unlock: %v", err)
				}
			}

			err := tt.run(t, f)
			if !errors.Is(err, workspace.ErrNoRecorder) {
				t.Fatalf("err = %v, want ErrNoRecorder", err)
			}

			checkHistoryTotal(t, f, tt.versions)
		})
	}
}

// TestForceUnlockNeedsARecorder proves that an unfenced release under the
// forced-write override refuses a nil record callback with ErrNoRecorder, keeps
// the lock, and writes no entry.
func TestForceUnlockNeedsARecorder(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	lockAndPush(t, f, `{"encrypted_data":"c2VjcmV0"}`)
	enableForced(t, f)

	_, _, err := f.svc.ForceUnlock(t.Context(), f.ws.ID, "admin", forcedOnly(), nil, forceEntry())
	if !errors.Is(err, workspace.ErrNoRecorder) {
		t.Fatalf("force unlock: err = %v, want ErrNoRecorder", err)
	}

	got, err := f.svc.GetByID(t.Context(), f.ws.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if !got.Lock.Locked() {
		t.Error("the lock was released, want it held")
	}

	if entries := f.store.releaseEntries(); len(entries) != 0 {
		t.Errorf("entries = %+v, want none", entries)
	}
}
