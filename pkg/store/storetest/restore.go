package storetest

import (
	"bytes"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
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
		{name: "restart marks every running restore failed", test: testRestoreRestartFailsRunning},
		{name: "a restore row survives close and reopen", test: testRestoreRowSurvivesReopen},
		{name: "apply swaps the staged data in one step", test: testRestoreApplySwapsStaged},
		{name: "apply drops the pre-backup row, which is re-recorded after", test: testRestoreApplyReRecordsBackup},
		{name: "apply without staged data is corrupt", test: testRestoreApplyWithoutStaging},
		{name: "bump increments every namespace and fences stale writes", test: testRestoreBumpFences},
	}

	for _, tc := range cases {
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
	}
}

// mustCreateRestore stores a running restore row, failing on error.
func mustCreateRestore(t *testing.T, s RestoreStore, r backup.RestoreRecord) backup.RestoreRecord {
	t.Helper()

	if err := s.CreateRestore(t.Context(), r); err != nil {
		t.Fatalf("create restore %s: %v", r.ID, err)
	}

	return r
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

// mustFinishRestore finishes a running restore row, failing on error.
func mustFinishRestore(t *testing.T, s RestoreStore, finish restoreFinish) backup.RestoreRecord {
	t.Helper()

	got, err := s.FinishRestore(t.Context(), finish.ID, finish.State, finish.Error, finish.EndedAt)
	if err != nil {
		t.Fatalf("finish restore %s: %v", finish.ID, err)
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
// create and a missing get fail, and returns the row.
func checkRestoreCreateAndGet(t *testing.T, s RestoreStore) backup.RestoreRecord {
	t.Helper()

	ctx := t.Context()

	first := mustCreateRestore(
		t, s, newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), backup.ID(ident.NewID("bkp-")), now()),
	)

	err := s.CreateRestore(ctx, first)
	if !errors.Is(err, backup.ErrRestoreExists) {
		t.Fatalf("create duplicate restore: err = %v, want ErrRestoreExists", err)
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
// second finish and a finish of a missing row fail.
func checkRestoreFinishPaths(t *testing.T, s RestoreStore, first backup.RestoreRecord) {
	t.Helper()

	ctx := t.Context()
	endedAt := now().Add(time.Hour)
	finished := mustFinishRestore(t, s, restoreFinish{
		ID:      first.ID,
		State:   backup.RestoreSucceeded,
		Error:   "",
		EndedAt: endedAt,
	})

	if finished.State != backup.RestoreSucceeded || !finished.EndedAt.Equal(endedAt) || finished.Error != "" {
		t.Fatalf("finished restore = %+v, want succeeded at %v without error", finished, endedAt)
	}

	_, err := s.FinishRestore(ctx, first.ID, backup.RestoreFailed, "again", endedAt)
	if !errors.Is(err, backup.ErrRestoreNotRunning) {
		t.Fatalf("finish a finished restore: err = %v, want ErrRestoreNotRunning", err)
	}

	_, err = s.FinishRestore(ctx, backup.RestoreID(ident.NewID("rst-")), backup.RestoreFailed, "no", endedAt)
	if !errors.Is(err, backup.ErrRestoreNotFound) {
		t.Fatalf("finish a missing restore: err = %v, want ErrRestoreNotFound", err)
	}
}

// checkRestoreApplying moves a second row to applying with the pre-backup ID,
// proves a second mark and a mark of a missing row fail, finishes it, and
// returns it.
func checkRestoreApplying(t *testing.T, s RestoreStore, first backup.RestoreRecord) backup.RestoreRecord {
	t.Helper()

	ctx := t.Context()
	applying := mustCreateRestore(
		t, s, newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), first.BackupID, now().Add(90*time.Minute)),
	)
	preID := backup.ID(ident.NewID("bkp-"))

	if err := s.MarkRestoreApplying(ctx, applying.ID, preID); err != nil {
		t.Fatalf("mark restore applying: %v", err)
	}

	if got := mustGetRestore(t, s, applying.ID); got.State != backup.RestoreApplying || got.PreBackupID != preID {
		t.Fatalf("applying restore = %+v, want applying with pre-backup %s", got, preID)
	}

	err := s.MarkRestoreApplying(ctx, applying.ID, preID)
	if !errors.Is(err, backup.ErrRestoreNotRunning) {
		t.Fatalf("mark an applying restore again: err = %v, want ErrRestoreNotRunning", err)
	}

	err = s.MarkRestoreApplying(ctx, backup.RestoreID(ident.NewID("rst-")), preID)
	if !errors.Is(err, backup.ErrRestoreNotFound) {
		t.Fatalf("mark a missing restore: err = %v, want ErrRestoreNotFound", err)
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

// testRestoreRestartFailsRunning marks a running row failed at the next start,
// with the error text, and leaves finished rows alone.
func testRestoreRestartFailsRunning(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)

	if err := s.FailRunningRestores(t.Context(), now(), "no restore is running"); err != nil {
		t.Fatalf("fail running restores: %v", err)
	}

	running := mustCreateRestore(
		t, s, newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), backup.ID(ident.NewID("bkp-")), now()),
	)
	applying := mustCreateRestore(
		t,
		s,
		newRestoreRecord(
			backup.RestoreID(ident.NewID("rst-")),
			backup.ID(ident.NewID("bkp-")),
			now().Add(30*time.Minute),
		),
	)

	if err := s.MarkRestoreApplying(t.Context(), applying.ID, backup.ID(ident.NewID("bkp-"))); err != nil {
		t.Fatalf("mark restore applying: %v", err)
	}

	done := mustCreateRestore(
		t,
		s,
		newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), backup.ID(ident.NewID("bkp-")), now().Add(time.Hour)),
	)
	endedAt := now().Add(2 * time.Hour)
	finished := mustFinishRestore(t, s, restoreFinish{
		ID:      done.ID,
		State:   backup.RestoreSucceeded,
		Error:   "",
		EndedAt: endedAt,
	})

	restartedAt := now().Add(3 * time.Hour)

	if err := s.FailRunningRestores(t.Context(), restartedAt, "server stopped during restore"); err != nil {
		t.Fatalf("fail running restores: %v", err)
	}

	got := mustGetRestore(t, s, running.ID)

	if got.State != backup.RestoreFailed || got.Error != "server stopped during restore" ||
		!got.EndedAt.Equal(restartedAt) || !got.StartedAt.Equal(running.StartedAt) ||
		got.BackupID != running.BackupID {
		t.Fatalf("restarted restore = %+v, want failed with the error text and end time", got)
	}

	if stalled := mustGetRestore(t, s, applying.ID); stalled.State != backup.RestoreFailed {
		t.Fatalf("applying restore after restart = %+v, want failed", stalled)
	}

	if untouched := mustGetRestore(t, s, done.ID); untouched != finished {
		t.Fatalf("finished restore after restart = %+v, want %+v", untouched, finished)
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

	running := mustCreateRestore(
		t, s, newRestoreRecord(backup.RestoreID(ident.NewID("rst-")), backup.ID(ident.NewID("bkp-")), now()),
	)
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

	failErr := reopened.FailRunningRestores(t.Context(), now().Add(3*time.Hour), "server stopped during restore")
	if failErr != nil {
		t.Fatalf("fail running restores: %v", failErr)
	}

	if after := mustGetRestore(t, reopened, running.ID); after.State != backup.RestoreFailed {
		t.Fatalf("restarted restore after reopen = %+v, want failed", after)
	}
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

	var buf bytes.Buffer

	sum, err := src.Export(ctx, &buf)
	if err != nil {
		t.Fatalf("export source: %v", err)
	}

	dst := newStore(t)
	other := newNamespace(t, dst, "other")

	staged, err := dst.StageRestore(ctx, bytes.NewReader(buf.Bytes()), sum)
	if err != nil {
		t.Fatalf("stage restore: %v", err)
	}

	if len(mustListNamespaces(t, dst)) != 1 {
		t.Fatal("live namespaces changed during the stage, want only the seeded one")
	}

	if err := dst.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staging area: %v", err)
	}

	got := mustListNamespaces(t, dst)

	if len(got) != 1 || got[0].Name != ns.Name || got[0].FailoverVersion != ns.FailoverVersion+1 {
		t.Fatalf("namespaces after apply = %+v, want [%+v] fenced by one", got, ns)
	}

	_ = other
	_ = staged

	if err := dst.DiscardStaging(ctx); err != nil {
		t.Fatalf("discard staging area after apply: %v", err)
	}

	if err := dst.ApplyStaging(ctx); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("apply after the swap: err = %v, want ErrCorrupt", err)
	}
}

// testRestoreApplyReRecordsBackup proves the swap drops the backup row taken
// before the apply, and that the row is re-recorded after it: the staged
// snapshot predates the pre-backup row, so the one-step swap replaces it with
// the old live data on every dialect. The restore service re-records the row
// (backup.PreserveAfterRestore), so the undo source stays listed and readable
// (ADR 30 decision 9). The case mirrors that order: stage, record the
// pre-backup, apply, prove the row is gone, re-record it, and read it back.
func testRestoreApplyReRecordsBackup(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ctx := t.Context()
	src := newStore(t)
	newNamespace(t, src, "acme")

	var buf bytes.Buffer

	sum, exportErr := src.Export(ctx, &buf)
	if exportErr != nil {
		t.Fatalf("export source: %v", exportErr)
	}

	dst := newStore(t)
	newNamespace(t, dst, "other")

	if _, stageErr := dst.StageRestore(ctx, bytes.NewReader(buf.Bytes()), sum); stageErr != nil {
		t.Fatalf("stage restore: %v", stageErr)
	}

	pre := newBackupRecord(backup.ID(ident.NewID("bkp-")), now())

	if createErr := dst.CreateBackup(ctx, pre); createErr != nil {
		t.Fatalf("create pre-backup %s: %v", pre.ID, createErr)
	}

	finished, finishErr := dst.FinishBackup(ctx, pre.ID, backup.StateSucceeded, "", now().Add(time.Hour))
	if finishErr != nil {
		t.Fatalf("finish pre-backup %s: %v", pre.ID, finishErr)
	}

	if applyErr := dst.ApplyStaging(ctx); applyErr != nil {
		t.Fatalf("apply staging area: %v", applyErr)
	}

	assertPreBackupRerecorded(t, dst, finished)
}

// assertPreBackupRerecorded proves the swap dropped the pre-backup row and
// that re-recording it keeps it listed and readable: the staged snapshot
// predates the row, so the one-step swap replaces it with the old live data
// on every dialect. The restore service re-records the row
// (backup.PreserveAfterRestore), so the undo source survives (ADR 30 decision
// 9).
func assertPreBackupRerecorded(t *testing.T, dst RestoreStore, finished backup.Record) {
	t.Helper()

	ctx := t.Context()

	if _, getErr := dst.GetBackup(ctx, finished.ID); !errors.Is(getErr, backup.ErrNotFound) {
		t.Fatalf("get pre-backup after the swap: err = %v, want ErrNotFound", getErr)
	}

	if createErr := dst.CreateBackup(ctx, finished); createErr != nil {
		t.Fatalf("re-record pre-backup %s after the swap: %v", finished.ID, createErr)
	}

	if got, getErr := dst.GetBackup(ctx, finished.ID); getErr != nil || got != finished {
		t.Fatalf("get re-recorded pre-backup = %+v, %v, want %+v", got, getErr, finished)
	}

	if !slices.Contains(mustListBackups(t, dst), finished) {
		t.Fatalf("backups omit the re-recorded pre-backup %s after the swap", finished.ID)
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
		CreatedAt:       now(),
		Name:            "other",
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
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
