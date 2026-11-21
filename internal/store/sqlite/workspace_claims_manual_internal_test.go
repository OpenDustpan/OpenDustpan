package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// deadlockWait bounds the wait for a registration that must not block. It is
// a safety net: a registration that deadlocks never ends, so the test fails
// with a message instead of hanging until the test binary times out.
const deadlockWait = 30 * time.Second

// errNoPhaseOfTheTest is the answer of a phase clock that knows no phase.
var errNoPhaseOfTheTest = errors.New("the job has no live phase")

// allowLocks authorizes every action of the manual lock service.
type allowLocks struct{}

func (allowLocks) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return nil
}

// fixedPhase is a phase clock with one answer.
type fixedPhase struct {
	at  time.Time
	err error
}

func (f fixedPhase) PhaseStartedAt(context.Context, string, manuallock.Unit) (time.Time, error) {
	return f.at, f.err
}

// lockAfterPreCheck is a claim store that puts a manual lock of the namespace
// in the store after the checks that run before the transaction, and then runs
// the transaction. It arms the reader store, so that a read of the locks
// through the lock service after this point shows in the test.
type lockAfterPreCheck struct {
	workspace.ClaimStore

	put   func()
	armed *atomic.Bool
}

func (l *lockAfterPreCheck) WithClaims(
	ctx context.Context, ws workspace.ID, fence namespace.Fence, fn func(tx workspace.ClaimTx) error,
) error {
	l.put()
	l.armed.Store(true)

	return l.ClaimStore.WithClaims(ctx, ws, fence, fn)
}

// queuedWriterStore is the store of the lock service. When it is armed, a read
// of the covering locks first queues a writer on the lock of the store (as the
// restore swap and Close do) and waits until the writer is queued, and only
// then reads. A read that runs while the caller holds the read lock of the
// store then waits for the writer, which waits for the caller: the deadlock.
type queuedWriterStore struct {
	manuallock.Store

	s     *Store
	armed *atomic.Bool
}

func (q *queuedWriterStore) CoveringLocks(ctx context.Context, unit collection.UnitRef) ([]manuallock.Lock, error) {
	if q.armed.Load() {
		go func() {
			q.s.mu.Lock()
			defer q.s.mu.Unlock()
		}()

		// A queued writer makes TryRLock fail. A writer that has gone makes it
		// succeed again, so a read outside the claim lock never waits here.
		for q.s.mu.TryRLock() {
			q.s.mu.RUnlock()
			runtime.Gosched()
		}
	}

	return q.Store.CoveringLocks(ctx, unit)
}

// manualWorld is a store with one workspace, a registry over the real manual
// lock service, and the lock that the claim store puts after the pre-check.
type manualWorld struct {
	registry *workspace.ClaimRegistry
	fence    namespace.Fence
	ws       workspace.ID
}

// lockReader wraps the store that the lock service reads.
type lockReader func(s *Store, armed *atomic.Bool) manuallock.Store

// newManualWorld returns a world whose lock service reads the store itself.
func newManualWorld(t *testing.T, phase fixedPhase) *manualWorld {
	t.Helper()

	return buildManualWorld(t, phase, func(s *Store, _ *atomic.Bool) manuallock.Store { return s })
}

// newQueuedWriterWorld returns a world whose lock service reads a store that
// queues a writer on each read once the claim store has armed it.
func newQueuedWriterWorld(t *testing.T, phase fixedPhase) *manualWorld {
	t.Helper()

	return buildManualWorld(t, phase, func(s *Store, armed *atomic.Bool) manuallock.Store {
		return &queuedWriterStore{Store: s, s: s, armed: armed}
	})
}

func buildManualWorld(t *testing.T, phase fixedPhase, reader lockReader) *manualWorld {
	t.Helper()

	s := openStoreThatMayDeadlock(t)
	fence, ws := workspacetest.SeedClaimWorkspace(t, s)
	armed := new(atomic.Bool)

	service, err := manuallock.NewService(manuallock.Config{
		Store: reader(s, armed), Namespaces: s, Authorizer: allowLocks{}, Phases: phase, Now: nil,
	})
	if err != nil {
		t.Fatalf("lock service: %v", err)
	}

	put := func() {
		target := manuallock.NamespaceTarget(fence.Namespace)
		lock := manuallock.Lock{
			Since:  time.UnixMilli(1_700_000_000_000).UTC(),
			Target: target,
			Holder: "ops",
			Reason: "freeze",
		}

		if putErr := s.PutLock(t.Context(), lock, fence, manualEntry(fence, target)); putErr != nil {
			t.Errorf("put the manual lock: %v", putErr)
		}
	}

	registry, err := workspace.NewClaimRegistry(workspace.ClaimRegistryConfig{
		Workspaces: s, Versions: s, Deleted: s, Manual: service, Now: nil, Lease: 0,
		Store: &lockAfterPreCheck{ClaimStore: s, put: put, armed: armed},
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	return &manualWorld{registry: registry, fence: fence, ws: ws}
}

// openStoreThatMayDeadlock opens a fresh store from the template, as
// openInternalTemplated does, and closes it at the end of a test that passed.
// A test that failed by a deadlock leaves a transaction that holds the read lock
// of the store for good, and Close waits for that lock: the store is left open
// then, so that the test ends with its failure and does not wait for the
// timeout of the test binary.
func openStoreThatMayDeadlock(t *testing.T) *Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyInternalTemplate(t, path)

	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if t.Failed() {
			return
		}

		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return s
}

// manualEntry returns a valid audit entry for a lock of target.
func manualEntry(fence namespace.Fence, target manuallock.Target) audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1_700_000_000_000).UTC(), Namespace: fence.Namespace, Subject: "ops", IDPSubject: "",
		TokenID: "", Action: "unit:lock", Object: target.String(), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "", Origin: nil,
	}
}

// register registers a claim of job-1 and returns the error of the registry.
func (w *manualWorld) register(ctx context.Context) error {
	entry := manualEntry(w.fence, manuallock.NamespaceTarget(w.fence.Namespace))
	entry.Action = string(identity.ActionUnitClaimRegister)

	_, err := w.registry.Register(ctx, &workspace.RegisterClaimRequest{
		ViaJobToken: false, WorkspaceID: w.ws, Base: "sv-acme", Addresses: []string{"aws_instance.a"},
		Replace: false, Principal: "ada", JobID: "job-1", Trigger: "", Audit: entry,
	}, w.fence)

	return err
}

// TestClaimUnderManualLockCallsNoStoreService is the regression test of a
// deadlock. The check of the manual locks under the claim lock once called the
// lock service, which read the store through its own read lock while the
// transaction held that lock. A writer that queued in between (the restore
// swap, Close) then blocked the read and was blocked by the transaction. The
// store of the lock service queues such a writer on every read after the
// manual lock appeared; a registration under the lock must still end, with the
// refusal.
func TestClaimUnderManualLockCallsNoStoreService(t *testing.T) {
	t.Parallel()

	w := newQueuedWriterWorld(t, fixedPhase{at: time.Time{}, err: errNoPhaseOfTheTest})

	ctx, cancel := context.WithTimeout(t.Context(), deadlockWait)
	defer cancel()

	done := make(chan error, 1)

	go func() { done <- w.register(ctx) }()

	select {
	case err := <-done:
		if !errors.Is(err, workspace.ErrClaimLocked) {
			t.Fatalf("register under a manual lock that came after the pre-check = %v, want ErrClaimLocked", err)
		}
	case <-ctx.Done():
		t.Fatal("the registration deadlocked: a store service was called under the claim lock")
	}
}

// TestClaimWithTheRealManualLockService runs the manual lock rule of a job
// phase through the real lock service over the SQLite store: a manual lock that
// comes after the pre-check refuses a job that has no live phase and a job
// whose phase started after the lock, and a job whose phase started before the
// lock passes it.
func TestClaimWithTheRealManualLockService(t *testing.T) {
	t.Parallel()

	lockAt := time.UnixMilli(1_700_000_000_000).UTC()

	for _, c := range []struct {
		name  string
		phase fixedPhase
		want  error
	}{
		{"job with no live phase", fixedPhase{at: time.Time{}, err: errNoPhaseOfTheTest}, workspace.ErrClaimLocked},
		{"phase after the lock", fixedPhase{at: lockAt.Add(time.Hour), err: nil}, workspace.ErrClaimLocked},
		{"phase before the lock", fixedPhase{at: lockAt.Add(-time.Hour), err: nil}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			w := newManualWorld(t, c.phase)

			if err := w.register(t.Context()); !errors.Is(err, c.want) {
				t.Fatalf("register = %v, want %v", err, c.want)
			}
		})
	}
}
