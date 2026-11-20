package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// createLockKeySQL is the key of the advisory lock that a create of a
// workspace and the put of a lock on a namespace share, as the queries
// LockNamespaceCreatesShared and LockNamespaceCreatesExclusive build it. The
// tests take the lock by hand, in a transaction of their own, to hold a create
// or a put in flight.
const createLockKeySQL = "hashtextextended('namespace-creates:' || current_schema() || ':' || $1::text, 0)"

// blockedFor is how long a call must stay blocked for the test to count it as
// waiting for the advisory lock.
const blockedFor = 400 * time.Millisecond

// raceRounds is the number of rounds of the race test.
const raceRounds = 60

// TestPutOfANamespaceLockWaitsForACreate proves that the put of a manual lock
// on a namespace waits for a create of a workspace that runs: it takes the
// exclusive form of the advisory lock that the create holds in the shared form.
// Without the lock the put does not wait, and the create can then commit under
// a lock that it did not read.
func TestPutOfANamespaceLockWaitsForACreate(t *testing.T) {
	t.Parallel()

	s, ns := createLockSetup(t, "acme")
	hold := holdCreateLock(t, s, ns.Name, "pg_advisory_xact_lock_shared")

	put := make(chan error, 1)

	go func() {
		put <- s.PutLock(t.Context(), namespaceLock(ns.Name, time.Now()), ns.Fence(), createLockEntry(ns.Name, "unit:lock"))
	}()

	assertCreateBlocked(t, put, "the put of a namespace lock")
	hold.release(t)
	assertCreateDone(t, put, "the put of a namespace lock after the create ends")
}

// TestCreateWaitsForThePutOfANamespaceLock proves that a create of a workspace
// waits for a put of a manual lock on its namespace that runs: it takes the
// shared form of the lock that the put holds in the exclusive form, and reads
// the lock after the put commits. Without the lock the create does not wait,
// and it can read the namespace before the lock exists and commit after it.
func TestCreateWaitsForThePutOfANamespaceLock(t *testing.T) {
	t.Parallel()

	s, ns := createLockSetup(t, "acme")
	hold := holdCreateLock(t, s, ns.Name, "pg_advisory_xact_lock")

	created := make(chan error, 1)

	go func() {
		created <- s.CreateWorkspace(t.Context(), newCreateLockWorkspace(ns.Name, "web"), ns.Fence())
	}()

	assertCreateBlocked(t, created, "the create of a workspace")
	hold.release(t)
	assertCreateDone(t, created, "the create of a workspace after the put ends")
}

// TestCreateAndNamespaceLockRaceLeaveNoWorkspaceUnderALock runs a create of a
// workspace and the put of a lock on its namespace at the same time, many
// times. When both succeed, the create has to be first: a workspace written
// after the lock is a workspace under a lock. The test compares the
// transaction IDs of the two rows, which the database gives in the order of the
// first write of each transaction. The advisory lock makes the later
// transaction wait for the earlier one before its first write.
func TestCreateAndNamespaceLockRaceLeaveNoWorkspaceUnderALock(t *testing.T) {
	t.Parallel()

	s, _ := createLockSetup(t, "base")

	for round := range raceRounds {
		ns := namespace.Namespace{
			ID:                  namespace.NewID(),
			CreatedAt:           time.Now(),
			Name:                namespace.Name(fmt.Sprintf("race-%d", round)),
			PrimaryInstallation: "eu-west",
			FailoverVersion:     1,
			Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
		}
		if err := s.CreateNamespace(t.Context(), ns); err != nil {
			t.Fatalf("create namespace: %v", err)
		}

		raceOneRound(t, s, ns)
	}
}

// raceOneRound starts a create and a put together on ns and checks the order
// of their writes.
func raceOneRound(t *testing.T, s *Store, ns namespace.Namespace) {
	t.Helper()

	var (
		wg         sync.WaitGroup
		createErr  error
		putErr     error
		startGroup = make(chan struct{})
	)

	wg.Go(func() {
		<-startGroup

		createErr = s.CreateWorkspace(t.Context(), newCreateLockWorkspace(ns.Name, "web"), ns.Fence())
	})
	wg.Go(func() {
		<-startGroup

		putErr = s.PutLock(
			t.Context(),
			namespaceLock(ns.Name, time.Now()),
			ns.Fence(),
			createLockEntry(ns.Name, "unit:lock"),
		)
	})

	close(startGroup)
	wg.Wait()

	if putErr != nil {
		t.Fatalf("put of the lock in %s: %v", ns.Name, putErr)
	}

	if createErr != nil {
		if !errors.Is(createErr, workspace.ErrLocked) {
			t.Fatalf("create in %s = %v, want nil or workspace.ErrLocked", ns.Name, createErr)
		}

		return
	}

	var workspaceXID, lockXID int64

	row := s.pool.QueryRow(
		t.Context(),
		"SELECT xmin::text::bigint FROM workspaces WHERE namespace = $1",
		ns.Name.String(),
	)
	if err := row.Scan(&workspaceXID); err != nil {
		t.Fatalf("read the transaction of the workspace in %s: %v", ns.Name, err)
	}

	row = s.pool.QueryRow(
		t.Context(),
		"SELECT xmin::text::bigint FROM manual_locks WHERE namespace = $1",
		ns.Name.String(),
	)
	if err := row.Scan(&lockXID); err != nil {
		t.Fatalf("read the transaction of the lock in %s: %v", ns.Name, err)
	}

	if workspaceXID > lockXID {
		t.Fatalf("%s: the workspace was written after the lock (transaction %d, lock %d) and the create succeeded",
			ns.Name, workspaceXID, lockXID)
	}
}

// createLockSetup opens a store on a schema of its own and makes the
// namespace name in it.
func createLockSetup(t *testing.T, name string) (*Store, namespace.Namespace) {
	t.Helper()

	s, err := Open(t.Context(), membershipURL(t))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Now(), Name: namespace.Name(name), PrimaryInstallation: "eu-west",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return s, ns
}

// createLockHold is a transaction that holds the advisory lock of the creates
// of a namespace.
type createLockHold struct {
	tx pgx.Tx
}

// holdCreateLock takes the advisory lock with the function fn, which is
// pg_advisory_xact_lock_shared or pg_advisory_xact_lock, in a transaction that
// stays open until release.
func holdCreateLock(t *testing.T, s *Store, ns namespace.Name, fn string) createLockHold {
	t.Helper()

	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	hold := createLockHold{tx: tx}

	t.Cleanup(func() {
		// A release has ended the transaction already, which is not an error.
		rollbackErr := tx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Errorf("roll back the hold: %v", rollbackErr)
		}
	})

	if _, err := tx.Exec(t.Context(), "SELECT "+fn+"("+createLockKeySQL+")", ns.String()); err != nil {
		t.Fatalf("take the advisory lock: %v", err)
	}

	return hold
}

// release ends the transaction and so frees the lock.
func (h createLockHold) release(t *testing.T) {
	t.Helper()

	if err := h.tx.Rollback(t.Context()); err != nil {
		t.Fatalf("release the advisory lock: %v", err)
	}
}

// assertCreateBlocked fails the test when the call answers before blockedFor.
func assertCreateBlocked(t *testing.T, done <-chan error, what string) {
	t.Helper()

	select {
	case err := <-done:
		t.Fatalf("%s did not wait for the advisory lock (it answered %v)", what, err)
	case <-time.After(blockedFor):
	}
}

// assertCreateDone fails the test unless the call answers without an error soon.
func assertCreateDone(t *testing.T, done <-chan error, what string) {
	t.Helper()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not finish", what)
	}
}

// namespaceLock returns a manual lock on the namespace ns.
func namespaceLock(ns namespace.Name, since time.Time) manuallock.Lock {
	return manuallock.Lock{Since: since, Target: manuallock.NamespaceTarget(ns), Holder: "alice", Reason: "freeze"}
}

// newCreateLockWorkspace returns a workspace to create in ns.
func newCreateLockWorkspace(ns namespace.Name, name string) workspace.Workspace {
	now := time.Now()

	return workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   now,
		UpdatedAt:   now,
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          workspace.ID(ident.NewID("ws-")),
		Ref:         workspace.Ref{Namespace: ns, Name: workspace.Name(name)},
		Revision:    1,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
}

// createLockEntry returns the audit entry of the put of a lock in ns.
func createLockEntry(ns namespace.Name, action string) audit.Entry {
	return membershipEntry(time.Now(), ns, action)
}
