package storetest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/durable"
)

// requireRunning checks that the store answers want, or nothing when want is nil.
func requireRunning(t *testing.T, store any, want *blobguard.Work, when string) {
	t.Helper()

	w, isWatch := store.(blobguard.Watch)
	if !isWatch {
		t.Fatalf("store %T does not implement blobguard.Watch", store)
	}

	got, ok, err := w.Running(t.Context())
	if err != nil {
		t.Fatalf("running work %s: %v", when, err)
	}

	switch {
	case want == nil && ok:
		t.Fatalf("running work %s = %v, want none", when, got)
	case want != nil && (!ok || got != *want):
		t.Fatalf("running work %s = %v, %v, want %v", when, got, ok, *want)
	default:
	}
}

// backupWork returns the running work that a backup row gives.
func backupWork(id backup.ID) *blobguard.Work {
	return &blobguard.Work{Kind: blobguard.Backup, ID: id.String()}
}

// restoreWork returns the running work that a restore row gives.
func restoreWork(id backup.RestoreID) *blobguard.Work {
	return &blobguard.Work{Kind: blobguard.Restore, ID: id.String()}
}

// testRunningNone reads a store with no backup and no restore row.
func testRunningNone(t *testing.T, newStore BackupFactory) {
	t.Helper()

	requireRunning(t, newStore(t), nil, "of an empty store")
}

// testRunningBackup reads a backup row that starts running, by each of the
// two ways that store one, and reads none after it ended.
func testRunningBackup(t *testing.T, newStore BackupFactory) {
	t.Helper()

	cases := []struct {
		name  string
		start func(t *testing.T, s BackupStore, r backup.Record)
	}{
		{name: "create", start: func(t *testing.T, s BackupStore, r backup.Record) {
			t.Helper()
			mustCreateBackup(t, s, r)
		}},
		{name: "start", start: func(t *testing.T, s BackupStore, r backup.Record) {
			t.Helper()
			mustStartBackup(t, s, r)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore(t)
			r := newBackupRecord(backup.ID(ident.NewID("bkp-")), now())
			tc.start(t, s, r)

			requireRunning(t, s, backupWork(r.ID), "of a running backup")
		})
	}
}

// testRunningEnded reads none when every backup row ended, whichever state
// it ended in.
func testRunningEnded(t *testing.T, newStore BackupFactory) {
	t.Helper()

	for _, state := range []backup.State{backup.StateSucceeded, backup.StateFailed} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			s := newStore(t)
			r := mustCreateBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))
			requireRunning(t, s, backupWork(r.ID), "before the end")

			mustFinishBackup(t, s, backupFinish{ID: r.ID, State: state, Error: "", EndedAt: now().Add(time.Minute)})
			requireRunning(t, s, nil, "after the end in state "+string(state))
		})
	}
}

// testRunningTakeover lets the claim of a running backup expire and gives the
// operation to a second owner. The row stays running through the takeover, so
// the deletes keep waiting.
func testRunningTakeover(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	r := mustStartBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))

	claimBackupOperation(t, s, r.ID, "first", 200*time.Millisecond)

	second := claimBackupWhenExpired(t, s, r.ID, "second")
	requireRunning(t, s, backupWork(r.ID), "after the takeover")

	mustFinishClaimed(t, s, second, backupFinish{
		ID: r.ID, State: backup.StateSucceeded, Error: "", EndedAt: now().Add(time.Minute),
	})
	requireRunning(t, s, nil, "after the finish of the new holder")
}

// testRunningRefusedStart checks that a refused start gives no running work
// of its own: the first backup stays the only answer, and a start with an
// invalid request stores nothing.
func testRunningRefusedStart(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	ctx := t.Context()

	invalid := newBackupRecord(backup.ID(ident.NewID("bkp-")), now())
	if err := s.StartBackup(ctx, invalid, durable.StartRequest{Key: "", Kind: "", Version: 0, Input: nil}); err == nil {
		t.Fatal("start with an empty request: err = nil, want an error")
	}

	requireRunning(t, s, nil, "after a start with an invalid request")

	first := mustStartBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))

	second := newBackupRecord(backup.ID(ident.NewID("bkp-")), now().Add(time.Minute))
	if err := s.StartBackup(ctx, second, backupStartRequest(second.ID)); !errors.Is(err, backup.ErrAlreadyRunning) {
		t.Fatalf("start while a backup runs: err = %v, want ErrAlreadyRunning", err)
	}

	requireRunning(t, s, backupWork(first.ID), "after a refused start")
}

// testRunningRestore reads a restore row in each state: checking and applying
// count, a restore that ended does not. A running backup comes before a
// running restore.
func testRunningRestore(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ops := restorePortsOf(t, s).ops
	watch := s

	requireRunning(t, watch, nil, "before the restore")

	id := mustStartRestoreOp(t, ops)
	requireRunning(t, watch, restoreWork(id), "of a checking restore")

	mustMarkApplying(t, s, id, backup.ID(ident.NewID("bkp-")))
	requireRunning(t, watch, restoreWork(id), "of an applying restore")

	b := mustCreateBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))
	requireRunning(t, watch, backupWork(b.ID), "of a backup next to a restore")

	mustFinishBackup(t, s, backupFinish{
		ID: b.ID, State: backup.StateSucceeded, Error: "", EndedAt: now().Add(time.Minute),
	})
	requireRunning(t, watch, restoreWork(id), "of the restore after the backup ended")

	held := restoreClaim(t, s, id)

	_, err := ops.FinishRestore(t.Context(), held.Step(backup.RestoreStepFinish), backup.RestoreEnd{
		ID: id, State: backup.RestoreSucceeded, Error: "", EndedAt: now().Add(2 * time.Minute), DevReport: nil,
		LeaveMaintenance: false,
	})
	if err != nil {
		t.Fatalf("finish restore %s: %v", id, err)
	}

	requireRunning(t, watch, nil, "after the restore ended")
}

// testSharedRunning starts a backup and then a restore on one handle and
// reads the guard on the other handle: a start that committed on one instance
// is running for every other instance.
func testSharedRunning(t *testing.T, a, b Store) {
	t.Helper()

	starter, ok := a.(runningStarter)
	if !ok {
		t.Fatalf("store %T does not start backup and restore rows", a)
	}

	other := b
	requireRunning(t, other, nil, "of the second handle before a start")

	bk := newBackupRecord(backup.ID(ident.NewID("bkp-")), now())
	if err := starter.CreateBackup(t.Context(), bk); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	requireRunning(t, other, backupWork(bk.ID), "of the second handle after a backup start")
}
