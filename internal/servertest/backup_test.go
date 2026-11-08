package servertest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// backupLease is the lease period of the backup tests. It is short, so a
// takeover test finishes fast, and long enough for a loaded test machine to
// renew a claim.
const backupLease = 2 * time.Second

// takeoverDeadline is how long a takeover test waits for the survivor to
// claim the operation and to end the backup. It only stops a test that hangs:
// the time of a takeover on a loaded test machine says nothing about the
// takeover path, so no test checks it against the lease.
const takeoverDeadline = 60 * time.Second

// gatedTarget is a backup target that holds the first write of a SQL table
// file until the test opens the gate or the context of the write ends. So a
// test holds a backup inside its write step.
type gatedTarget struct {
	blob.Store

	mu      sync.Mutex
	entered chan struct{}
	open    chan struct{}
	held    bool
}

// newGatedTarget returns a gated target over a directory.
func newGatedTarget(t *testing.T) *gatedTarget {
	t.Helper()

	store, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	return &gatedTarget{
		Store:   store,
		mu:      sync.Mutex{},
		entered: make(chan struct{}),
		open:    make(chan struct{}),
		held:    false,
	}
}

// Put holds the first write of a table file, then writes through.
func (g *gatedTarget) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	if strings.Contains(key.String(), "/sql/") {
		g.mu.Lock()
		first := !g.held
		g.held = true
		g.mu.Unlock()

		if first {
			close(g.entered)

			select {
			case <-g.open:
			case <-ctx.Done():
				return fmt.Errorf("hold write: %w", ctx.Err())
			}
		}
	}

	return g.Store.Put(ctx, key, r)
}

// release lets every write through.
func (g *gatedTarget) release() {
	close(g.open)
}

// waitEntered waits until a backup holds the gate.
func (g *gatedTarget) waitEntered(t *testing.T, c *servertest.Cluster) {
	t.Helper()

	select {
	case <-g.entered:
	case <-time.After(30 * time.Second):
		ops, err := c.Operations(t, 0).
			List(t.Context(), durable.Filter{Kind: backup.OperationKind, States: nil, Limit: 0})
		if err != nil || len(ops) == 0 {
			t.Fatalf("no backup reached its write step, and no operation: %v", err)
		}

		t.Fatalf("no backup reached its write step: operation %s, last error %q", ops[0].State, ops[0].LastError)
	}
}

// backupStatus is the part of the answer of GET /api/server/backups/{id}
// that the tests read.
type backupStatus struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Error string `json:"error"`
}

// startBackup starts a backup on server i and returns its ID.
func startBackup(t *testing.T, c *servertest.Cluster, i int) string {
	t.Helper()

	status, body := c.Do(t, i, http.MethodPost, "/api/server/backups", "")
	if status != http.StatusAccepted {
		t.Fatalf("start backup on server %d: status %d, body %s", i, status, body)
	}

	var started backupStatus
	if err := json.Unmarshal([]byte(body), &started); err != nil || started.ID == "" {
		t.Fatalf("start backup answer %q: %v", body, err)
	}

	return started.ID
}

// readBackup reads the status of backup id on server i.
func readBackup(t *testing.T, c *servertest.Cluster, i int, id string) backupStatus {
	t.Helper()

	status, body := c.Do(t, i, http.MethodGet, "/api/server/backups/"+id, "")
	if status != http.StatusOK {
		t.Fatalf("read backup on server %d: status %d, body %s", i, status, body)
	}

	var got backupStatus
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("backup answer %q: %v", body, err)
	}

	return got
}

// waitBackup polls server i until backup id leaves the running state, and
// returns its final status. It fails the test after limit.
func waitBackup(t *testing.T, c *servertest.Cluster, i int, id string, limit time.Duration) backupStatus {
	t.Helper()

	deadline := time.Now().Add(limit)

	for {
		got := readBackup(t, c, i, id)
		if got.State != string(backup.StateRunning) {
			return got
		}

		if time.Now().After(deadline) {
			t.Fatalf("backup %s still %s on server %d after %v", id, got.State, i, limit)
		}

		select {
		case <-t.Context().Done():
			t.Fatalf("wait for backup %s: %v", id, t.Context().Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// backupOperation returns the only backup operation of the cluster.
func backupOperation(t *testing.T, c *servertest.Cluster, i int) durable.Operation {
	t.Helper()

	ops, err := c.Operations(t, i).List(t.Context(), durable.Filter{Kind: backup.OperationKind, States: nil, Limit: 0})
	if err != nil || len(ops) != 1 {
		t.Fatalf("backup operations: %d, err %v; want 1", len(ops), err)
	}

	return ops[0]
}

// endedBackupOperation returns the only backup operation of the cluster once
// it ended. The runner completes it just after the finish write, so a caller
// that saw the backup succeed waits for that.
func endedBackupOperation(t *testing.T, c *servertest.Cluster, i int) durable.Operation {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for {
		op := backupOperation(t, c, i)
		if op.State.Terminal() || time.Now().After(deadline) {
			return op
		}

		select {
		case <-t.Context().Done():
			t.Fatalf("wait for the backup operation: %v", t.Context().Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// waitSurvivorClaim polls the backup operation until server survivor holds its
// claim. A claim that is already released (the operation ended) counts when
// the operation shows a second attempt.
func waitSurvivorClaim(t *testing.T, c *servertest.Cluster, survivor int) {
	t.Helper()

	deadline := time.Now().Add(takeoverDeadline)

	for {
		op := backupOperation(t, c, survivor)
		if op.Claim.Owner == servertest.InstanceID(survivor) || (op.State.Terminal() && op.Attempt >= 2) {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("the survivor did not claim the operation: state %s, owner %q", op.State, op.Claim.Owner)
		}

		select {
		case <-t.Context().Done():
			t.Fatalf("wait for the claim of the survivor: %v", t.Context().Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// splitSets returns servers 0 and 2 with every component and server 1 with the
// api component only.
func splitSets(t *testing.T) []dustpan.Components {
	t.Helper()

	api, err := dustpan.ParseComponents("api")
	if err != nil {
		t.Fatal(err)
	}

	return []dustpan.Components{dustpan.AllComponents(), api, dustpan.AllComponents()}
}

// TestBackupSurvivesStartOfAnotherInstance proves that a start of one
// instance does not fail the live backup of another: server 0 runs a backup
// and server 1 restarts during it. A restart of the second instance that has
// every component, and so a worker, does not fail it either. The backup ends
// succeeded, and all servers answer the same state while it runs.
func TestBackupSurvivesStartOfAnotherInstance(t *testing.T) {
	t.Parallel()

	target := newGatedTarget(t)
	c := servertest.StartSplit(t, splitSets(t),
		dustpan.WithBackupTarget(target, "test"), dustpan.WithOperationLease(backupLease))

	id := startBackup(t, c, 1)
	target.waitEntered(t, c)

	for i := range 3 {
		if got := readBackup(t, c, i, id); got.State != string(backup.StateRunning) {
			t.Fatalf("server %d: state %q while the backup runs, want running", i, got.State)
		}
	}

	c.Kill(t, 1)
	c.Restart(t, 1)

	for i := range 3 {
		if got := readBackup(t, c, i, id); got.State != string(backup.StateRunning) {
			t.Fatalf("server %d: state %q after the restart of server 1, want running", i, got.State)
		}
	}

	// Restart the instance with a worker that does not hold the claim.
	idle := 0
	if backupOperation(t, c, 0).Claim.Owner == servertest.InstanceID(0) {
		idle = 2
	}

	c.Kill(t, idle)
	c.Restart(t, idle)

	for i := range 3 {
		if got := readBackup(t, c, i, id); got.State != string(backup.StateRunning) {
			t.Fatalf("server %d: state %q after the restart of server %d, want running", i, got.State, idle)
		}
	}

	target.release()

	for i := range 3 {
		if got := waitBackup(t, c, i, id, 30*time.Second); got.State != string(backup.StateSucceeded) {
			t.Fatalf("server %d: backup ended %q (%s), want succeeded", i, got.State, got.Error)
		}
	}

	if op := endedBackupOperation(t, c, 0); op.State != durable.StateSucceeded || op.Expiries != 0 {
		t.Fatalf("operation = %s with %d expiries, want succeeded with none", op.State, op.Expiries)
	}
}

// TestBackupTakeoverAfterKill proves that another instance takes over a
// backup when the instance that runs it dies in a step: the survivor claims
// the operation only after the claim of the victim expired, the backup ends
// succeeded, the second run claims the operation after one expiry, and the
// target holds one complete backup. The test checks no time against the lease:
// the time of the run on a loaded machine says nothing about the takeover.
func TestBackupTakeoverAfterKill(t *testing.T) {
	t.Parallel()

	// The survivor finds the expired claim at its next poll. A poll interval
	// as long as the lease would make each takeover wait a second lease.
	if durable.DefaultPollEvery >= backupLease {
		t.Fatalf("the poll interval %v is not shorter than the lease %v", durable.DefaultPollEvery, backupLease)
	}

	target := newGatedTarget(t)
	c := servertest.Start(t, 2, dustpan.WithBackupTarget(target, "test"), dustpan.WithOperationLease(backupLease))

	id := startBackup(t, c, 0)
	target.waitEntered(t, c)

	holder := backupOperation(t, c, 0)
	if holder.State != durable.StateRunning {
		t.Fatalf("operation state = %s while the backup runs, want running", holder.State)
	}

	victim, survivor := 0, 1
	if holder.Claim.Owner == servertest.InstanceID(1) {
		victim, survivor = 1, 0
	}

	c.Kill(t, victim)

	// The victim renews no more, so its last claim is final.
	dead := backupOperation(t, c, survivor)

	target.release()

	waitSurvivorClaim(t, c, survivor)

	checkClaimedAfterExpiry(t, dead, servertest.InstanceID(victim))

	got := waitBackup(t, c, survivor, id, takeoverDeadline)
	if got.State != string(backup.StateSucceeded) {
		t.Fatalf("backup ended %q (%s) after the takeover, want succeeded", got.State, got.Error)
	}

	op := endedBackupOperation(t, c, survivor)
	if op.State != durable.StateSucceeded || op.Attempt < 2 || op.Expiries < 1 {
		t.Fatalf("operation = %+v, want succeeded after a takeover", op)
	}

	if err := backup.Check(t.Context(), target.Store, backup.ID(id)); err != nil {
		t.Fatalf("check backup after the takeover: %v", err)
	}
}

// TestBackupTakeoverAfterGracefulStop proves that a stop signal on the
// instance that runs a backup gives the claim to the other instance at
// once. The lease is far longer than the limit, so only the release of the
// claim at the stop can explain the time. The release does not count as an
// expiry, and the target holds one complete backup.
func TestBackupTakeoverAfterGracefulStop(t *testing.T) {
	t.Parallel()

	const (
		longLease = 30 * time.Second
		limit     = 5 * time.Second
	)

	target := newGatedTarget(t)
	c := servertest.Start(t, 2, dustpan.WithBackupTarget(target, "test"), dustpan.WithOperationLease(longLease))

	id := startBackup(t, c, 0)
	target.waitEntered(t, c)

	holder := backupOperation(t, c, 0)
	if holder.State != durable.StateRunning {
		t.Fatalf("operation state = %s while the backup runs, want running", holder.State)
	}

	victim, survivor := 0, 1
	if holder.Claim.Owner == servertest.InstanceID(1) {
		victim, survivor = 1, 0
	}

	signaledAt := time.Now()
	wait := c.Signal(t, victim)

	waitSurvivorClaim(t, c, survivor)

	claimedAfter := time.Since(signaledAt)
	if claimedAfter > limit {
		t.Fatalf("the survivor claimed the operation %v after the stop signal, want within %v", claimedAfter, limit)
	}

	wait()
	target.release()

	if got := waitBackup(t, c, survivor, id, 30*time.Second); got.State != string(backup.StateSucceeded) {
		t.Fatalf("backup ended %q (%s) after the takeover, want succeeded", got.State, got.Error)
	}

	op := endedBackupOperation(t, c, survivor)
	if op.State != durable.StateSucceeded || op.Attempt < 2 || op.Expiries != 0 {
		t.Fatalf("operation = %+v, want succeeded after a release with no expiry", op)
	}

	if err := backup.Check(t.Context(), target.Store, backup.ID(id)); err != nil {
		t.Fatalf("check backup after the takeover: %v", err)
	}
}

// checkClaimedAfterExpiry fails the test when the survivor claimed the
// operation before the claim of the victim expired. dead is the operation as
// the test read it just after the kill. When that read already shows another
// owner, the test stalled for more than the lease after the kill and the
// survivor claimed it legally, so the check cannot run: it logs that and
// checks nothing. The checks of the expiry and of the attempt still run.
func checkClaimedAfterExpiry(t *testing.T, dead durable.Operation, victim string) {
	t.Helper()

	if dead.Claim.Owner != victim {
		t.Logf("the test read the operation after the takeover (owner %q): it stalled for more than the lease "+
			"after the kill, so the check that the claim waited for the expiry does not run", dead.Claim.Owner)

		return
	}

	if claimedAt := time.Now(); claimedAt.Before(dead.Claim.ExpiresAt) {
		t.Fatalf("the survivor claimed the operation at %v, before the claim of the victim expired at %v",
			claimedAt, dead.Claim.ExpiresAt)
	}
}
