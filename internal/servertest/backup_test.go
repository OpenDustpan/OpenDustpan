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

// splitSets returns server 0 with every component and server 1 with the api
// component only.
func splitSets(t *testing.T) []dustpan.Components {
	t.Helper()

	api, err := dustpan.ParseComponents("api")
	if err != nil {
		t.Fatal(err)
	}

	return []dustpan.Components{dustpan.AllComponents(), api}
}

// TestBackupSurvivesStartOfAnotherInstance proves that a start of one
// instance does not fail the live backup of another: server 0 runs a backup
// and server 1 restarts during it. The backup ends succeeded, and both
// servers answer the same state while it runs.
func TestBackupSurvivesStartOfAnotherInstance(t *testing.T) {
	t.Parallel()

	target := newGatedTarget(t)
	c := servertest.StartSplit(t, splitSets(t),
		dustpan.WithBackupTarget(target, "test"), dustpan.WithOperationLease(backupLease))

	id := startBackup(t, c, 1)
	target.waitEntered(t, c)

	for i := range 2 {
		if got := readBackup(t, c, i, id); got.State != string(backup.StateRunning) {
			t.Fatalf("server %d: state %q while the backup runs, want running", i, got.State)
		}
	}

	c.Kill(t, 1)
	c.Restart(t, 1)

	for i := range 2 {
		if got := readBackup(t, c, i, id); got.State != string(backup.StateRunning) {
			t.Fatalf("server %d: state %q after the restart of server 1, want running", i, got.State)
		}
	}

	target.release()

	for i := range 2 {
		if got := waitBackup(t, c, i, id, 30*time.Second); got.State != string(backup.StateSucceeded) {
			t.Fatalf("server %d: backup ended %q (%s), want succeeded", i, got.State, got.Error)
		}
	}

	if op := backupOperation(t, c, 0); op.State != durable.StateSucceeded || op.Attempt != 1 {
		t.Fatalf("operation = %s after attempt %d, want succeeded after attempt 1", op.State, op.Attempt)
	}
}

// TestBackupTakeoverAfterKill proves that another instance takes over a
// backup when the instance that runs it dies in a step: the backup ends
// succeeded within a few lease periods, the second run claims the operation
// once, and the target holds one complete backup.
func TestBackupTakeoverAfterKill(t *testing.T) {
	t.Parallel()

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

	killedAt := time.Now()

	c.Kill(t, victim)
	target.release()

	// The claim expires one lease period after its last renewal. The
	// survivor polls for work each second, and runs the backup at once.
	got := waitBackup(t, c, survivor, id, 4*backupLease+10*time.Second)
	if got.State != string(backup.StateSucceeded) {
		t.Fatalf("backup ended %q (%s) after the takeover, want succeeded", got.State, got.Error)
	}

	if took := time.Since(killedAt); took > 3*backupLease+5*time.Second {
		t.Fatalf("takeover took %v, want a few lease periods of %v", took, backupLease)
	}

	op := backupOperation(t, c, survivor)
	if op.State != durable.StateSucceeded || op.Attempt != 2 || op.Expiries != 1 {
		t.Fatalf("operation = %+v, want succeeded after attempt 2 with 1 expiry", op)
	}

	if err := backup.Check(t.Context(), target.Store, backup.ID(id)); err != nil {
		t.Fatalf("check backup after the takeover: %v", err)
	}
}
