package workspace_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// noRecorderCase is one write that uses an override and gives no record
// callback.
type noRecorderCase struct {
	name string
	// locked keeps the lock of alice while the write runs.
	locked bool
	run    func(t *testing.T, f fixture) error
}

// noRecorderCases returns the declare (forced and unlocked) and the streamed
// write. Each one uses an override with a nil callback.
func noRecorderCases() []noRecorderCase {
	unlockedOnly := namespace.Overrides{Unlocked: true, Forced: false}

	return []noRecorderCase{
		{
			name:   "declare with the forced override",
			locked: true,
			run: func(t *testing.T, f fixture) error {
				t.Helper()

				forced := newState("lin-old", 2, smallDoc)
				forced.Force = true

				_, err := f.svc.BeginState(t.Context(), f.ws.ID, "alice", forced, authority(forcedOnly(), nil))

				return err
			},
		},
		{
			name:   "declare with the unlocked override",
			locked: false,
			run: func(t *testing.T, f fixture) error {
				t.Helper()

				_, err := f.svc.BeginState(
					t.Context(), f.ws.ID, "alice", newState("lin-old", 2, smallDoc), authority(unlockedOnly, nil),
				)

				return err
			},
		},
		{
			name:   "streamed write with the unlocked override",
			locked: false,
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
		},
	}
}

// overrideFixture returns a workspace with one state version and both switches
// on, so that the store alone would let an override through. Alice holds the
// lock.
func overrideFixture(t *testing.T) fixture {
	t.Helper()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if err := f.push(t, newState("lin-old", 1, smallDoc), smallDoc); err != nil {
		t.Fatalf("first state: %v", err)
	}

	allow := namespace.Overrides{Unlocked: true, Forced: true}
	if _, err := f.namespaces.SetOverrides(ctx, f.ws.Ref.Namespace, allow); err != nil {
		t.Fatalf("set namespace switches: %v", err)
	}

	if _, err := f.svc.SetOverrides(ctx, f.ws.ID, allow); err != nil {
		t.Fatalf("set unit switches: %v", err)
	}

	return f
}

// TestOverrideNeedsARecorder proves that each write path that uses an override
// refuses a nil record callback with ErrNoRecorder before it changes anything,
// as if the adapter had passed no callback. The paths are the declare (forced
// and unlocked) and the streamed write. TestForceUnlockNeedsARecorder covers
// the unfenced release.
func TestOverrideNeedsARecorder(t *testing.T) {
	t.Parallel()

	for _, tt := range noRecorderCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := overrideFixture(t)

			if !tt.locked {
				if _, err := f.svc.Unlock(t.Context(), f.ws.ID, "alice"); err != nil {
					t.Fatalf("unlock: %v", err)
				}
			}

			if err := tt.run(t, f); !errors.Is(err, workspace.ErrNoRecorder) {
				t.Fatalf("err = %v, want ErrNoRecorder", err)
			}

			checkHistoryTotal(t, f, 1)
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
