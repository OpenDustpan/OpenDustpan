package backup_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
)

// errLeasesRead is the error of a failed read of the live blob delete leases.
var errLeasesRead = errors.New("the blob delete leases are unavailable")

// afterApplyKey is one blob that the manifest of the fixture names. A deleter
// that read the guard of the blob deletes before the restore started can
// remove it after the copy-blobs step.
const afterApplyKey = "namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws1/sv1/a1"

// leaseClock is the clock of the leases of the blob deletes, which a test
// moves. It is safe for concurrent use.
type leaseClock struct {
	mu sync.Mutex
	t  time.Time
}

// newLeaseClock returns a clock at a fixed time.
func newLeaseClock() *leaseClock {
	return &leaseClock{mu: sync.Mutex{}, t: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)}
}

// Now reads the clock.
func (c *leaseClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.t
}

// advance moves the clock by d.
func (c *leaseClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.t = c.t.Add(d)
}

// failingLeases is a lease port whose read of the live leases fails. Every
// other method is the one of the embedded port.
type failingLeases struct {
	blobguard.Leases

	err error
}

// LiveLeases implements blobguard.Leases with the injected failure.
func (f failingLeases) LiveLeases(context.Context) (int, error) { return 0, f.err }

// removeHook is an after-apply hook that deletes one blob of the live store,
// as a deleter that read the guard of the blob deletes before the restore
// started does. It counts its calls.
type removeHook struct {
	blobs *memStore
	key   blob.Key
	calls int
}

// newRemoveHook returns a hook for the blob at key.
func newRemoveHook(t *testing.T, key string) *removeHook {
	t.Helper()

	return &removeHook{blobs: nil, key: mustKey(t, key), calls: 0}
}

// run deletes the blob.
func (h *removeHook) run(ctx context.Context) error {
	h.calls++

	return h.blobs.Delete(ctx, h.key)
}

// takeDeleterLease takes the lease of holder in leases, as a deleter does
// before a delete.
func takeDeleterLease(t *testing.T, leases *blobguardtest.Leases, holder string, ttl time.Duration) {
	t.Helper()

	if _, held, err := leases.TakeLease(t.Context(), holder, ttl); err != nil || held {
		t.Fatalf("take the lease of %s: held = %v, err = %v", holder, held, err)
	}
}

// hookSetup builds the fixture with leases as the lease port of the restore
// and removed as its after-apply hook.
func hookSetup(t *testing.T, leases blobguard.Leases, removed *removeHook) *restoreServiceSetup {
	t.Helper()

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) {
		cfg.Leases = leases
		cfg.AfterApply = removed.run
	})
	removed.blobs = setup.blobs

	return setup
}

// startPlainRestore starts a full restore of the backup of the fixture.
func startPlainRestore(t *testing.T, setup *restoreServiceSetup) backup.RestoreID {
	t.Helper()

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start restore: %v", err)
	}

	return id
}

// restoreRowState returns the state of the row of restore id.
func restoreRowState(t *testing.T, setup *restoreServiceSetup, id backup.RestoreID) backup.RestoreState {
	t.Helper()

	row, err := setup.regs.GetRestore(t.Context(), id)
	if err != nil {
		t.Fatalf("get restore: %v", err)
	}

	return row.State
}

// requireLiveBlob fails the test unless the live blob of the hook is stored.
func requireLiveBlob(t *testing.T, setup *restoreServiceSetup, when string) {
	t.Helper()

	if !setup.blobs.has(afterApplyKey) {
		t.Fatalf("the live blob is not stored %s", when)
	}
}

// requireCheckRan fails the test unless the after-apply hook ran and the blob
// that it removed is stored again.
func requireCheckRan(t *testing.T, setup *restoreServiceSetup, removed *removeHook) {
	t.Helper()

	if removed.calls == 0 {
		t.Fatal("the after-apply hook never ran, want the check of the named blobs")
	}

	requireLiveBlob(t, setup, "after the check, want the blob copied again")
}

// TestNewRestoreServiceNeedsTheBlobDeleteLeases proves that the constructor
// refuses a config without the lease port: a restore without the port cannot
// wait for the blob deletes, and it fails closed.
func TestNewRestoreServiceNeedsTheBlobDeleteLeases(t *testing.T) {
	t.Parallel()

	var cfg backup.RestoreConfig

	// The fixture builds a config with every port; the test takes the lease
	// port away and builds the service itself.
	_ = newRestoreServiceSetupWith(t, func(c *backup.RestoreConfig) { cfg = *c })

	cfg.Leases = nil

	service, err := backup.NewRestoreService(cfg)
	if service != nil || !errors.Is(err, backup.ErrMissingDependency) ||
		!strings.Contains(err.Error(), "blob delete leases") {
		t.Fatalf("new restore service without the lease port = %v, %v, want ErrMissingDependency naming the port",
			service, err)
	}
}

// TestAfterApplyWaitsForTheBlobDeleteLeases proves that the after-apply step
// waits for zero live leases of the blob deleters: with a live lease the step
// defers, runs no check, and the restore stays applying; after the release the
// step runs the check, finds the blob that the deleter removed, and copies it
// again.
func TestAfterApplyWaitsForTheBlobDeleteLeases(t *testing.T) {
	t.Parallel()

	clk := newLeaseClock()
	leases := blobguardtest.NewLeases(clk.Now)

	takeDeleterLease(t, leases, "dlt-test", time.Hour)

	removed := newRemoveHook(t, afterApplyKey)
	setup := hookSetup(t, leases, removed)

	id := startPlainRestore(t, setup)

	setup.regs.runWorker(t.Context())

	if !errors.Is(setup.regs.runErr, durable.ErrDeferred) {
		t.Fatalf("the run with a live lease = %v, want a deferral", setup.regs.runErr)
	}

	if state := restoreRowState(t, setup, id); state != backup.RestoreApplying {
		t.Fatalf("restore state with a live lease = %q, want applying", state)
	}

	if removed.calls != 0 {
		t.Fatalf("the after-apply hook ran %d times with a live lease, want none", removed.calls)
	}

	requireLiveBlob(t, setup, "while the restore waits")

	if err := leases.ReleaseLease(t.Context(), "dlt-test"); err != nil {
		t.Fatalf("release the lease: %v", err)
	}

	if row := waitRestoreEnd(t, setup.regs, id); row.State != backup.RestoreSucceeded {
		t.Fatalf("restore state after the release = %q, want succeeded", row.State)
	}

	requireCheckRan(t, setup, removed)
}

// TestAfterApplyRunsWhenTheLeaseEnded proves that a lease which ended on the
// clock does not hold the after-apply step: the step runs the check, and the
// restore finishes, with no release of the lease.
func TestAfterApplyRunsWhenTheLeaseEnded(t *testing.T) {
	t.Parallel()

	clk := newLeaseClock()
	leases := blobguardtest.NewLeases(clk.Now)

	takeDeleterLease(t, leases, "dlt-old", time.Minute)
	clk.advance(2 * time.Minute)

	removed := newRemoveHook(t, afterApplyKey)
	setup := hookSetup(t, leases, removed)

	id := startPlainRestore(t, setup)

	if row := waitRestoreEnd(t, setup.regs, id); row.State != backup.RestoreSucceeded {
		t.Fatalf("restore state with a lease that ended = %q, want succeeded", row.State)
	}

	requireCheckRan(t, setup, removed)
}

// TestAfterApplyFailsClosedOnAFailedLeaseRead proves that a failed read of the
// live leases fails the step closed: the restore fails, and the step runs
// neither the after-apply hook nor the check.
func TestAfterApplyFailsClosedOnAFailedLeaseRead(t *testing.T) {
	t.Parallel()

	removed := newRemoveHook(t, afterApplyKey)
	setup := hookSetup(t, failingLeases{Leases: blobguardtest.None{}, err: errLeasesRead}, removed)

	id := startPlainRestore(t, setup)

	if row := waitRestoreEnd(t, setup.regs, id); row.State != backup.RestoreFailed {
		t.Fatalf("restore state with a failed lease read = %q, want failed", row.State)
	}

	if removed.calls != 0 {
		t.Fatalf("the after-apply hook ran %d times with a failed lease read, want none", removed.calls)
	}

	requireLiveBlob(t, setup, "after a failed lease read, want it kept")
}
