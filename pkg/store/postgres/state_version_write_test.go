package postgres_test

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/store/postgres"
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

		s := openUnitStateStore(t)

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

		s := openUnitStateStore(t)

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

	s := openUnitStateStore(t)

	fence, unit := unitStateTofuUnit(t, s)
	delivered := awaitingWriteBackJob(t, s, fence, unit)
	assertWriteBackWaiting(t, s, delivered.ID)

	w := unlockedForWrites(t, s, fence, unit)
	sv := unitStatePending(t, w)

	if err := s.CreateStateVersion(t.Context(), sv, fence, unitStatePendingCap); err != nil {
		t.Fatalf("CreateStateVersion = %v", err)
	}

	unitStateFinalize(t, s, sv, fence, "")

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

	s := openUnitStateStore(t)

	fence, unit := unitStateTofuUnit(t, s)
	delivered := awaitingWriteBackJob(t, s, fence, unit)

	w := unlockedForWrites(t, s, fence, unit)
	sv := unitStatePending(t, w)

	if err := s.CreateStateVersion(t.Context(), sv, fence, unitStatePendingCap); err != nil {
		t.Fatalf("CreateStateVersion = %v", err)
	}

	final := unitStateFinalize(t, s, sv, fence, "")

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

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL store tests", envURL)
	}

	schemaURL := isolatedURL(t, base)

	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		s.Close()
	})

	ctx := t.Context()

	fence, unit := unitStateTofuUnit(t, s)
	w := unitStateTofuLock(t, s, fence, unit)
	delivered := awaitingWriteBackJob(t, s, fence, unit)

	fenceVersion := unitStatePending(t, w)
	fenceVersion.Holder = w.Lock.Holder
	fenceVersion.LockID = w.Lock.ID
	fenceVersion.UnlockedWrite = false
	fenceVersion.Origin = identity.StateOrigin{Job: string(delivered.ID), Commit: ""}

	if createErr := s.CreateStateVersion(ctx, fenceVersion, fence, unitStatePendingCap); createErr != nil {
		t.Fatalf("CreateStateVersion = %v", createErr)
	}

	tamperStateVersionContent(t, schemaURL, fenceVersion.ID.String())

	at := time.UnixMilli(unitStateWriteAtMs + 2000).UTC()
	lock := w.Lock

	released := w
	released.Lock = workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}
	released.LastRelease = workspace.Release{
		At: at, LockID: lock.ID, Holder: lock.Holder, By: writeStateHolder, Kind: workspace.ReleaseNormal,
	}
	released.Revision++
	released.UpdatedAt = at

	releaseErr := s.FenceAndUnlock(ctx, released, fenceVersion, "", fence, stateOriginEntry(unit))

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

// tamperStateVersionContent replaces the recorded content key of the state
// version id through a second database handle, so the next finalize of the
// version matches no row inside its transaction.
func tamperStateVersionContent(t *testing.T, schemaURL, id string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	_, execErr := conn.Exec(t.Context(),
		`UPDATE state_versions SET content_key = 'tampered' WHERE id = $1`, id)

	if execErr != nil {
		t.Fatalf("tamper the recorded content = %v", execErr)
	}
}

// assertFinalizeStillPending fails t when sv is no longer a pending state
// version: a refused finalize must not change the row.
func assertFinalizeStillPending(t *testing.T, s *postgres.Store, sv workspace.StateVersion) {
	t.Helper()

	stored, err := s.GetStateVersion(t.Context(), sv.ID)
	if err != nil {
		t.Fatalf("GetStateVersion = %v", err)
	}

	if stored.Status != workspace.StatusPending {
		t.Fatalf("status of the refused version = %q, want %q", stored.Status, workspace.StatusPending)
	}
}
