package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// recoveryLease is the lease period of the recovery test.
const recoveryLease = 300 * time.Millisecond

// holdTarget is a backup target that holds the first write of a SQL table
// file until the context of the write ends, as a process that dies in the
// write step holds it.
type holdTarget struct {
	blob.Store

	mu      sync.Mutex
	held    bool
	entered chan struct{}
}

// Put holds the first write of a table file, then writes through.
func (h *holdTarget) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	if strings.Contains(key.String(), "/sql/") {
		h.mu.Lock()
		first := !h.held
		h.held = true
		h.mu.Unlock()

		if first {
			close(h.entered)
			<-ctx.Done()

			return fmt.Errorf("hold write: %w", ctx.Err())
		}
	}

	return h.Store.Put(ctx, key, r)
}

// backupService builds a backup service over store and the two directories.
func backupService(
	t *testing.T, store *sqlite.Store, ops durable.Store, blobs, target blob.Store,
) *backup.Service {
	t.Helper()

	svc, err := backup.NewService(backup.Config{
		Exporter:    store,
		Kinds:       store,
		Registry:    store,
		Blobs:       blobs,
		Target:      target,
		TargetName:  "test",
		Operations:  ops,
		Owner:       "worker",
		Lease:       recoveryLease,
		StateOpener: nil,
		Now:         nil,
		Version:     "",
		MaxSQLBytes: 0,
		Logger:      nil,
	})
	if err != nil {
		t.Fatalf("backup service: %v", err)
	}

	return svc
}

// crashedBackup starts a backup on a store at path, runs its worker until the
// write step holds, and then kills the process: the database handle goes
// away before the context ends, so the runner cannot release its claim.
func crashedBackup(t *testing.T, path string, dirs recoveryDirs, id backup.ID) {
	t.Helper()

	ctx := t.Context()

	first, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	if _, claimErr := first.ClaimKind(ctx, installation.KindProduction); claimErr != nil {
		t.Fatalf("claim kind: %v", claimErr)
	}

	held := &holdTarget{Store: dirs.target, mu: sync.Mutex{}, held: false, entered: make(chan struct{})}
	dead := &crashOps{Store: first.DurableOperationsPort(), crashed: atomic.Bool{}}
	svc := backupService(t, first, dead, dirs.blobs, held)

	if startErr := svc.Start(ctx, id); startErr != nil {
		t.Fatalf("start backup: %v", startErr)
	}

	workCtx, crash := context.WithCancel(ctx)

	var worker sync.WaitGroup

	worker.Go(func() { svc.Work(workCtx) })

	select {
	case <-held.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the backup did not reach its write step")
	}

	dead.crashed.Store(true)
	crash()
	worker.Wait()

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("close store: %v", closeErr)
	}
}

// errCrashed is the answer of a store after the crash of its process.
var errCrashed = errors.New("the process crashed")

// crashOps is a durable store whose writes that end an attempt fail once the
// process crashed, as they do when the process is gone: the claim stays
// until its lease ends.
type crashOps struct {
	durable.Store

	crashed atomic.Bool
}

// Release fails after the crash.
func (c *crashOps) Release(ctx context.Context, claim durable.Claim) error {
	if c.crashed.Load() {
		return errCrashed
	}

	return c.Store.Release(ctx, claim)
}

// Fail fails after the crash.
func (c *crashOps) Fail(ctx context.Context, claim durable.Claim, f durable.Failure) error {
	if c.crashed.Load() {
		return errCrashed
	}

	return c.Store.Fail(ctx, claim, f)
}

// Complete fails after the crash.
func (c *crashOps) Complete(ctx context.Context, claim durable.Claim) error {
	if c.crashed.Load() {
		return errCrashed
	}

	return c.Store.Complete(ctx, claim)
}

// recoveryDirs holds the live blob store and the backup target of the test.
type recoveryDirs struct {
	blobs  *fs.Store
	target *fs.Store
}

// TestBackupRecoversAfterCrash proves that one instance on SQLite recovers a
// backup after a crash: the process dies in the write step with its claim
// unreleased, and the next process finishes the same backup after the lease
// ends, with one complete backup in the target.
func TestBackupRecoversAfterCrash(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "dustpan.db")
	id := backup.ID("bkp-crash")

	blobs, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blobs: %v", err)
	}

	target, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open target: %v", err)
	}

	dirs := recoveryDirs{blobs: blobs, target: target}
	crashedBackup(t, path, dirs, id)

	second, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := second.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	if row, getErr := second.GetBackup(ctx, id); getErr != nil || row.State != backup.StateRunning {
		t.Fatalf("backup after the crash = %+v, %v; want running", row, getErr)
	}

	again := backupService(t, second, second.DurableOperationsPort(), blobs, target)
	recoverCtx, stop := context.WithCancel(ctx)

	var worker sync.WaitGroup

	worker.Go(func() { again.Work(recoverCtx) })

	t.Cleanup(func() {
		stop()
		worker.Wait()
	})

	checkRecovered(t, second, target, id)
}

// checkRecovered checks that the backup ended succeeded after the second
// attempt, and that the target holds one complete backup.
func checkRecovered(t *testing.T, second *sqlite.Store, target blob.Store, id backup.ID) {
	t.Helper()

	ctx := t.Context()

	if row := waitBackupEnd(t, second, id); row.State != backup.StateSucceeded {
		t.Fatalf("backup after the recovery = %+v, want succeeded", row)
	}

	// The runner completes the operation just after the finish write.
	opID := durableID(t, second, id)
	deadline := time.Now().Add(10 * time.Second)

	op, err := second.DurableOperationsPort().Get(ctx, opID)
	for err == nil && !op.State.Terminal() && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
		case <-time.After(20 * time.Millisecond):
		}

		op, err = second.DurableOperationsPort().Get(ctx, opID)
	}

	if err != nil || op.State != durable.StateSucceeded || op.Attempt != 2 || op.Expiries != 1 {
		t.Fatalf("operation = %+v, %v; want succeeded after attempt 2 with 1 expiry", op, err)
	}

	if checkErr := backup.Check(ctx, target, id); checkErr != nil {
		t.Fatalf("check recovered backup: %v", checkErr)
	}
}

// waitBackupEnd polls the row of backup id until it leaves running.
func waitBackupEnd(t *testing.T, store *sqlite.Store, id backup.ID) backup.Record {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for {
		row, err := store.GetBackup(t.Context(), id)
		if err != nil {
			t.Fatalf("get backup: %v", err)
		}

		if row.State != backup.StateRunning {
			return row
		}

		if time.Now().After(deadline) {
			t.Fatalf("backup %s still running after 30s", id)
		}

		select {
		case <-t.Context().Done():
			t.Fatalf("backup %s: %v", id, t.Context().Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// durableID returns the ID of the operation that runs backup id.
func durableID(t *testing.T, store *sqlite.Store, id backup.ID) durable.ID {
	t.Helper()

	ops, err := store.DurableOperationsPort().
		List(t.Context(), durable.Filter{Kind: backup.OperationKind, States: nil, Limit: 0})
	if err != nil {
		t.Fatalf("list operations: %v", err)
	}

	for i := range ops {
		if ops[i].Key == backup.OperationKey(id) {
			return ops[i].ID
		}
	}

	t.Fatalf("no operation for backup %s", id)

	return ""
}
