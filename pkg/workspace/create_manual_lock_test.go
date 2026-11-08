package workspace_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// passStore is the memory store of the tests that keeps the lock pass of each
// create. It reads no manual lock, as a store adapter that keeps none.
type passStore struct {
	*memStore

	mu     sync.Mutex
	passes []time.Time
}

// CreateWorkspace keeps the lock pass of ctx, then creates w.
func (p *passStore) CreateWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error {
	p.mu.Lock()
	p.passes = append(p.passes, workspace.LockPassFrom(ctx))
	p.mu.Unlock()

	return p.memStore.CreateWorkspace(ctx, w, fence)
}

// CreateWorkspacePlaced keeps the lock pass of ctx, then creates w.
func (p *passStore) CreateWorkspacePlaced(
	ctx context.Context, w workspace.Workspace, pl workspace.Placement, fence namespace.Fence,
) error {
	p.mu.Lock()
	p.passes = append(p.passes, workspace.LockPassFrom(ctx))
	p.mu.Unlock()

	return p.memStore.CreateWorkspacePlaced(ctx, w, pl, fence)
}

// serviceOver returns a service over the stores of f, with store for the
// workspaces and locks for the manual locks. locks may be nil.
func serviceOver(t *testing.T, f fixture, store workspace.Store, locks workspace.ManualLocks) *workspace.Service {
	t.Helper()

	svc, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        locks,
		Workspaces:         store,
		States:             f.store,
		Blobs:              f.blobs,
		Namespaces:         f.namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             f.secrets,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      0,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// freshRef returns the reference of the workspace acme/fresh, which does not
// exist yet.
func freshRef(t *testing.T) workspace.Ref {
	t.Helper()

	name, err := workspace.ParseName("fresh")
	if err != nil {
		t.Fatalf("parse name: %v", err)
	}

	return workspace.Ref{Namespace: "acme", Name: name}
}

// TestCreateRefusedByANamespaceLock proves that Create refuses a workspace in
// a namespace with a manual lock in the service, before the store: the store
// of the test reads no manual lock. A job whose phase started before the lock
// passes it. A refused create makes no workspace.
func TestCreateRefusedByANamespaceLock(t *testing.T) {
	t.Parallel()

	lockAt := heldNamespaceLock().Since

	cases := []struct {
		name  string
		jobID string
		want  error
	}{
		{name: "request with no job", jobID: "", want: workspace.ErrLocked},
		{name: "job that started after the lock", jobID: "job-after", want: workspace.ErrLocked},
		{name: "job that started before the lock", jobID: "job-before", want: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			locks := answering(heldNamespaceLock())
			locks.phases = map[string]time.Time{
				"job-before": lockAt.Add(-time.Hour),
				"job-after":  lockAt.Add(time.Hour),
			}
			store := &passStore{memStore: f.store, mu: sync.Mutex{}, passes: nil}
			svc := serviceOver(t, f, store, locks)
			ref := freshRef(t)

			_, err := svc.Create(workspace.WithJob(t.Context(), tc.jobID), ref, memPlacement(ref.Namespace))
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Create = %v, want nil", err)
				}

				return
			}

			checkRefusedCreate(t, svc, store, ref, err)
		})
	}
}

// TestCreateRefusedWhenTheLockReadFails proves that Create fails closed when
// the manual lock of the namespace cannot be read: it returns the read error,
// it does not call the store, and it makes no workspace.
func TestCreateRefusedWhenTheLockReadFails(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	store := &passStore{memStore: f.store, mu: sync.Mutex{}, passes: nil}
	svc := serviceOver(t, f, store, failing(errManualLockRead))
	ref := freshRef(t)

	if _, err := svc.Create(t.Context(), ref, memPlacement(ref.Namespace)); !errors.Is(err, errManualLockRead) {
		t.Fatalf("Create = %v, want the read error of the manual lock", err)
	}

	if len(store.passes) != 0 {
		t.Fatalf("the store was called %d times, want none", len(store.passes))
	}

	if _, getErr := svc.Get(t.Context(), ref); !errors.Is(getErr, workspace.ErrNotFound) {
		t.Fatalf("Get after a refused create = %v, want ErrNotFound", getErr)
	}
}

// checkRefusedCreate checks that err is the refusal of a manual lock, that the
// service did not call the store, and that no workspace ref exists.
func checkRefusedCreate(t *testing.T, svc *workspace.Service, store *passStore, ref workspace.Ref, err error) {
	t.Helper()

	if !errors.Is(err, workspace.ErrLocked) || !errors.Is(err, manuallock.ErrLocked) {
		t.Fatalf("Create = %v, want ErrLocked and manuallock.ErrLocked", err)
	}

	if manual, ok := errors.AsType[*manuallock.LockedError](err); !ok || manual == nil {
		t.Fatalf("Create = %v, want a refusal that names the manual lock", err)
	}

	if len(store.passes) != 0 {
		t.Fatalf("the store was called %d times, want none", len(store.passes))
	}

	if _, getErr := svc.Get(t.Context(), ref); !errors.Is(getErr, workspace.ErrNotFound) {
		t.Fatalf("Get after a refused create = %v, want ErrNotFound", getErr)
	}
}

// TestCreateSetsItsOwnLockPass proves that the lock pass that reaches the
// store comes from the service: a pass that the caller put in the context is
// replaced. With no manual lock service the pass is the zero time, and with
// one it is the start of the phase of the job of the request.
func TestCreateSetsItsOwnLockPass(t *testing.T) {
	t.Parallel()

	forged := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	phase := time.Date(2026, time.October, 9, 8, 0, 0, 0, time.UTC)

	cases := []struct {
		name        string
		withService bool
		jobID       string
		want        time.Time
	}{
		{name: "no manual lock service", withService: false, jobID: "job-1", want: time.Time{}},
		{name: "request with no job", withService: true, jobID: "", want: time.Time{}},
		{name: "job with a phase", withService: true, jobID: "job-1", want: phase},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Each subtest has its own fake: the fake records its calls, so
			// subtests that share one would race. A lock of a collection does
			// not cover a workspace that does not exist yet, so the create
			// passes and the store gets the pass.
			var locks workspace.ManualLocks
			if tc.withService {
				fake := answering(heldCollectionLock())
				fake.phases = map[string]time.Time{"job-1": phase}
				locks = fake
			}

			f := newFixture(t)
			store := &passStore{memStore: f.store, mu: sync.Mutex{}, passes: nil}
			svc := serviceOver(t, f, store, locks)

			ctx := workspace.WithLockPass(workspace.WithJob(t.Context(), tc.jobID), forged)
			if _, err := svc.Create(ctx, freshRef(t), memPlacement("acme")); err != nil {
				t.Fatalf("Create = %v, want nil", err)
			}

			if len(store.passes) != 1 || !store.passes[0].Equal(tc.want) {
				t.Fatalf("lock passes at the store = %v, want [%v]", store.passes, tc.want)
			}
		})
	}
}
