package postgres_test

import (
	"os"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/pkg/backup"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/jobcheck"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// quotaBackupRig is one store with the backup and restore services over
// them, as the server wires them.
type quotaBackupRig struct {
	store    *backupStore
	backups  *backup.Service
	restores *backup.RestoreService
}

// newQuotaBackupRig returns a fresh store with the backup and restore
// services over it, a fresh live blob store and the shared backup target.
func newQuotaBackupRig(t *testing.T, base, targetPath string) *quotaBackupRig {
	t.Helper()

	st := openBackup(t, base)

	if _, err := st.ClaimKind(t.Context(), installation.KindProduction); err != nil {
		t.Fatalf("claim the installation kind: %v", err)
	}

	live, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open live blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := live.Close(); closeErr != nil {
			t.Errorf("close live blob store: %v", closeErr)
		}
	})

	target, err := blobfs.Open(targetPath)
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := target.Close(); closeErr != nil {
			t.Errorf("close backup target: %v", closeErr)
		}
	})

	backups, err := backup.NewService(backup.Config{
		Jobs:     jobcheck.Checker{},
		Exporter: st, Kinds: st, Registry: st, Blobs: live, Target: target, TargetName: "dir:test",
		Operations: st.DurableOperationsPort(), Version: "test", StateOpener: nil, Owner: "", Lease: 0, Now: nil,
		MaxSQLBytes: 0, Logger: nil,
	})
	if err != nil {
		t.Fatalf("new backup service: %v", err)
	}

	restores, err := backup.NewRestoreService(backup.RestoreConfig{
		Live: st, Operations: st.DurableOperationsPort(), Backups: backups,
		Maintenance: storetest.StateMaintenance{State: st, Hold: false}, Blobs: live, Leases: st, Target: target,
		Key: nil, Installation: "test", AfterApply: nil, Admit: nil, AllowDevBackups: false, AllowDevHint: "",
		Owner: "", Lease: 0, CleanupLockWait: 0, RetryBase: time.Millisecond, Now: nil, Logger: nil,
		AccessSetting:       nil,
		AccessSettingStatic: nil,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	return &quotaBackupRig{store: st, backups: backups, restores: restores}
}

// quotaBackupFence creates the namespace name in s and returns its fence.
func quotaBackupFence(t *testing.T, s *backupStore, name namespace.Name) namespace.Fence {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                name,
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %s: %v", name, err)
	}

	return ns.Fence()
}

// quotaBackupTime is the time of the quota backup cases.
func quotaBackupTime() time.Time {
	return time.Unix(1_790_000_000, 0).UTC()
}

// quotaBackupUsages returns the usage of each check, and fails the test when
// the read fails.
func quotaBackupUsages(
	t *testing.T, s *backupStore, checks []quota.Check, at time.Time,
) []quota.Usage {
	t.Helper()

	got, err := s.QuotaCounter().Usage(t.Context(), checks, at)
	if err != nil {
		t.Fatalf("Usage() = %v", err)
	}

	if len(got) != len(checks) {
		t.Fatalf("Usage() gave %d usages for %d checks", len(got), len(checks))
	}

	return got
}

// TestQuotaBackupRestoresReservationKeys proves that a backup holds each
// reservation before its keys: each key row needs its reservation row. An
// open reservation with two keys restores on PostgreSQL, and the usage after
// the restore equals the usage before the backup. The copy order is the list
// of portableQuotaTables in pkg/backup/portable_quota.go.
func TestQuotaBackupRestoresReservationKeys(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL quota restore suite", envURL)
	}

	targetPath := t.TempDir()

	source := newQuotaBackupRig(t, base, targetPath)
	fence := quotaBackupFence(t, source.store, "payments")
	counter := source.store.QuotaCounter()
	at := quotaBackupTime()

	all := quota.Key{Namespace: "payments", Blueprint: "postgres", Kind: quota.KindUnits, Scope: "namespace"}
	mine := quota.Key{Namespace: "payments", Blueprint: "postgres", Kind: quota.KindUnits, Scope: "requester:alice"}
	reserved := quota.Reservation{
		ID: "req-1/1", Namespace: "payments", State: quota.ReservationOpen,
		Keys: []quota.Key{all, mine}, At: at, EndsAt: at.Add(24 * time.Hour),
	}

	err := counter.InTx(t.Context(), fence, func(tx quota.Tx) error {
		_, reserveErr := tx.Reserve(t.Context(), reserved,
			[]quota.Check{{Key: all, Window: 0, Max: 10}, {Key: mine, Window: 0, Max: 10}})

		return reserveErr
	})
	if err != nil {
		t.Fatalf("Reserve() = %v", err)
	}

	checks := []quota.Check{{Key: all, Window: 0, Max: 0}, {Key: mine, Window: 0, Max: 0}}
	want := []quota.Usage{
		{Key: all, Settled: 0, Reserved: 1, Requests: 0},
		{Key: mine, Settled: 0, Reserved: 1, Requests: 0},
	}
	before := quotaBackupUsages(t, source.store, checks, at)
	if !slices.Equal(want, before) {
		t.Fatalf("Usage before the backup = %+v, want %+v", before, want)
	}

	backupID := backup.ID(ident.NewID("bkp-"))
	if _, err = source.backups.Run(t.Context(), backupID); err != nil {
		t.Fatalf("run backup: %v", err)
	}

	dest := newQuotaBackupRig(t, base, targetPath)
	restoreID := backup.RestoreID(ident.NewID("rst-"))
	req := backup.RestoreRequest{
		BackupID: backupID, PreBackupID: "", CheckOnly: false, Force: false, RequestedBy: "tester",
		KeepAccessTables: false,
	}

	if err = dest.restores.Start(t.Context(), restoreID, req); err != nil {
		t.Fatalf("start restore: %v", err)
	}

	waitRestoreSucceeded(t, dest.restores, restoreID)

	after := quotaBackupUsages(t, dest.store, checks, at)
	if !slices.Equal(before, after) {
		t.Fatalf("Usage after the restore = %+v, want %+v", after, before)
	}
}
