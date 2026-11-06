package servertest_test

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/servertest"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// writeGatedTarget is a backup target that holds the first write after the test
// arms it, until the test opens the gate or the context of the write ends. The
// request that starts a restore only reads the backup, so the first write after
// the arm is a write of the restore in the worker, for example the backup that
// it makes before the swap. So a test holds a restore inside a step of the
// worker.
type writeGatedTarget struct {
	blob.Store

	mu      sync.Mutex
	armed   bool
	held    bool
	entered chan struct{}
	open    chan struct{}
}

// Put holds the first write after arm, then writes through.
func (g *writeGatedTarget) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	g.mu.Lock()
	hold := g.armed && !g.held
	g.held = g.held || hold
	g.mu.Unlock()

	if hold {
		close(g.entered)

		select {
		case <-g.open:
		case <-ctx.Done():
			return fmt.Errorf("hold write: %w", ctx.Err())
		}
	}

	return g.Store.Put(ctx, key, r)
}

// arm makes the next write wait for the gate.
func (g *writeGatedTarget) arm() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.armed = true
}

// TestOperationLeaseReachesRestore proves that the lease period that
// WithOperationLease sets is the lease of the claim of a restore, as it is for
// a backup. The restore waits inside its first read of the backup, and the
// claim shows the expiry that the option gives. The default lease is 30
// seconds, so an expiry minutes ahead can come only from the option.
func TestOperationLeaseReachesRestore(t *testing.T) {
	t.Parallel()

	const lease = 10 * time.Minute

	store, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	target := &writeGatedTarget{
		Store: store, mu: sync.Mutex{}, armed: false, held: false,
		entered: make(chan struct{}), open: make(chan struct{}),
	}

	c := servertest.Start(t, 1, dustpan.WithBackupTarget(target, "test"), dustpan.WithOperationLease(lease))

	id := startBackup(t, c, 0)
	if got := waitBackup(t, c, 0, id, 30*time.Second); got.State != string(backup.StateSucceeded) {
		t.Fatalf("backup ended %q (%s), want succeeded", got.State, got.Error)
	}

	target.arm()
	startRestore(t, c, id)

	select {
	case <-target.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the restore did not reach a write")
	case <-t.Context().Done():
		t.Fatalf("wait for the restore: %v", t.Context().Err())
	}

	defer close(target.open)

	ops, err := c.Operations(t, 0).
		List(t.Context(), durable.Filter{Kind: backup.RestoreOperationKind, States: nil, Limit: 0})
	if err != nil || len(ops) != 1 {
		t.Fatalf("restore operations = %d, err %v, want 1", len(ops), err)
	}

	// The expiry is the claim or the last renewal plus the lease. Half the
	// lease is far above the default lease and far below the option, so a
	// slow machine cannot move the check either way.
	if left := time.Until(ops[0].Claim.ExpiresAt); left < lease/2 {
		t.Fatalf("the claim of the restore expires in %v, want about the lease of %v", left, lease)
	}
}
