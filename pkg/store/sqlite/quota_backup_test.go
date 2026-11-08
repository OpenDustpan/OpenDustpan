package sqlite_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// quotaBackupRig is one store with the backup and restore services over it,
// as the server wires them.
type quotaBackupRig struct {
	store    *backupStore
	backups  *backup.Service
	restores *backup.RestoreService
}

// newQuotaBackupRig returns a fresh store with the backup and restore
// services over it, a fresh live blob store and the shared backup target.
func newQuotaBackupRig(t *testing.T, target *fs.Store) *quotaBackupRig {
	t.Helper()

	st, ok := openBackup(t).(*backupStore)
	if !ok {
		t.Fatal("the backup store is not a *backupStore")
	}

	if _, err := st.ClaimKind(t.Context(), installation.KindProduction); err != nil {
		t.Fatalf("claim the installation kind: %v", err)
	}

	live, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open live blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := live.Close(); closeErr != nil {
			t.Errorf("close live blob store: %v", closeErr)
		}
	})

	backups, err := backup.NewService(backup.Config{
		Exporter: st, Kinds: st, Registry: st, Blobs: live, Target: target, TargetName: "dir:test",
		Operations: st.DurableOperationsPort(), Version: "test", StateOpener: nil, Owner: "", Lease: 0, Now: nil,
		MaxSQLBytes: 0, Logger: nil,
	})
	if err != nil {
		t.Fatalf("new backup service: %v", err)
	}

	restores, err := backup.NewRestoreService(backup.RestoreConfig{
		Live: st, Operations: st.DurableOperationsPort(), Backups: backups,
		Maintenance: storetest.StateMaintenance{State: st, Hold: false}, Blobs: live, Target: target,
		Key: nil, Installation: "test", AfterApply: nil, Admit: nil, AllowDevBackups: false, AllowDevHint: "",
		Owner: "", Lease: 0, CleanupLockWait: 0, RetryBase: time.Millisecond, Now: nil, Logger: nil,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	return &quotaBackupRig{store: st, backups: backups, restores: restores}
}

// TestQuotaBackupRestore proves that a backup and a restore keep the rows of
// the quota counter: a settled count, one reservation with two keys and one
// recorded request. Usage answers the same after the restore, and the
// reservation still settles.
func TestQuotaBackupRestore(t *testing.T) {
	t.Parallel()

	target, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := target.Close(); closeErr != nil {
			t.Errorf("close backup target: %v", closeErr)
		}
	})

	source := newQuotaBackupRig(t, target)
	fence := quotaFence(t, source.store.Store, "payments")

	counter := source.store.QuotaCounter()
	at := quotaTime()

	all := quota.Key{Namespace: "payments", Blueprint: "postgres", Kind: quota.KindUnits, Scope: "namespace"}
	mine := quota.Key{Namespace: "payments", Blueprint: "postgres", Kind: quota.KindUnits, Scope: "requester:alice"}
	requests := quota.Key{
		Namespace: "payments",
		Blueprint: "postgres",
		Kind:      quota.KindRequests,
		Scope:     "requester:alice",
	}

	if err = counter.SetSettled(t.Context(), all, 3, fence); err != nil {
		t.Fatalf("SetSettled() = %v", err)
	}

	reserved := quota.Reservation{
		ID: "req-1/1", Namespace: "payments", State: quota.ReservationOpen,
		Keys: []quota.Key{all, mine}, At: at, EndsAt: at.Add(24 * time.Hour),
	}

	err = counter.InTx(t.Context(), fence, func(tx quota.Tx) error {
		_, reserveErr := tx.Reserve(t.Context(), reserved,
			[]quota.Check{{Key: all, Window: 0, Max: 10}, {Key: mine, Window: 0, Max: 10}})

		return reserveErr
	})
	if err != nil {
		t.Fatalf("Reserve() = %v", err)
	}

	err = counter.InTx(t.Context(), fence, func(tx quota.Tx) error {
		return tx.Record(t.Context(), at, []quota.Check{{Key: requests, Window: time.Hour, Max: 5}})
	})
	if err != nil {
		t.Fatalf("Record() = %v", err)
	}

	checks := []quota.Check{
		{Key: all, Window: 0, Max: 0},
		{Key: mine, Window: 0, Max: 0},
		{Key: requests, Window: time.Hour, Max: 0},
	}
	before := quotaUsages(t, source.store.Store, checks, at)

	backupID := backup.ID(ident.NewID("bkp-"))
	if _, err = source.backups.Run(t.Context(), backupID); err != nil {
		t.Fatalf("run backup: %v", err)
	}

	dest := newQuotaBackupRig(t, target)
	restoreID := backup.RestoreID(ident.NewID("rst-"))
	req := backup.RestoreRequest{
		BackupID: backupID, PreBackupID: "", CheckOnly: false, Force: false, RequestedBy: "tester",
		KeepAccessTables: false,
	}

	if err = dest.restores.Start(t.Context(), restoreID, req); err != nil {
		t.Fatalf("start restore: %v", err)
	}

	waitQuotaRestore(t, dest.restores, restoreID)

	after := quotaUsages(t, dest.store.Store, checks, at)
	if !slices.Equal(before, after) {
		t.Fatalf("Usage after the restore = %+v, want %+v", after, before)
	}

	// The restore fences every namespace, so the settle carries the fence of
	// the restored namespace.
	restored, err := dest.store.GetNamespace(t.Context(), "payments")
	if err != nil {
		t.Fatalf("get the namespace after the restore: %v", err)
	}

	if err := dest.store.QuotaCounter().Settle(t.Context(), reserved.ID, restored.Fence()); err != nil {
		t.Fatalf("Settle() after the restore = %v", err)
	}

	settled := quotaUsages(t, dest.store.Store, checks, at)
	want := []quota.Usage{
		{Key: all, Settled: 4, Reserved: 0, Requests: 0},
		{Key: mine, Settled: 1, Reserved: 0, Requests: 0},
		{Key: requests, Settled: 0, Reserved: 0, Requests: 1},
	}
	if !slices.Equal(settled, want) {
		t.Fatalf("Usage after the settle = %+v, want %+v", settled, want)
	}
}

// quotaRestoreRuns bounds the runs of the restore operation in
// waitQuotaRestore. Each run takes one step, and a restore has fewer steps.
const quotaRestoreRuns = 100

// waitQuotaRestore runs the restore operation, as a worker does, until the
// restore succeeds. It fails the test after quotaRestoreRuns runs.
func waitQuotaRestore(t *testing.T, restores *backup.RestoreService, id backup.RestoreID) {
	t.Helper()

	for range quotaRestoreRuns {
		row, err := restores.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("get restore: %v", err)
		}

		if row.State == backup.RestoreSucceeded {
			return
		}

		if row.State != backup.RestoreChecking && row.State != backup.RestoreApplying {
			t.Fatalf("restore state = %q, want succeeded (error %q)", row.State, row.Error)
		}

		// No worker runs in the case: it runs the restore operation itself.
		if err := restores.RunOnce(t.Context()); err != nil && !errors.Is(err, durable.ErrNoWork) &&
			t.Context().Err() == nil {
			t.Logf("run a restore operation: %v", err)
		}
	}

	t.Fatalf("restore %s did not succeed after %d runs", id, quotaRestoreRuns)
}
