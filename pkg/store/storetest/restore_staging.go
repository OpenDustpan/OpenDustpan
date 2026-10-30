package storetest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// restoreStagingCases are the cases of backup.RestoreStaging.
func restoreStagingCases() []restoreCase {
	return []restoreCase{
		{name: "a staging area has the name of its restore and token", test: testRestoreAreaStage},
		{name: "a stage drops the earlier areas of its restore and keeps a newer one", test: testRestoreAreaStageDrops},
		{name: "a staging area refuses a name that is not valid", test: testRestoreAreaRefusesBadName},
		{name: "the swap of a staging area checks the fence in its transaction", test: testRestoreAreaSwapFenced},
		{name: "a cancel before the swap and the swap have one winner", test: testRestoreAreaSwapCanceled},
		{name: "cleanup drops the areas of its restore and of ended restores", test: testRestoreAreaCleanup},
	}
}

// stagedRestore is one restore of the staging cases: a destination store
// with its ports, a backup of a source store, and a started restore with a
// claim.
type stagedRestore struct {
	dst     PortableRestoreStore
	ops     backup.RestoreOperations
	staging backup.RestoreStaging
	sink    *portableSink
	sum     backup.Summary
	files   []backup.PortableFile
	id      backup.RestoreID
	held    durable.Operation
}

// newStagedRestore exports a seeded source store and starts a restore with
// a claim on a fresh destination store.
func newStagedRestore(t *testing.T, newStore RestoreFactory) *stagedRestore {
	t.Helper()

	src, dst := newStore(t), newStore(t)
	ports := restorePortsOf(t, dst)
	ps := portableRestoreStores(t, src, dst)

	seedBackupStore(t, ps.src)
	sink, sum, files := exportPortable(t, ps.src)

	id := mustStartRestoreOp(t, ports.ops)
	held := mustClaimRestoreOp(t, dst.DurableOperationsPort(), "instance-a")

	return &stagedRestore{
		dst: ps.dst, ops: ports.ops, staging: ports.staging, sink: sink, sum: sum, files: files, id: id, held: held,
	}
}

// input returns the stage input of the backup of r, with no dev strip.
func (r *stagedRestore) input(t *testing.T) backup.RestoreStageInput {
	t.Helper()

	return backup.RestoreStageInput{
		Open: r.sink.read, Want: r.sum, Files: r.files, Options: StageAs(t, r.dst), Strip: false,
		StripMode: backup.DevStripDetect,
	}
}

// stageArea stages the backup into area.
func (r *stagedRestore) stageArea(t *testing.T, area backup.StagingArea) {
	t.Helper()

	if _, _, err := r.staging.StageRestore(t.Context(), area, r.input(t)); err != nil {
		t.Fatalf("stage into %s: %v", area, err)
	}
}

// stage stages the backup into the area of the restore and token.
func (r *stagedRestore) stage(t *testing.T, token int64) backup.StagingArea {
	t.Helper()

	area, err := backup.NewStagingArea(r.id, token)
	if err != nil {
		t.Fatalf("staging area: %v", err)
	}

	r.stageArea(t, area)

	return area
}

// areas lists the staging areas of the destination.
func (r *stagedRestore) areas(t *testing.T) []backup.StagingArea {
	t.Helper()

	got, err := r.staging.ListRestoreAreas(t.Context())
	if err != nil {
		t.Fatalf("list staging areas: %v", err)
	}

	return got
}

func testRestoreAreaStage(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newStagedRestore(t, newStore)
	area := r.stage(t, r.held.Claim.Token)

	// A second stage into the same area replaces it.
	r.stage(t, r.held.Claim.Token)

	if got := r.areas(t); len(got) != 1 || got[0] != area {
		t.Fatalf("staging areas = %+v, want only %s", got, area)
	}

	if rows := mustListRestores(t, r.dst); len(rows) != 1 || rows[0].State != backup.RestoreChecking {
		t.Fatalf("restores after the stages = %+v, want the one checking restore", rows)
	}
}

func testRestoreAreaStageDrops(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newStagedRestore(t, newStore)
	token := r.held.Claim.Token

	// A newer holder staged first. The stale holder of the older token
	// stages after it: it drops nothing of the newer holder.
	newer := r.stage(t, token+2)
	older := r.stage(t, token)

	if got := r.areas(t); len(got) != 2 || !slices.Contains(got, newer) || !slices.Contains(got, older) {
		t.Fatalf("staging areas = %+v, want %s and %s", got, older, newer)
	}

	// A holder between them drops the older area, and keeps the newer one.
	middle := r.stage(t, token+1)

	if got := r.areas(t); len(got) != 2 || !slices.Contains(got, newer) || !slices.Contains(got, middle) {
		t.Fatalf("staging areas = %+v, want %s and %s", got, middle, newer)
	}
}

func testRestoreAreaRefusesBadName(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newStagedRestore(t, newStore)

	// The service builds areas with NewStagingArea. Each adapter checks the
	// area again, so these values never reach a schema name or a path.
	bad := []backup.StagingArea{
		{Restore: "rst-../../escape", Token: 1},
		{Restore: "rst-aaaaaaaaaaaaaaaaaaaa\"; DROP SCHEMA public; --", Token: 1},
		{Restore: r.id, Token: 0},
		{Restore: r.id, Token: -1},
		{Restore: "", Token: 1},
	}

	for _, area := range bad {
		if _, _, err := r.staging.StageRestore(t.Context(), area, r.input(t)); !errors.Is(
			err, backup.ErrInvalidRestoreID,
		) {
			t.Fatalf("stage into %+v: error = %v, want ErrInvalidRestoreID", area, err)
		}

		_, err := r.staging.ApplyRestoreArea(t.Context(), r.held.Claim.Step(backup.RestoreStepSwap), area, false)
		if !errors.Is(err, backup.ErrInvalidRestoreID) {
			t.Fatalf("swap of %+v: error = %v, want ErrInvalidRestoreID", area, err)
		}

		// An ID that is not valid never reaches a drop. A valid ID with a bad
		// token is a valid cleanup, so the case leaves it out.
		if area.Restore == r.id {
			continue
		}

		cleanup := r.held.Claim.Step(backup.RestoreStepCleanup)

		_, err = r.staging.CleanRestoreAreas(t.Context(), cleanup, area.Restore, time.Second)
		if !errors.Is(err, backup.ErrInvalidRestoreID) {
			t.Fatalf("cleanup of %q: error = %v, want ErrInvalidRestoreID", area.Restore, err)
		}
	}

	if got := r.areas(t); len(got) != 0 {
		t.Fatalf("staging areas after refused names = %+v, want none", got)
	}

	durableOps := r.dst.DurableOperationsPort()
	mustLackStep(t, durableOps, r.held.ID, backup.RestoreStepSwap)
	mustLackStep(t, durableOps, r.held.ID, backup.RestoreStepCleanup)
}

func testRestoreAreaSwapFenced(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newStagedRestore(t, newStore)
	durableOps := r.dst.DurableOperationsPort()
	area := r.stage(t, r.held.Claim.Token)

	// A takeover: the first holder releases, and a second holder claims.
	taken := replaceClaim(t, durableOps, r.held)

	_, err := r.staging.ApplyRestoreArea(t.Context(), r.held.Claim.Step(backup.RestoreStepSwap), area, false)
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("swap by the replaced holder: error = %v, want ErrClaimLost", err)
	}

	if got := r.areas(t); !slices.Contains(got, area) {
		t.Fatalf("staging areas after a refused swap = %+v, want %s kept", got, area)
	}

	mustLackStep(t, durableOps, taken.ID, backup.RestoreStepSwap)

	// The swap runs only in maintenance mode: in normal mode the live holder
	// is refused too, and nothing changes.
	swap := taken.Claim.Step(backup.RestoreStepSwap)
	if _, err := r.staging.ApplyRestoreArea(t.Context(), swap, area, false); !errors.Is(
		err, maintenance.ErrStateChanged,
	) {
		t.Fatalf("swap in normal mode: error = %v, want ErrStateChanged", err)
	}

	if got := r.areas(t); !slices.Contains(got, area) {
		t.Fatalf("staging areas after a swap in normal mode = %+v, want %s kept", got, area)
	}

	mustLackStep(t, durableOps, taken.ID, backup.RestoreStepSwap)

	if err := r.dst.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	if _, err := r.staging.ApplyRestoreArea(t.Context(), swap, area, false); err != nil {
		t.Fatalf("swap by the live holder: %v", err)
	}

	mustHaveStep(t, durableOps, taken.ID, backup.RestoreStepSwap)

	if got := r.areas(t); slices.Contains(got, area) {
		t.Fatalf("staging areas after the swap = %+v, want %s gone", got, area)
	}

	// The restore row is in the control tables, so it stays across the swap.
	if row := mustGetRestore(t, r.dst, r.id); row.State != backup.RestoreChecking {
		t.Fatalf("restore after the swap = %+v, want checking", row)
	}
}

func testRestoreAreaSwapCanceled(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newStagedRestore(t, newStore)
	durableOps := r.dst.DurableOperationsPort()

	// The restore depends on the cancel guard, so a store with the restore
	// ports must have it: the case fails, it does not skip.
	canceler, ok := durableOps.(durable.StepCanceler)
	if !ok {
		t.Fatal("the store of durable operations does not implement durable.StepCanceler")
	}

	area := r.stage(t, r.held.Claim.Token)

	if err := canceler.CancelBefore(t.Context(), r.held.ID, backup.RestoreStepSwap); err != nil {
		t.Fatalf("cancel before the swap: %v", err)
	}

	_, err := r.staging.ApplyRestoreArea(t.Context(), r.held.Claim.Step(backup.RestoreStepSwap), area, false)
	if !errors.Is(err, durable.ErrCanceled) {
		t.Fatalf("swap after the cancel: error = %v, want ErrCanceled", err)
	}

	if got := r.areas(t); !slices.Contains(got, area) {
		t.Fatalf("staging areas after a canceled swap = %+v, want %s kept", got, area)
	}

	mustLackStep(t, durableOps, r.held.ID, backup.RestoreStepSwap)
}

func testRestoreAreaCleanup(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newStagedRestore(t, newStore)
	durableOps := r.dst.DurableOperationsPort()
	ctx := t.Context()

	// The area of the live holder, and the area of a newer token that a
	// stale stage left: cleanup drops every area of its own restore.
	newer := r.stage(t, r.held.Claim.Token+1)
	own := r.stage(t, r.held.Claim.Token)

	// An area of a restore that has no row here: another installation can
	// share the database, so cleanup keeps it.
	foreign, err := backup.NewStagingArea(backup.MintRestoreID(), 1)
	if err != nil {
		t.Fatalf("foreign area: %v", err)
	}

	r.stageArea(t, foreign)

	cleanup := r.held.Claim.Step(backup.RestoreStepCleanup)

	dropped, err := r.staging.CleanRestoreAreas(ctx, cleanup, r.id, time.Second)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if len(dropped) != 2 || !slices.Contains(dropped, newer) || !slices.Contains(dropped, own) {
		t.Fatalf("cleanup dropped %+v, want %s and %s", dropped, own, newer)
	}

	if got := r.areas(t); len(got) != 1 || got[0] != foreign {
		t.Fatalf("staging areas after the cleanup = %+v, want only %s", got, foreign)
	}

	mustHaveStep(t, durableOps, r.held.ID, backup.RestoreStepCleanup)

	// A second cleanup of the same step changes nothing.
	if _, err := r.staging.CleanRestoreAreas(ctx, cleanup, r.id, time.Second); !errors.Is(err, durable.ErrStepDone) {
		t.Fatalf("second cleanup: error = %v, want ErrStepDone", err)
	}

	r.finishAndCleanNext(t, foreign)
}

// finishAndCleanNext ends the restore of r with an area left behind, starts
// the next restore, and proves that its cleanup drops the area of the ended
// restore and keeps foreign.
func (r *stagedRestore) finishAndCleanNext(t *testing.T, foreign backup.StagingArea) {
	t.Helper()

	durableOps := r.dst.DurableOperationsPort()
	ctx := t.Context()
	left := r.stage(t, r.held.Claim.Token+2)

	end := backup.RestoreEnd{
		ID: r.id, State: backup.RestoreFailed, Error: "stopped", EndedAt: now(), DevReport: nil,
		LeaveMaintenance: false,
	}

	if _, err := r.ops.FinishRestore(ctx, r.held.Claim.Step(backup.RestoreStepFinish), end); err != nil {
		t.Fatalf("finish: %v", err)
	}

	if err := durableOps.Complete(ctx, r.held.Claim); err != nil {
		t.Fatalf("complete: %v", err)
	}

	next := mustStartRestoreOp(t, r.ops)
	held := mustClaimRestoreOp(t, durableOps, "instance-a")

	dropped, err := r.staging.CleanRestoreAreas(ctx, held.Claim.Step(backup.RestoreStepCleanup), next, time.Second)
	if err != nil {
		t.Fatalf("cleanup of the next restore: %v", err)
	}

	if len(dropped) != 1 || dropped[0] != left {
		t.Fatalf("cleanup of the next restore dropped %+v, want %s of the ended restore", dropped, left)
	}

	if got := r.areas(t); len(got) != 1 || got[0] != foreign {
		t.Fatalf("staging areas after the next cleanup = %+v, want only %s", got, foreign)
	}
}
