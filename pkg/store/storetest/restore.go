package storetest

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// RestoreStore is every port the restore suite exercises: the backup store
// ports it seeds and swaps through, the applier under test, the fencer under
// test, and the restore registry under test. Every store adapter runs the
// suite once it implements the ports; until then a fake in restore_test.go
// runs it and proves each case can fail.
type RestoreStore interface {
	BackupStore
	backup.Applier
	backup.Fencer
}

// PortableRestoreStore is a RestoreStore that also exports and stages the
// portable format. The swap cases need it: a RestoreStore without the portable
// ports skips them.
type PortableRestoreStore interface {
	RestoreStore
	backup.PortableExporter
	backup.PortableStager
}

// RestoreFactory returns a fresh, empty store for one subtest.
type RestoreFactory func(t *testing.T) RestoreStore

// RestoreReopener is an optional RestoreStore capability for the
// close-and-reopen case: Close releases the store and Reopen returns a new
// handle to the same backing storage. The reopened handle keeps the restore
// ports, so the case asserts it back to a RestoreStore.
type RestoreReopener interface {
	RestoreStore
	// Close releases the store.
	Close() error
	// Reopen returns a new handle to the same backing storage.
	Reopen() BackupStore
}

// RunRestore executes the conformance suite of the restore ports against
// stores produced by newStore.
func RunRestore(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, newStore RestoreFactory)
		name string
	}{
		{name: "restores table creates, finishes, gets and lists", test: testRestoreRegistry},
		{name: "one restore runs at a time", test: testRestoreOneRunning},
		{name: "a backup starts after a restore of a running snapshot", test: testRestoreThenBackup},
		{name: "a restore starts after a restore of a running snapshot", test: testRestoreThenRestore},
		{name: "the running restore records its end after the swap", test: testRestoreRecordsEndAfterSwap},
		{name: "a restore row survives close and reopen", test: testRestoreRowSurvivesReopen},
		{name: "apply swaps the staged data in one step", test: testRestoreApplySwapsStaged},
		{name: "apply refuses too few working break-glass accounts", test: testRestoreRefusesTooFewWorkingBreakGlass},
		{name: "apply keeps the audit chain record of the target", test: testRestoreKeepsChainRecord},
		{name: "apply keeps the installation state of the target", test: testRestoreKeepsInstallationState},
		{name: "apply keeps the installation kind of the target", test: testRestoreKeepsInstallationKind},
		{
			name: "a restore keeps the backups, restores and installation rows of the target",
			test: testRestoreKeepsControlRows,
		},
		{
			name: "a restored log of another chain state checks valid after the migration",
			test: testRestoreChainAgreesAfterMigration,
		},
		{name: "apply drops the pre-backup row, which is re-recorded after", test: testRestoreApplyReRecordsBackup},
		{name: "apply without staged data is corrupt", test: testRestoreApplyWithoutStaging},
		{name: "bump increments every namespace and fences stale writes", test: testRestoreBumpFences},
		{name: "apply releases the workspace locks of the backup", test: testRestoreApplyReleasesWorkspaceLocks},
		{name: "export does not tear a concurrent stack write", test: testRestoreExportDoesNotTear},
		{name: "staging loads a backup without touching live data", test: testRestoreStagingKeepsLive},
		{name: "staging refuses a backup with an unsealed state version", test: testRestoreStagingRefusesUnsealedState},
		{
			name: "staging refuses a grant of a namespace principal outside its namespace",
			test: testRestoreStagingRefusesNamespacePrincipalGrants,
		},
		{
			name: "staging refuses a membership of a namespace principal in a group",
			test: testRestoreStagingRefusesNamespacePrincipalMemberships,
		},
		{name: "a new stage discards the previous staging area", test: testRestoreStagingReplacesPrevious},
		{name: "staging keeps a checkpoint held in blobs", test: testRestoreStagingKeepsCheckpointBlobs},
		{name: "a restore keeps the journal rows of a running update", test: testRestoreJournalBlob},
		{name: "a restore keeps the live durable operations", test: testRestoreKeepsDurableOperations},
		{name: "a restore keeps the live runner control rows", test: testRestoreKeepsRunnerControlRows},
		{name: "a restore keeps the live used ticket records", test: testRestoreKeepsUsedTickets},
		{name: "a restore leaves no replay record", test: testRestoreEmptiesIdempotencyRecords},
		{name: "round trip over every table keeps every row", test: testRestoreRoundTrip},
		{name: "a restore without restore-access keeps the access tables", test: testRestoreKeepsAccessTables},
		{
			name: "a restore keeps a principal created after the backup with its child rows",
			test: testRestoreKeepsLiveOnlyPrincipal,
		},
		{name: "a restore that keeps access refuses unknown namespaces and units", test: testRestoreKeepAccessMismatch},
		{
			name: "a read of the newest profile revision during a restore swap never answers not found",
			test: testRestoreProfileRevisionDuringSwap,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore)
		})
	}

	for _, tc := range restoreOperationCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore)
		})
	}
}

// newRestoreRecord returns a running restore record with the given start time.
func newRestoreRecord(id backup.RestoreID, backupID backup.ID, startedAt time.Time) backup.RestoreRecord {
	return backup.RestoreRecord{
		StartedAt:   startedAt,
		EndedAt:     time.Time{},
		ID:          id,
		BackupID:    backupID,
		PreBackupID: "",
		RequestedBy: "admin",
		Error:       "",
		State:       backup.RestoreChecking,
		DevReport:   nil,
	}
}

// mustCreateRestore starts a checking restore row with its operation,
// failing on error.
func mustCreateRestore(t *testing.T, s RestoreStore, r backup.RestoreRecord) backup.RestoreRecord {
	t.Helper()

	if err := restorePortsOf(t, s).ops.StartRestore(t.Context(), r, restoreOpStart(r.ID)); err != nil {
		t.Fatalf("start restore %s: %v", r.ID, err)
	}

	return r
}

// restoreClaim returns the live claim of the operation of restore id in s,
// and claims the operation when no claim holds it.
func restoreClaim(t *testing.T, s RestoreStore, id backup.RestoreID) durable.Claim {
	t.Helper()

	ops := s.DurableOperationsPort()

	all := restoreOps(t, ops)
	for i := range all {
		op := &all[i]
		if op.Key != backup.RestoreOperationKey(id) {
			continue
		}

		if op.State == durable.StateRunning {
			return op.Claim
		}

		claimed := mustClaimRestoreOp(t, ops, "instance-a")
		if claimed.ID != op.ID {
			t.Fatalf("claim took operation %s, want the operation %s of restore %s", claimed.ID, op.ID, id)
		}

		return claimed.Claim
	}

	t.Fatalf("restore %s has no operation", id)

	return durable.Claim{Operation: "", Owner: "", Token: 0, ExpiresAt: time.Time{}}
}

// mustMarkApplying moves the restore id to applying with the pre-backup ID,
// under the claim of its operation, failing on error.
func mustMarkApplying(t *testing.T, s RestoreStore, id backup.RestoreID, pre backup.ID) {
	t.Helper()

	fence := restoreClaim(t, s, id).Step(backup.RestoreStepPreBackup)
	if err := restorePortsOf(t, s).ops.MarkRestoreApplying(t.Context(), fence, id, pre, nil); err != nil {
		t.Fatalf("mark restore %s applying: %v", id, err)
	}
}

// mustGetRestore reads one restore row, failing on error.
func mustGetRestore(t *testing.T, s RestoreStore, id backup.RestoreID) backup.RestoreRecord {
	t.Helper()

	got, err := s.GetRestore(t.Context(), id)
	if err != nil {
		t.Fatalf("get restore %s: %v", id, err)
	}

	return got
}

// restoreFinish holds how to finish a running restore row: its ID, the end
// state with the error text, and the end time.
type restoreFinish struct {
	ID      backup.RestoreID
	State   backup.RestoreState
	Error   string
	EndedAt time.Time
}

// mustFinishRestore finishes a running restore row under the claim of its
// operation, and completes the operation, failing on error.
func mustFinishRestore(t *testing.T, s RestoreStore, finish restoreFinish) backup.RestoreRecord {
	t.Helper()

	claim := restoreClaim(t, s, finish.ID)
	end := backup.RestoreEnd{
		ID: finish.ID, State: finish.State, Error: finish.Error, EndedAt: finish.EndedAt, DevReport: nil,
		LeaveMaintenance: false,
	}

	got, err := restorePortsOf(t, s).ops.FinishRestore(t.Context(), claim.Step(backup.RestoreStepFinish), end)
	if err != nil {
		t.Fatalf("finish restore %s: %v", finish.ID, err)
	}

	if err := s.DurableOperationsPort().Complete(t.Context(), claim); err != nil {
		t.Fatalf("complete the operation of restore %s: %v", finish.ID, err)
	}

	return got
}

// testRestoreRegistry covers the restores table: create a running row, finish
// it succeeded or failed with the error text, get it, and list newest first.
func testRestoreRegistry(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)

	if empty := mustListRestores(t, s); len(empty) != 0 {
		t.Fatalf("restores = %+v, want none", empty)
	}

	first := checkRestoreCreateAndGet(t, s)
	checkRestoreFinishPaths(t, s, first)
	applied := checkRestoreApplying(t, s, first)
	checkRestoreFailedAndListed(t, s, first, applied)
}

// checkRestoreCreateAndGet creates the first running row, proves a duplicate
// start and a missing get fail, and returns the row.
func checkRestoreCreateAndGet(t *testing.T, s RestoreStore) backup.RestoreRecord {
	t.Helper()

	ctx := t.Context()

	first := mustCreateRestore(
		t, s, newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), backup.ID(ident.NewID("bkp-")), now()),
	)

	err := restorePortsOf(t, s).ops.StartRestore(ctx, first, restoreOpStart(first.ID))
	if !errors.Is(err, backup.ErrRestoreExists) {
		t.Fatalf("start duplicate restore: err = %v, want ErrRestoreExists", err)
	}

	if got := mustGetRestore(t, s, first.ID); got != first {
		t.Fatalf("restore = %+v, want %+v", got, first)
	}

	_, err = s.GetRestore(ctx, backup.RestoreID(ident.NewID("rst-")))
	if !errors.Is(err, backup.ErrRestoreNotFound) {
		t.Fatalf("get missing restore: err = %v, want ErrRestoreNotFound", err)
	}

	return first
}

// checkRestoreFinishPaths finishes the first row succeeded, then proves a
// second finish fails, and that a fence of another restore cannot finish it.
func checkRestoreFinishPaths(t *testing.T, s RestoreStore, first backup.RestoreRecord) {
	t.Helper()

	ctx := t.Context()
	endedAt := now().Add(time.Hour)
	claim := restoreClaim(t, s, first.ID)
	end := backup.RestoreEnd{
		ID: first.ID, State: backup.RestoreSucceeded, Error: "", EndedAt: endedAt, DevReport: nil,
		LeaveMaintenance: false,
	}

	finished, err := restorePortsOf(t, s).ops.FinishRestore(ctx, claim.Step(backup.RestoreStepFinish), end)
	if err != nil {
		t.Fatalf("finish restore: %v", err)
	}

	if finished.State != backup.RestoreSucceeded || !finished.EndedAt.Equal(endedAt) || finished.Error != "" {
		t.Fatalf("finished restore = %+v, want succeeded at %v without error", finished, endedAt)
	}

	end.State, end.Error = backup.RestoreFailed, "again"

	_, err = restorePortsOf(t, s).ops.FinishRestore(ctx, claim.Step("finish-again"), end)
	if !errors.Is(err, backup.ErrRestoreNotRunning) {
		t.Fatalf("finish a finished restore: err = %v, want ErrRestoreNotRunning", err)
	}

	end.ID = backup.RestoreID(ident.NewID("rst-"))

	_, err = restorePortsOf(t, s).ops.FinishRestore(ctx, claim.Step("finish-other"), end)
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("finish another restore under this claim: err = %v, want ErrClaimLost", err)
	}

	if err := s.DurableOperationsPort().Complete(ctx, claim); err != nil {
		t.Fatalf("complete the operation: %v", err)
	}
}

// checkRestoreApplying moves a second row to applying with the pre-backup ID,
// proves a second mark fails, finishes it, and returns it.
func checkRestoreApplying(t *testing.T, s RestoreStore, first backup.RestoreRecord) backup.RestoreRecord {
	t.Helper()

	ctx := t.Context()
	applying := mustCreateRestore(
		t, s, newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), first.BackupID, now().Add(90*time.Minute)),
	)
	preID := backup.ID(ident.NewID("bkp-"))

	mustMarkApplying(t, s, applying.ID, preID)

	if got := mustGetRestore(t, s, applying.ID); got.State != backup.RestoreApplying || got.PreBackupID != preID {
		t.Fatalf("applying restore = %+v, want applying with pre-backup %s", got, preID)
	}

	again := restoreClaim(t, s, applying.ID).Step("pre-backup-again")

	err := restorePortsOf(t, s).ops.MarkRestoreApplying(ctx, again, applying.ID, preID, nil)
	if !errors.Is(err, backup.ErrRestoreNotRunning) {
		t.Fatalf("mark an applying restore again: err = %v, want ErrRestoreNotRunning", err)
	}

	applied := mustFinishRestore(t, s, restoreFinish{
		ID:      applying.ID,
		State:   backup.RestoreSucceeded,
		Error:   "",
		EndedAt: now().Add(95 * time.Minute),
	})

	if applied.State != backup.RestoreSucceeded || applied.PreBackupID != preID {
		t.Fatalf("finished applying restore = %+v, want succeeded with pre-backup %s", applied, preID)
	}

	return applied
}

// checkRestoreFailedAndListed finishes a third row failed with the error text,
// creates a fourth, and proves the list holds all four newest first.
func checkRestoreFailedAndListed(
	t *testing.T, s RestoreStore, first, applied backup.RestoreRecord,
) {
	t.Helper()

	second := mustCreateRestore(
		t, s, newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), first.BackupID, now().Add(2*time.Hour)),
	)
	failedAt := now().Add(3 * time.Hour)
	secondDone := mustFinishRestore(t, s, restoreFinish{
		ID:      second.ID,
		State:   backup.RestoreFailed,
		Error:   "copy blobs: connection reset",
		EndedAt: failedAt,
	})

	if secondDone.State != backup.RestoreFailed || secondDone.Error != "copy blobs: connection reset" ||
		!secondDone.EndedAt.Equal(failedAt) {
		t.Fatalf("failed restore = %+v, want the error text and end time", secondDone)
	}

	third := mustCreateRestore(
		t, s, newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), first.BackupID, now().Add(4*time.Hour)),
	)

	list := mustListRestores(t, s)

	if len(list) != 4 || list[0].ID != third.ID || list[1].ID != secondDone.ID || list[2].ID != applied.ID ||
		list[3].ID != first.ID {
		t.Fatalf(
			"restores newest first = %v, want [%s %s %s %s]",
			restoreIDs(list),
			third.ID,
			secondDone.ID,
			applied.ID,
			first.ID,
		)
	}

	if list[1] != secondDone {
		t.Fatalf("listed failed restore = %+v, want %+v", list[1], secondDone)
	}
}

// restoreIDs returns the IDs of rows for failure messages.
func restoreIDs(rows []backup.RestoreRecord) []backup.RestoreID {
	ids := make([]backup.RestoreID, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}

	return ids
}

// testRestoreOneRunning refuses a second running restore with
// ErrRestoreRunning while the first one checks and while it applies, keeps
// ErrRestoreExists for a taken ID, and allows the next restore once the
// running one finished.
func testRestoreOneRunning(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ops := restorePortsOf(t, s).ops
	first := mustCreateRestore(
		t, s, newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), backup.ID(ident.NewID("bkp-")), now()),
	)
	second := newRestoreRecord(
		backup.RestoreID(ident.NewID("rst-")),
		backup.ID(ident.NewID("bkp-")),
		now().Add(time.Minute),
	)

	err := ops.StartRestore(
		t.Context(),
		second,
		restoreOpStart(second.ID),
	)
	if !errors.Is(err, backup.ErrRestoreRunning) {
		t.Fatalf("second restore while one checks: err = %v, want ErrRestoreRunning", err)
	}

	mustMarkApplying(t, s, first.ID, backup.ID(ident.NewID("bkp-")))

	err = ops.StartRestore(
		t.Context(),
		second,
		restoreOpStart(second.ID),
	)
	if !errors.Is(err, backup.ErrRestoreRunning) {
		t.Fatalf("second restore while one applies: err = %v, want ErrRestoreRunning", err)
	}

	if err := ops.StartRestore(t.Context(), first, restoreOpStart(first.ID)); !errors.Is(err, backup.ErrRestoreExists) {
		t.Fatalf("taken restore ID: err = %v, want ErrRestoreExists", err)
	}

	mustFinishRestore(t, s, restoreFinish{ID: first.ID, State: backup.RestoreSucceeded, Error: "", EndedAt: now()})
	mustCreateRestore(t, s, second)
}

// testRestoreThenBackup restores a snapshot while the target holds no control
// rows: a backup leaves the control tables out, so the running backup and
// the checking restore of the source never reach the target. The target
// keeps its own control rows, and a new backup starts after the apply.
func testRestoreThenBackup(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ctx := t.Context()
	ps := portableRestoreStores(t, newStore(t), newStore(t))

	running := mustCreateBackup(t, ps.src, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))
	mustCreateRestore(
		t, ps.src, newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), backup.ID(ident.NewID("bkp-")), now()),
	)

	sink, sum, files := exportPortable(t, ps.src)

	if _, err := ps.dst.StagePortable(ctx, sink.read, sum, files, StageAs(t, ps.dst)); err != nil {
		t.Fatalf("stage a snapshot with a running backup and a checking restore: %v", err)
	}

	if err := ps.dst.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staging area: %v", err)
	}

	if _, err := ps.dst.GetBackup(ctx, running.ID); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("get backup of the snapshot after the swap: err = %v, want ErrNotFound", err)
	}

	next := newBackupRecord(backup.ID(ident.NewID("bkp-")), now().Add(time.Hour))
	if err := ps.dst.CreateBackup(ctx, next); err != nil {
		t.Fatalf("start a backup after the restore: %v, want nil", err)
	}
}

// testRestoreThenRestore applies a snapshot while the target holds no
// control rows: a backup leaves the restores table out, so the checking or
// applying restore of the source never reaches the target. The target keeps
// its own control rows, and a new restore starts after the apply.
func testRestoreThenRestore(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	for _, applying := range []bool{false, true} {
		t.Run(fmt.Sprintf("applying=%t", applying), func(t *testing.T) {
			ctx := t.Context()
			ps := portableRestoreStores(t, newStore(t), newStore(t))

			held := mustCreateRestore(
				t,
				ps.src,
				newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), backup.ID(ident.NewID("bkp-")), now()),
			)

			if applying {
				mustMarkApplying(t, ps.src, held.ID, backup.ID(ident.NewID("bkp-")))
			}

			applySnapshot(t, ps)

			if _, err := ps.dst.GetRestore(ctx, held.ID); !errors.Is(err, backup.ErrRestoreNotFound) {
				t.Fatalf("get restore of the snapshot after the swap: err = %v, want ErrRestoreNotFound", err)
			}

			next := newRestoreRecord(
				backup.RestoreID(ident.NewID("rst-")),
				backup.ID(ident.NewID("bkp-")),
				now().Add(time.Hour),
			)
			if err := restorePortsOf(t, ps.dst).ops.StartRestore(ctx, next, restoreOpStart(next.ID)); err != nil {
				t.Fatalf("start a restore after the restore: %v, want nil", err)
			}
		})
	}
}

// testRestoreRecordsEndAfterSwap runs a restore on the target while it
// applies a snapshot. The control tables stay across the swap, so the
// running restore row of the target stays applying with its pre-backup ID,
// with no need to store it again, and then records its end. Any handle
// reads the row during the swap: it never leaves the registry.
func testRestoreRecordsEndAfterSwap(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ctx := t.Context()
	ps := portableRestoreStores(t, newStore(t), newStore(t))

	srcRestore := mustCreateRestore(
		t, ps.src, newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), backup.ID(ident.NewID("bkp-")), now()),
	)

	current := mustCreateRestore(
		t, ps.dst, newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), backup.ID(ident.NewID("bkp-")), now()),
	)

	preBackupID := backup.ID(ident.NewID("bkp-"))
	mustMarkApplying(t, ps.dst, current.ID, preBackupID)

	applySnapshot(t, ps)

	if _, err := ps.dst.GetRestore(ctx, srcRestore.ID); !errors.Is(err, backup.ErrRestoreNotFound) {
		t.Fatalf("get source restore after the swap: err = %v, want ErrRestoreNotFound", err)
	}

	kept := mustGetRestore(t, ps.dst, current.ID)
	if kept.State != backup.RestoreApplying || kept.PreBackupID != preBackupID || kept.BackupID != current.BackupID {
		t.Fatalf("running restore after the swap = %+v, want applying with pre-backup %s", kept, preBackupID)
	}

	end := now().Add(time.Hour)
	mustFinishRestore(t, ps.dst, restoreFinish{ID: current.ID, State: backup.RestoreSucceeded, Error: "", EndedAt: end})

	if got := mustGetRestore(t, ps.dst, current.ID); got.State != backup.RestoreSucceeded || !got.EndedAt.Equal(end) {
		t.Fatalf("running restore after its end = %+v, want succeeded at %v", got, end)
	}
}

// applySnapshot exports ps.src, stages the export on ps.dst and applies it.
func applySnapshot(t *testing.T, ps portableStores) {
	t.Helper()

	sink, sum, files := exportPortable(t, ps.src)

	if _, err := ps.dst.StagePortable(t.Context(), sink.read, sum, files, StageAs(t, ps.dst)); err != nil {
		t.Fatalf("stage the snapshot: %v", err)
	}

	if err := ps.dst.ApplyStaging(t.Context()); err != nil {
		t.Fatalf("apply staging area: %v", err)
	}
}

// testRestoreRowSurvivesReopen closes the store and reopens the same backing
// storage: every restore row stays. Stores without the RestoreReopener
// capability skip this case; the SQL adapters implement it.
func testRestoreRowSurvivesReopen(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)

	reopenable, ok := s.(RestoreReopener)
	if !ok {
		t.Skip("store does not support close and reopen")
	}

	// One restore runs at a time: the finished row runs and ends first.
	done := mustCreateRestore(
		t,
		s,
		newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), backup.ID(ident.NewID("bkp-")), now().Add(time.Hour)),
	)
	finished := mustFinishRestore(t, s, restoreFinish{
		ID:      done.ID,
		State:   backup.RestoreSucceeded,
		Error:   "",
		EndedAt: now().Add(2 * time.Hour),
	})
	running := mustCreateRestore(
		t, s, newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), backup.ID(ident.NewID("bkp-")), now()),
	)

	if err := reopenable.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, ok := reopenable.Reopen().(RestoreStore)
	if !ok {
		t.Skip("reopened store keeps no restore ports")
	}

	if gotRunning := mustGetRestore(t, reopened, running.ID); gotRunning != running {
		t.Fatalf("running restore after reopen = %+v, want %+v", gotRunning, running)
	}

	if gotFinished := mustGetRestore(t, reopened, finished.ID); gotFinished != finished {
		t.Fatalf("finished restore after reopen = %+v, want %+v", gotFinished, finished)
	}
}

// portableStores holds the source and the destination of a swap case as
// portable restore stores.
type portableStores struct {
	src PortableRestoreStore
	dst PortableRestoreStore
}

// portableRestoreStores returns src and dst as portable restore stores. A
// store without the portable ports skips the test: the portable format is its
// only export, so the swap cases have nothing to run.
func portableRestoreStores(t *testing.T, src, dst RestoreStore) portableStores {
	t.Helper()

	portableSrc, srcOK := src.(PortableRestoreStore)
	portableDst, dstOK := dst.(PortableRestoreStore)

	if !srcOK || !dstOK {
		t.Skip("the store has no portable export and stage")
	}

	return portableStores{src: portableSrc, dst: portableDst}
}

// testRestoreApplySwapsStaged stages an export of one store into another and
// applies it: the live data becomes the staged data in one step, every
// namespace is fenced by one version, and the staging area is gone after the
// apply.
func testRestoreApplySwapsStaged(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ctx := t.Context()
	src := newStore(t)
	ns := newNamespace(t, src, "acme")

	ps := portableRestoreStores(t, src, newStore(t))
	psrc, pdst := ps.src, ps.dst

	sink, sum, files := exportPortable(t, psrc)

	other := newNamespace(t, pdst, "other")

	staged, err := pdst.StagePortable(ctx, sink.read, sum, files, StageAs(t, pdst))
	if err != nil {
		t.Fatalf("stage restore: %v", err)
	}

	if len(mustListNamespaces(t, pdst)) != 1 {
		t.Fatal("live namespaces changed during the stage, want only the seeded one")
	}

	if err := pdst.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staging area: %v", err)
	}

	got := mustListNamespaces(t, pdst)

	if len(got) != 1 || got[0].Name != ns.Name || got[0].FailoverVersion != ns.FailoverVersion+1 {
		t.Fatalf("namespaces after apply = %+v, want [%+v] fenced by one", got, ns)
	}

	if got[0].ID != ns.ID {
		t.Fatalf("namespace ID after apply = %q, want %q: a restore keeps the ID", got[0].ID, ns.ID)
	}

	_ = other
	_ = staged

	if err := pdst.DiscardStaging(ctx); err != nil {
		t.Fatalf("discard staging area after apply: %v", err)
	}

	if err := pdst.ApplyStaging(ctx); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("apply after the swap: err = %v, want ErrCorrupt", err)
	}
}

// breakGlassRestoreStore is every port that the working break-glass restore
// case seeds and reads.
type breakGlassRestoreStore interface {
	PortableRestoreStore
	backup.AccessKeepingApplier
	principal.WorkingBreakGlassCounter
	principal.Store
	mfa.PasskeyStore
	access.GrantStore
}

// testRestoreRefusesTooFewWorkingBreakGlass proves that an apply refuses a
// restore that lowers the working break-glass count below the floor, and
// changes nothing, while a restore that does not lower the count is allowed.
// A restore that keeps the access tables keeps the live working accounts, so
// a staged account does not lower the count.
func testRestoreRefusesTooFewWorkingBreakGlass(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	tests := []breakGlassRestoreCase{
		replaceRestoreCase("replace refuses two live working accounts that become one", 2, 1, false, true),
		replaceRestoreCase("replace refuses a replay that leaves one of two", 2, 2, true, true),
		replaceRestoreCase("replace refuses one live working account that becomes none", 1, 0, false, true),
		replaceRestoreCase("replace allows two live working accounts that stay two", 2, 2, false, false),
		replaceRestoreCase("replace allows one live working account that stays one", 1, 1, false, false),
		replaceRestoreCase("replace allows one live working account that becomes two", 1, 2, false, false),
		keepAccessRestoreCase("keep access allows two live working accounts and one staged", 2, 1, false),
		keepAccessRestoreCase("keep access allows a replay that cannot lower the kept live data", 2, 2, true),
		keepAccessRestoreCase("keep access allows two live working accounts that stay two", 2, 2, false),
		keepAccessRestoreCase("keep access allows one live working account that stays one", 1, 0, false),
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runBreakGlassRestoreCase(t, newStore, tc)
		})
	}
}

// breakGlassRestoreCase is one row of the working break-glass restore case.
type breakGlassRestoreCase struct {
	name       string
	live       int
	staged     int
	revoke     bool
	keepAccess bool
	refuse     bool
}

// replaceRestoreCase returns a row of a restore that replaces the access
// tables.
func replaceRestoreCase(name string, live, staged int, revoke, refuse bool) breakGlassRestoreCase {
	return breakGlassRestoreCase{
		name: name, live: live, staged: staged, revoke: revoke, keepAccess: false, refuse: refuse,
	}
}

// keepAccessRestoreCase returns a row of a restore that keeps the access
// tables.
func keepAccessRestoreCase(name string, live, staged int, revoke bool) breakGlassRestoreCase {
	return breakGlassRestoreCase{
		name: name, live: live, staged: staged, revoke: revoke, keepAccess: true, refuse: false,
	}
}

// runBreakGlassRestoreCase runs one row of the working break-glass restore
// case: it seeds the live and the staged data, stages the backup, applies it,
// and checks the refusal or the count that is live after the swap.
func runBreakGlassRestoreCase(t *testing.T, newStore RestoreFactory, tc breakGlassRestoreCase) {
	t.Helper()

	if tc.keepAccess {
		runBreakGlassKeepAccessCase(t, newStore, tc)

		return
	}

	ctx := t.Context()
	src, dst := newStore(t), newStore(t)

	srcStore, srcOK := src.(breakGlassRestoreStore)
	dstStore, dstOK := dst.(breakGlassRestoreStore)

	if !srcOK || !dstOK {
		t.Skip("the store keeps no portable principals, passkeys or grants")
	}

	liveIDs, revoked := seedBreakGlassReplace(t, srcStore, dstStore, tc)

	checkLiveBreakGlassCount(t, dstStore, tc.live)

	live := breakGlassLiveStateOf(t, dstStore, liveIDs)

	applyErr := dstStore.ApplyStaging(ctx)

	if tc.refuse {
		if !errors.Is(applyErr, principal.ErrNoWorkingBreakGlass) {
			t.Fatalf("apply = %v, want ErrNoWorkingBreakGlass", applyErr)
		}

		assertBreakGlassLiveState(t, dstStore, liveIDs, live)

		return
	}

	if applyErr != nil {
		t.Fatalf("apply: %v", applyErr)
	}

	checkLiveBreakGlassCount(t, dstStore, tc.staged)

	if revoked != "" {
		if _, err := dstStore.GetPrincipal(ctx, revoked); err != nil {
			t.Fatalf("get the revoked break-glass account after the apply: %v", err)
		}
	}
}

// seedBreakGlassReplace seeds the staged data in src and the live data in
// dst, stages the backup into dst, and returns the live account IDs and the
// account that a revocation disables.
func seedBreakGlassReplace(
	t *testing.T, src, dst breakGlassRestoreStore, tc breakGlassRestoreCase,
) ([]string, string) {
	t.Helper()

	ctx := t.Context()

	seedBackupStore(t, src)
	seedBackupStore(t, dst)

	stagedIDs := breakGlassIDs(tc.staged)

	for _, id := range stagedIDs {
		seedWorkingBreakGlass(t, src, id)
	}

	liveIDs := breakGlassLiveIDs(tc, stagedIDs)

	for _, id := range liveIDs {
		seedWorkingBreakGlass(t, dst, id)
	}

	sink, sum, files := exportPortable(t, src)

	if _, err := dst.StagePortable(ctx, sink.read, sum, files, StageAs(t, dst)); err != nil {
		t.Fatalf("stage restore: %v", err)
	}

	revoked := ""
	if tc.revoke {
		revoked = stagedIDs[tc.live-1]
		disableBreakGlassForRestore(t, dst, revoked)
	}

	return liveIDs, revoked
}

// runBreakGlassKeepAccessCase runs one row that keeps the access tables. The
// backup comes from the same store, because the kept access rows name its
// namespace IDs. The live working accounts are the seeded ones plus the ones
// that the case adds after the export.
func runBreakGlassKeepAccessCase(t *testing.T, newStore RestoreFactory, tc breakGlassRestoreCase) {
	t.Helper()

	ctx := t.Context()
	s := newStore(t)

	store, ok := s.(breakGlassRestoreStore)
	if !ok {
		t.Skip("the store keeps no portable principals, passkeys or grants")
	}

	seedBackupStore(t, store)

	stagedIDs := breakGlassIDs(tc.staged)
	liveIDs := breakGlassLiveIDs(tc, stagedIDs)

	for _, id := range stagedIDs {
		seedWorkingBreakGlass(t, store, id)
	}

	sink, sum, files := exportPortable(t, store)

	for _, id := range liveIDs {
		if slices.Contains(stagedIDs, id) {
			continue
		}

		seedWorkingBreakGlass(t, store, id)
	}

	revoked := ""
	if tc.revoke {
		revoked = stagedIDs[tc.live-1]
		disableBreakGlassForRestore(t, store, revoked)
	}

	checkLiveBreakGlassCount(t, store, tc.live)

	live := breakGlassLiveStateOf(t, store, liveIDs)

	if _, err := store.StagePortable(ctx, sink.read, sum, files, StageAs(t, store)); err != nil {
		t.Fatalf("stage restore: %v", err)
	}

	if _, applyErr := store.ApplyStagingKeepingAccess(ctx); applyErr != nil {
		t.Fatalf("apply keeping the access tables: %v", applyErr)
	}

	checkLiveBreakGlassCount(t, store, tc.live)

	assertBreakGlassLiveState(t, store, liveIDs, live)

	if revoked != "" {
		if _, err := store.GetPrincipal(ctx, revoked); err != nil {
			t.Fatalf("get the revoked break-glass account after the apply: %v", err)
		}
	}
}

// checkLiveBreakGlassCount counts the live working break-glass accounts and
// returns the count after it proves the want.
func checkLiveBreakGlassCount(t *testing.T, s breakGlassRestoreStore, want int) int64 {
	t.Helper()

	got, err := s.CountWorkingBreakGlass(t.Context(), now())
	if err != nil {
		t.Fatalf("count the live working break-glass accounts: %v", err)
	}

	if got != int64(want) {
		t.Fatalf("live working break-glass accounts = %d, want %d", got, want)
	}

	return got
}

// breakGlassIDs returns the IDs of the first n break-glass accounts.
func breakGlassIDs(n int) []string {
	out := make([]string, 0, n)

	for i := 1; i <= n; i++ {
		out = append(out, fmt.Sprintf("prn-bg-%d", i))
	}

	return out
}

// breakGlassLiveIDs returns the IDs of the live break-glass accounts of tc.
// A revocation case seeds one more account than the live count and disables
// it, so that the journal holds a revocation that lowers the restored data.
func breakGlassLiveIDs(tc breakGlassRestoreCase, stagedIDs []string) []string {
	want := tc.live
	if tc.revoke {
		want++
	}

	out := make([]string, 0, want)

	for i := 1; i <= want; i++ {
		if i <= len(stagedIDs) {
			out = append(out, stagedIDs[i-1])

			continue
		}

		out = append(out, fmt.Sprintf("prn-bg-live-%d", i))
	}

	return out
}

// disableBreakGlassForRestore enters maintenance mode and disables the
// break-glass account id, so the journal holds the revocation.
func disableBreakGlassForRestore(t *testing.T, s breakGlassRestoreStore, id string) {
	t.Helper()

	if err := s.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	if err := s.DisablePrincipal(t.Context(), id, ServerChange(now(), "principal:"+id)); err != nil {
		t.Fatalf("disable staged break-glass account: %v", err)
	}
}

// seedWorkingBreakGlass stores a working break-glass account.
func seedWorkingBreakGlass(t *testing.T, s breakGlassRestoreStore, id string) {
	t.Helper()

	ctx := t.Context()

	p := principal.Principal{
		CreatedAt: now(), UpdatedAt: now(), DisabledAt: time.Time{},
		ID: id, Subject: id + "@example.com", DisplayName: id, Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: true,
	}

	if err := s.CreatePrincipal(ctx, p, ServerChange(now(), "principal:"+id)); err != nil {
		t.Fatalf("create break-glass principal %q: %v", id, err)
	}

	passkey := mfa.Passkey{
		CreatedAt: now(), LastUsedAt: time.Time{}, SuspendedAt: time.Time{},
		ID: "pky-" + id, PersonID: id, Name: "key-" + id,
		CredentialID: []byte("credential-" + id), PublicKey: []byte("public-key-" + id),
		Transports: nil, AAGUID: [16]byte{}, SignCount: 0, BackupEligible: false,
	}

	if err := s.AddPasskey(ctx, passkey, ServerChange(now(), "passkey:pky-"+id)); err != nil {
		t.Fatalf("add passkey of %q: %v", id, err)
	}

	grant := access.Grant{
		CreatedAt: now(), Condition: nil, Object: identity.ServerObject(), ID: "grt-" + id,
		Role: access.RoleSiteAdministrator, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: id},
	}

	err := s.CreateGrant(
		ctx,
		grant,
		namespace.Fence{Namespace: "", FailoverVersion: 0},
		ServerChange(now(), "grant:grt-"+id),
		nil,
	)
	if err != nil {
		t.Fatalf("create grant of %q: %v", id, err)
	}
}

// breakGlassLiveState holds the live rows that a refused restore must keep.
type breakGlassLiveState struct {
	principals map[string]principal.Principal
	passkeys   map[string][]mfa.Passkey
	grants     map[string]access.Grant
}

// breakGlassLiveStateOf reads the live rows of ids.
func breakGlassLiveStateOf(t *testing.T, s breakGlassRestoreStore, ids []string) breakGlassLiveState {
	t.Helper()

	ctx := t.Context()

	out := breakGlassLiveState{
		principals: make(map[string]principal.Principal, len(ids)),
		passkeys:   make(map[string][]mfa.Passkey, len(ids)),
		grants:     make(map[string]access.Grant, len(ids)),
	}

	for _, id := range ids {
		p, err := s.GetPrincipal(ctx, id)
		if err != nil {
			t.Fatalf("get principal %q: %v", id, err)
		}

		out.principals[id] = p

		keys, err := s.Passkeys(ctx, id)
		if err != nil {
			t.Fatalf("get passkeys of %q: %v", id, err)
		}

		out.passkeys[id] = keys

		grant, err := s.GetGrant(ctx, "grt-"+id)
		if err != nil {
			t.Fatalf("get grant of %q: %v", id, err)
		}

		out.grants[id] = grant
	}

	return out
}

// assertBreakGlassLiveState fails unless the live rows of ids are what want
// holds.
func assertBreakGlassLiveState(t *testing.T, s breakGlassRestoreStore, ids []string, want breakGlassLiveState) {
	t.Helper()

	got := breakGlassLiveStateOf(t, s, ids)

	if !reflect.DeepEqual(got.principals, want.principals) {
		t.Fatalf("live principals after the refused apply = %+v, want %+v", got.principals, want.principals)
	}

	if !reflect.DeepEqual(got.passkeys, want.passkeys) {
		t.Fatalf("live passkeys after the refused apply = %+v, want %+v", got.passkeys, want.passkeys)
	}

	if !reflect.DeepEqual(got.grants, want.grants) {
		t.Fatalf("live grants after the refused apply = %+v, want %+v", got.grants, want.grants)
	}
}

// chainRestoreStore is a store with the audit chain and its check.
type chainRestoreStore interface {
	audit.Store
	audit.ChainStore
	audit.ScopesChecker
}

// chainAt answers the time of the chain records of the restore cases.
func chainAt() time.Time {
	return time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC)
}

// testRestoreKeepsChainRecord proves that a restore keeps the audit chain
// record of the target installation: the backup does not carry it, so the
// source record never reaches the target. The swap notes that it replaced
// the log. A store without a chain store skips the case.
func testRestoreKeepsChainRecord(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	want := audit.ChainRecord{
		ChangedAt:          chainAt(),
		ChangedBy:          "dst",
		State:              audit.ChainStateOn,
		Pending:            false,
		EnteredMaintenance: false,
		MigratingAt:        time.Time{},
		LogReplaced:        false,
	}

	to, _ := restoreChain(t, newStore, func(from chainRestoreStore, _ namespace.Namespace) {
		saveChainRecord(t, from, audit.ChainRecord{
			ChangedAt: chainAt(), ChangedBy: "src", State: audit.ChainStateOff, Pending: true,
			EnteredMaintenance: false, MigratingAt: time.Time{}, LogReplaced: false,
		})
	}, func(to chainRestoreStore) { saveChainRecord(t, to, want) })

	got, err := to.ChainRecord(t.Context())
	if err != nil {
		t.Fatalf("chain record after the restore: %v", err)
	}

	want.LogReplaced = true
	if !got.ChangedAt.Equal(want.ChangedAt) || got.ChangedBy != want.ChangedBy || got.State != want.State ||
		got.Pending != want.Pending || got.EnteredMaintenance != want.EnteredMaintenance ||
		!got.MigratingAt.Equal(want.MigratingAt) || got.LogReplaced != want.LogReplaced {
		t.Fatalf("chain record after the restore = %+v, want the target record %+v", got, want)
	}
}

// testRestoreKeepsInstallationState proves that the swap keeps the
// installation state of the target in both directions: a target in
// maintenance mode stays in maintenance mode after the swap of a backup taken
// in normal operation, so writes stay refused until the restore leaves
// maintenance mode itself; and a target in normal operation stays normal
// after the swap of a backup taken in maintenance mode, such as the backup
// that a restore takes before it applies. A store without the installation
// state skips the case.
func testRestoreKeepsInstallationState(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	cases := []struct {
		source maintenance.State
		target maintenance.State
	}{
		{source: maintenance.StateNormal, target: maintenance.StateMaintenance},
		{source: maintenance.StateMaintenance, target: maintenance.StateNormal},
	}

	for _, tc := range cases {
		ctx := t.Context()
		ps := portableRestoreStores(t, newStore(t), newStore(t))

		from, okSrc := ps.src.(maintenance.Store)
		to, okDst := ps.dst.(maintenance.Store)

		if !okSrc || !okDst {
			t.Skip("the store has no installation state")
		}

		setInstallationState(t, from, tc.source)
		setInstallationState(t, to, tc.target)

		sink, sum, files := exportPortable(t, ps.src)

		if _, err := ps.dst.StagePortable(ctx, sink.read, sum, files, StageAs(t, ps.dst)); err != nil {
			t.Fatalf("stage restore: %v", err)
		}

		if err := ps.dst.ApplyStaging(ctx); err != nil {
			t.Fatalf("apply staging area: %v", err)
		}

		got, err := to.GetState(ctx)
		if err != nil {
			t.Fatalf("installation state after the swap: %v", err)
		}

		if got != tc.target {
			t.Fatalf("installation state after the swap of a %s backup = %q, want the target state %q",
				tc.source, got, tc.target)
		}
	}
}

// testRestoreKeepsInstallationKind proves that a backup leaves the installation
// kind out and a restore keeps the kind of the live row: after the swap, a
// claim of another kind still returns the kind of the target. A store without
// the kind port skips the case.
func testRestoreKeepsInstallationKind(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	cases := []struct {
		name string
		// source and target are the kinds claimed before the backup and the
		// restore; empty claims nothing.
		source installation.Kind
		target installation.Kind
		// other is the kind that the claim after the swap asks for, and want
		// the kind that the claim must return.
		other installation.Kind
		want  installation.Kind
	}{
		{
			name: "a dev target stays dev after a production backup", source: installation.KindProduction,
			target: installation.KindDev, other: installation.KindProduction, want: installation.KindDev,
		},
		{
			name: "a dev target stays dev after a backup with no kind", source: "",
			target: installation.KindDev, other: installation.KindProduction, want: installation.KindDev,
		},
		{
			name: "a target with no kind takes none from a dev backup", source: installation.KindDev,
			target: "", other: installation.KindProduction, want: installation.KindProduction,
		},
	}

	for _, tc := range cases {
		ctx := t.Context()
		ps := portableRestoreStores(t, newStore(t), newStore(t))

		from, okSrc := ps.src.(installation.KindStore)
		to, okDst := ps.dst.(installation.KindStore)

		if !okSrc || !okDst {
			t.Skip("the store has no installation kind")
		}

		claimKindIfSet(t, from, tc.source)
		claimKindIfSet(t, to, tc.target)

		applySnapshot(t, ps)

		got, err := to.ClaimKind(ctx, tc.other)
		if err != nil {
			t.Fatalf("%s: claim %q after the swap: %v", tc.name, tc.other, err)
		}

		if got != tc.want {
			t.Fatalf("%s: claim %q after the swap = %q, want %q", tc.name, tc.other, got, tc.want)
		}
	}
}

// claimKindIfSet claims kind in s, and claims nothing for an empty kind.
func claimKindIfSet(t *testing.T, s installation.KindStore, kind installation.Kind) {
	t.Helper()

	if kind == "" {
		return
	}

	if _, err := s.ClaimKind(t.Context(), kind); err != nil {
		t.Fatalf("claim %q: %v", kind, err)
	}
}

// setInstallationState stores state as the installation state of s.
func setInstallationState(t *testing.T, s maintenance.Store, state maintenance.State) {
	t.Helper()

	if err := s.SetState(t.Context(), state); err != nil {
		t.Fatalf("set installation state %q: %v", state, err)
	}
}

// testRestoreKeepsControlRows proves that a restore keeps the backups,
// restores and installation rows of the target on both dialects: a backup
// leaves the control tables out, so the rows of the source never reach the
// target, and the swap never renames the control schema. Any handle reads
// the restore row during the swap: after the apply the running restore is
// still readable, also through a reopened handle.
func testRestoreKeepsControlRows(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ps := portableRestoreStores(t, newStore(t), newStore(t))

	target := seedTargetControlRows(t, ps.dst)
	srcBackup, srcRestore := seedSourceControlRows(t, ps.src)

	applySnapshot(t, ps)

	checkTargetControlRows(t, ps.dst, target, srcBackup, srcRestore)

	// Any handle reads the restore row: a reopened handle to the same
	// backing storage still reads the running restore.
	checkReopenedControlRow(t, ps.dst, target.restoreID)
}

// targetControlRows holds the control rows of the restore target.
type targetControlRows struct {
	backup    backup.Record
	restoreID backup.RestoreID
	state     maintenance.State
}

// seedTargetControlRows writes a finished backup, a restore and the
// maintenance installation state to dst.
func seedTargetControlRows(t *testing.T, dst RestoreStore) targetControlRows {
	t.Helper()

	targetBackup := mustCreateBackup(t, dst, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))
	done := mustFinishBackup(t, dst, backupFinish{
		ID: targetBackup.ID, State: backup.StateSucceeded, Error: "", EndedAt: now().Add(time.Hour),
	})
	restore := mustCreateRestore(
		t, dst, newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), backup.ID(ident.NewID("bkp-")), now()),
	)

	var state maintenance.State

	if to, ok := dst.(maintenance.Store); ok {
		setInstallationState(t, to, maintenance.StateMaintenance)
		state = maintenance.StateMaintenance
	}

	return targetControlRows{backup: done, restoreID: restore.ID, state: state}
}

// seedSourceControlRows writes a finished backup and a restore with other IDs
// and the opposite installation state to src, and returns their IDs.
func seedSourceControlRows(t *testing.T, src RestoreStore) (backup.ID, backup.RestoreID) {
	t.Helper()

	srcBackup := mustCreateBackup(t, src, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))
	mustFinishBackup(t, src, backupFinish{
		ID: srcBackup.ID, State: backup.StateSucceeded, Error: "", EndedAt: now().Add(time.Hour),
	})
	srcRestore := mustCreateRestore(
		t, src, newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), backup.ID(ident.NewID("bkp-")), now()),
	)

	if from, ok := src.(maintenance.Store); ok {
		setInstallationState(t, from, maintenance.StateNormal)
	}

	return srcBackup.ID, srcRestore.ID
}

// checkTargetControlRows fails unless dst holds the target control rows and
// none of the source control rows.
func checkTargetControlRows(
	t *testing.T, dst RestoreStore, target targetControlRows, srcBackup backup.ID, srcRestore backup.RestoreID,
) {
	t.Helper()

	ctx := t.Context()

	if got := mustGetBackup(t, dst, target.backup.ID); got != target.backup {
		t.Fatalf("target backup after the swap = %+v, want %+v", got, target.backup)
	}

	if _, err := dst.GetBackup(ctx, srcBackup); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("get source backup after the swap: err = %v, want ErrNotFound", err)
	}

	kept := mustGetRestore(t, dst, target.restoreID)
	if kept.ID != target.restoreID {
		t.Fatalf("target restore after the swap = %+v, want %s", kept, target.restoreID)
	}

	if _, err := dst.GetRestore(ctx, srcRestore); !errors.Is(err, backup.ErrRestoreNotFound) {
		t.Fatalf("get source restore after the swap: err = %v, want ErrRestoreNotFound", err)
	}

	if to, ok := dst.(maintenance.Store); ok {
		if got, err := to.GetState(ctx); err != nil || got != target.state {
			t.Fatalf("installation state after the swap = %q, %v, want %q", got, err, target.state)
		}
	}
}

// checkReopenedControlRow fails unless a reopened handle to the same backing
// storage still reads the target restore row.
func checkReopenedControlRow(t *testing.T, dst RestoreStore, id backup.RestoreID) {
	t.Helper()

	reopenable, ok := dst.(RestoreReopener)
	if !ok {
		return
	}

	if err := reopenable.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, ok := reopenable.Reopen().(RestoreStore)
	if !ok {
		t.Skip("reopened store keeps no restore ports")
	}

	if got, err := reopened.GetRestore(t.Context(), id); err != nil || got.ID != id {
		t.Fatalf("get target restore through a reopened handle = %+v, %v, want %s", got, err, id)
	}
}

// testRestoreChainAgreesAfterMigration proves that a restored log from an
// installation with the chain off disagrees with a target that keeps the
// chain on, and that the migration of the target makes every scope check
// valid. A store without a chain store skips the case.
func testRestoreChainAgreesAfterMigration(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	to, acme := restoreChain(t, newStore, func(from chainRestoreStore, acme namespace.Namespace) {
		appendDecrypts(t, from, acme, chainAt(), 1)

		change := audit.ChainChange{At: chainAt().Add(time.Minute), By: "src", To: audit.ChainStateOff}
		migrateChain(t, from, []namespace.Fence{acme.Fence()}, change)

		appendDecrypts(t, from, acme, chainAt().Add(2*time.Minute), 1)
	}, func(chainRestoreStore) {})

	scopes := []namespace.Name{acme.Name}

	agrees, err := audit.LogAgrees(t.Context(), to, scopes, audit.ChainStateOn)
	if err != nil || agrees {
		t.Fatalf("restored log against the chain on: agrees %v, err %v, want a disagreement", agrees, err)
	}

	// acme was fenced by the swap: read its fence again.
	fenced := restoredFence(t, to, acme.Name)

	change := audit.ChainChange{At: chainAt().Add(time.Hour), By: "alice", To: audit.ChainStateOn}
	migrateChain(t, to, []namespace.Fence{fenced}, change)

	wantScopesValid(t, to, acme.Name)
}

// restoredFence answers the fence of the namespace name in s after a
// restore, which incremented its failover version.
func restoredFence(t *testing.T, s audit.Store, name namespace.Name) namespace.Fence {
	t.Helper()

	reader, ok := s.(namespace.Store)
	if !ok {
		t.Fatal("the store cannot read a namespace")
	}

	ns, err := reader.GetNamespace(t.Context(), name)
	if err != nil {
		t.Fatalf("get namespace %s: %v", name, err)
	}

	return ns.Fence()
}

// restoreChain restores a backup of a new store, which fill prepares, into
// a new store, which target prepares. It answers the target and the
// namespace acme. A store without a chain store skips the case.
//
//nolint:ireturn // The cases use the ports of both SQL adapters, which share no concrete type.
func restoreChain(
	t *testing.T, newStore RestoreFactory,
	fill func(from chainRestoreStore, acme namespace.Namespace), target func(to chainRestoreStore),
) (chainRestoreStore, namespace.Namespace) {
	t.Helper()

	ctx := t.Context()
	src := newStore(t)
	acme := newNamespace(t, src, "acme")

	ps := portableRestoreStores(t, src, newStore(t))

	from, okSrc := ps.src.(chainRestoreStore)
	to, okDst := ps.dst.(chainRestoreStore)

	if !okSrc || !okDst {
		t.Skip("the store has no audit chain store")
	}

	fill(from, acme)
	target(to)

	sink, sum, files := exportPortable(t, ps.src)

	if _, err := ps.dst.StagePortable(ctx, sink.read, sum, files, StageAs(t, ps.dst)); err != nil {
		t.Fatalf("stage restore: %v", err)
	}

	if err := ps.dst.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staging area: %v", err)
	}

	return to, acme
}

// saveChainRecord saves rec as the chain record of s.
func saveChainRecord(t *testing.T, s audit.ChainStore, rec audit.ChainRecord) {
	t.Helper()

	if err := s.SaveChainRecord(t.Context(), rec); err != nil {
		t.Fatalf("save chain record %+v: %v", rec, err)
	}
}

// migrateChain runs the migration of change in s.
func migrateChain(t *testing.T, s chainRestoreStore, fences []namespace.Fence, change audit.ChainChange) {
	t.Helper()

	if err := audit.MigrateChain(t.Context(), s, fences, change); err != nil {
		t.Fatalf("migrate the chain: %v", err)
	}
}

// appendDecrypts appends n decrypt entries to ns in s, one second apart
// after at.
func appendDecrypts(t *testing.T, s audit.Store, ns namespace.Namespace, at time.Time, n int) {
	t.Helper()

	for i := range n {
		e := ServerChange(at.Add(time.Duration(i+1)*time.Second), "pulumi:acme/web/prod")
		e.Namespace, e.Action = ns.Name, "unit:decrypt"

		if _, err := s.Append(t.Context(), ns.Fence(), e); err != nil {
			t.Fatalf("append entry %d: %v", i, err)
		}
	}
}

// wantScopesValid fails the test unless every scope of scopes checks valid.
func wantScopesValid(t *testing.T, s audit.ScopesChecker, scopes ...namespace.Name) {
	t.Helper()

	results, err := s.CheckEach(t.Context(), scopes)
	if err != nil {
		t.Fatalf("check scopes: %v", err)
	}

	for i, scope := range scopes {
		if i >= len(results) || !results[i].Valid {
			t.Fatalf("check %s: results %+v, want valid", scope, results)
		}
	}
}

// testRestoreApplyReRecordsBackup proves the swap keeps the backup row taken
// before the apply: the control tables stay across the swap with the live
// rows, so the pre-backup row survives on every dialect with no need to
// re-record it. The restore service still calls PreserveAfterRestore, which
// does nothing when the row already exists.
func testRestoreApplyReRecordsBackup(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ctx := t.Context()
	src := newStore(t)
	newNamespace(t, src, "acme")

	ps := portableRestoreStores(t, src, newStore(t))
	psrc, pdst := ps.src, ps.dst

	sink, sum, files := exportPortable(t, psrc)

	newNamespace(t, pdst, "other")

	if _, stageErr := pdst.StagePortable(ctx, sink.read, sum, files, StageAs(t, pdst)); stageErr != nil {
		t.Fatalf("stage restore: %v", stageErr)
	}

	pre := newBackupRecord(backup.ID(ident.NewID("bkp-")), now())

	if createErr := pdst.CreateBackup(ctx, pre); createErr != nil {
		t.Fatalf("create pre-backup %s: %v", pre.ID, createErr)
	}

	finished := mustFinishBackup(t, pdst, backupFinish{
		ID: pre.ID, State: backup.StateSucceeded, Error: "", EndedAt: now().Add(time.Hour),
	})

	if applyErr := pdst.ApplyStaging(ctx); applyErr != nil {
		t.Fatalf("apply staging area: %v", applyErr)
	}

	assertPreBackupKept(t, pdst, finished)
}

// assertPreBackupKept proves the swap kept the pre-backup row: the control
// tables stay across the swap, so the row is still listed and readable.
func assertPreBackupKept(t *testing.T, dst RestoreStore, finished backup.Record) {
	t.Helper()

	ctx := t.Context()

	if got, getErr := dst.GetBackup(ctx, finished.ID); getErr != nil || got != finished {
		t.Fatalf("get pre-backup after the swap = %+v, %v, want %+v", got, getErr, finished)
	}

	if !slices.Contains(mustListBackups(t, dst), finished) {
		t.Fatalf("backups omit the pre-backup %s after the swap", finished.ID)
	}
}

// testRestoreApplyWithoutStaging applies with no staged data and expects
// ErrCorrupt, with the live data unchanged.
func testRestoreApplyWithoutStaging(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ns := newNamespace(t, s, "acme")

	if err := s.ApplyStaging(t.Context()); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("apply without staged data: err = %v, want ErrCorrupt", err)
	}

	if got := mustListNamespaces(t, s); len(got) != 1 || got[0] != ns {
		t.Fatalf("namespaces after a refused apply = %+v, want [%+v]", got, ns)
	}
}

// testRestoreBumpFences increments the failover version of every namespace:
// a write with the old fence fails fenced, and a write with the fresh fence
// works.
func testRestoreBumpFences(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ctx := t.Context()
	s := newStore(t)
	ns := newNamespace(t, s, "acme")
	stale := ns.Fence()

	bumped, err := s.BumpFailoverVersions(ctx)
	if err != nil {
		t.Fatalf("bump failover versions: %v", err)
	}

	if bumped != 1 {
		t.Fatalf("bumped namespaces = %d, want 1", bumped)
	}

	got, err := s.GetNamespace(ctx, ns.Name)
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	if got.FailoverVersion != ns.FailoverVersion+1 {
		t.Fatalf("failover version = %d, want %d", got.FailoverVersion, ns.FailoverVersion+1)
	}

	createErr := s.CreateNamespace(ctx, namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now(),
		Name:                "other",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	})
	if createErr != nil {
		t.Fatalf("create namespace after bump: %v", createErr)
	}

	_, bumpErr := s.BumpFailoverVersions(ctx)
	if bumpErr != nil {
		t.Fatalf("bump failover versions again: %v", bumpErr)
	}

	// A write with the fence from before the bump is stale: creating a
	// stack with it must fail fenced, while the fresh fence writes.
	staleStack := newStack(ns.Name, "web", "stale")

	stackErr := s.CreateStack(ctx, staleStack, stale)
	if !errors.Is(stackErr, namespace.ErrFenced) {
		t.Fatalf("create stack with a stale fence: err = %v, want ErrFenced", stackErr)
	}

	fresh, err := s.GetNamespace(ctx, ns.Name)
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	if err := s.CreateStack(ctx, staleStack, fresh.Fence()); err != nil {
		t.Fatalf("create stack with the fresh fence: %v", err)
	}
}

// testRestoreApplyReleasesWorkspaceLocks proves that a restore releases the
// lock of a workspace that the backup holds. The client chooses the lock ID,
// so a client from before the restore could present the same ID and write
// into the restored data.
func testRestoreApplyReleasesWorkspaceLocks(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ctx := t.Context()
	src := newStore(t)
	ns := newNamespace(t, src, "acme")

	locked := lockTestWorkspace(t, src, ns, createTestWorkspace(t, src, ns, "prod"), "alice")
	free := createTestWorkspace(t, src, ns, "dev")

	ps := portableRestoreStores(t, src, newStore(t))

	// The inventory entry must hold the lock before the restore. Without
	// this, the check on the restored entry below could pass on its own.
	if workspaceInventoryLock(t, ps.src, ns.Name, locked.Ref.Name.String()) == nil {
		t.Fatalf("inventory lock of %s before the restore = none, want one", locked.Ref)
	}

	sink, sum, files := exportPortable(t, ps.src)

	if _, err := ps.dst.StagePortable(ctx, sink.read, sum, files, StageAs(t, ps.dst)); err != nil {
		t.Fatalf("stage restore: %v", err)
	}

	if err := ps.dst.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staging area: %v", err)
	}

	for _, ref := range []workspace.Ref{locked.Ref, free.Ref} {
		if got := workspaceInventoryLock(t, ps.dst, ns.Name, ref.Name.String()); got != nil {
			t.Fatalf("inventory lock of %s after restore = %+v, want none", ref, got)
		}
	}

	got, err := ps.dst.GetWorkspace(ctx, locked.Ref)
	if err != nil {
		t.Fatalf("get restored workspace %s: %v", locked.Ref, err)
	}

	if got.Lock != clearedLock() {
		t.Fatalf("lock of %s after restore = %+v, want no lock", locked.Ref, got.Lock)
	}

	if got.ID != locked.ID {
		t.Fatalf("workspace ID after restore = %q, want %q", got.ID, locked.ID)
	}

	other, err := ps.dst.GetWorkspace(ctx, free.Ref)
	if err != nil {
		t.Fatalf("get restored workspace %s: %v", free.Ref, err)
	}

	if other.Lock != clearedLock() {
		t.Fatalf("lock of %s after restore = %+v, want no lock", free.Ref, other.Lock)
	}
}

// workspaceInventoryLock returns the lock on the inventory entry of the
// OpenTofu workspace name in ns, or nil when the entry holds none. The console
// reads the lock of a workspace from this entry. It fails the test when the
// inventory has no such entry.
func workspaceInventoryLock(t *testing.T, s inventory.Reader, ns namespace.Name, name string) *inventory.Lock {
	t.Helper()

	entries, err := s.ListUnits(t.Context(), ns)
	if err != nil {
		t.Fatalf("list inventory of %q: %v", ns, err)
	}

	for i := range entries {
		if entries[i].Tool == "opentofu" && entries[i].Name == name {
			return entries[i].Lock
		}
	}

	t.Fatalf("inventory has no opentofu entry %q in %q", name, ns)

	return nil
}
