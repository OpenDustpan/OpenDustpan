package sqlite_test

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestStateWriteSetsCurrentVersion proves that each state write of each tool
// sets the current state version of the unit: a Pulumi checkpoint and an
// OpenTofu finalize each make the written version current, before any final
// write.
func TestStateWriteSetsCurrentVersion(t *testing.T) {
	t.Parallel()

	t.Run("Pulumi checkpoint", func(t *testing.T) {
		t.Parallel()

		s := open(t)

		fence, unit := unitStatePulumiUnit(t, s)

		if got := unitStateVersion(t, s, fence.Namespace, unit.UnitID()); got != "" {
			t.Fatalf("state version before the checkpoint = %q, want none", got)
		}

		want := unitStatePulumiCheckpoint(t, s, fence, unit)

		if got := unitStateVersion(t, s, fence.Namespace, unit.UnitID()); got != want {
			t.Fatalf("state version after the checkpoint = %q, want %q", got, want)
		}
	})

	t.Run("OpenTofu finalize", func(t *testing.T) {
		t.Parallel()

		s := open(t)

		fence, unit := unitStateTofuUnit(t, s)

		if got := unitStateVersion(t, s, fence.Namespace, unit.UnitID()); got != "" {
			t.Fatalf("state version before the finalize = %q, want none", got)
		}

		want := unitStateTofuFinalize(t, s, fence, unit)

		if got := unitStateVersion(t, s, fence.Namespace, unit.UnitID()); got != want {
			t.Fatalf("state version after the finalize = %q, want %q", got, want)
		}
	})
}

// TestFinalWriteClosesAwaitingJob proves that a final state write closes the
// awaiting write-back job of the unit in the same commit: after the finalize,
// the job is succeeded and closed by the finalized version.
func TestFinalWriteClosesAwaitingJob(t *testing.T) {
	t.Parallel()

	s := open(t)

	fence, unit := stateWriteUnit(t, s)
	delivered := awaitingWriteBackJob(t, s, fence, unit)
	assertWriteBackWaiting(t, s, delivered.ID)

	w := unlockedForWrites(t, s, fence, unit)
	sv := pendingStateWrite(t, w, "")

	if err := s.CreateStateVersion(t.Context(), sv, fence, stateWriteCap); err != nil {
		t.Fatalf("CreateStateVersion = %v", err)
	}

	finalizeStateWrite(t, s, sv, fence)

	if got := unitStateVersion(t, s, fence.Namespace, unit.UnitID()); got != sv.ID.String() {
		t.Fatalf("state version after the final write = %q, want %q", got, sv.ID)
	}

	assertWriteBackClosed(t, s, delivered.ID, sv.ID)
}

// TestDuplicateFinalIdempotent proves that a duplicate final write changes
// nothing: the second finalize of the same version is refused, and the current
// and final versions stay as the first write left them.
func TestDuplicateFinalIdempotent(t *testing.T) {
	t.Parallel()

	s := open(t)

	fence, unit := stateWriteUnit(t, s)
	delivered := awaitingWriteBackJob(t, s, fence, unit)

	w := unlockedForWrites(t, s, fence, unit)
	sv := pendingStateWrite(t, w, "")

	if err := s.CreateStateVersion(t.Context(), sv, fence, stateWriteCap); err != nil {
		t.Fatalf("CreateStateVersion = %v", err)
	}

	final := finalizeStateWrite(t, s, sv, fence)

	if err := s.FinalizeStateVersion(t.Context(), final, fence); !errors.Is(err, workspace.ErrUploadDenied) {
		t.Fatalf("second FinalizeStateVersion = %v, want %v", err, workspace.ErrUploadDenied)
	}

	if got := unitStateVersion(t, s, fence.Namespace, unit.UnitID()); got != sv.ID.String() {
		t.Fatalf("state version after the duplicate finalize = %q, want %q", got, sv.ID)
	}

	got, ok, err := s.Jobs().FinalVersion(t.Context(), fence.Namespace, unit.UnitID())
	if err != nil || !ok {
		t.Fatalf("final version after the duplicate finalize = %+v, %v, %v; want one", got, ok, err)
	}

	if got.VersionID != sv.ID.String() {
		t.Fatalf("final version after the duplicate finalize = %q, want %q", got.VersionID, sv.ID)
	}

	assertWriteBackClosed(t, s, delivered.ID, sv.ID)
}

// TestFinalWriteRollback proves that a version write that fails inside the
// transaction rolls back the state write: the lock release of a fence version
// whose recorded content is gone finalizes no row, so the lock, the current
// version and the awaiting job stay as they were.
func TestFinalWriteRollback(t *testing.T) {
	t.Parallel()

	s, path := openUnitStateStoreWithLogger(t, slog.New(new(unitStateLogHandler)))
	ctx := t.Context()

	fence, unit := stateWriteUnit(t, s)
	w := unitStateTofuLock(t, s, fence, unit)
	delivered := awaitingWriteBackJob(t, s, fence, unit)

	fenceVersion := pendingStateWrite(t, w, string(delivered.ID))
	fenceVersion.Holder = w.Lock.Holder
	fenceVersion.LockID = w.Lock.ID
	fenceVersion.UnlockedWrite = false

	if err := s.CreateStateVersion(ctx, fenceVersion, fence, stateWriteCap); err != nil {
		t.Fatalf("CreateStateVersion = %v", err)
	}

	tamperStateVersionContent(t, path, fenceVersion.ID.String())

	at := time.UnixMilli(unitStateWriteAtMs + 2000).UTC()
	lock := w.Lock

	released := w
	released.Lock = workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}
	released.LastRelease = workspace.Release{
		At: at, LockID: lock.ID, Holder: lock.Holder, By: writeStateHolder, Kind: workspace.ReleaseNormal,
	}
	released.Revision++
	released.UpdatedAt = at

	releaseErr := s.FenceAndUnlock(ctx, released, fenceVersion, "", fence, stateWriteEntry(unit))

	if !errors.Is(releaseErr, workspace.ErrUploadDenied) {
		t.Fatalf("FenceAndUnlock with a missing version write = %v, want %v",
			releaseErr, workspace.ErrUploadDenied)
	}

	stored, err := s.GetWorkspaceByID(ctx, workspace.ID(unit.UnitID()))
	if err != nil {
		t.Fatalf("GetWorkspaceByID = %v", err)
	}

	if stored.Lock.Holder != writeStateHolder || stored.Revision != w.Revision {
		t.Fatalf("workspace after the rolled back release = lock %q, revision %d; want the lock held, revision %d",
			stored.Lock.Holder, stored.Revision, w.Revision)
	}

	if got := unitStateVersion(t, s, fence.Namespace, unit.UnitID()); got != "" {
		t.Fatalf("state version after the rolled back release = %q, want none", got)
	}

	assertFinalizeStillPending(t, s, fenceVersion)
	assertWriteBackWaiting(t, s, delivered.ID)
}
