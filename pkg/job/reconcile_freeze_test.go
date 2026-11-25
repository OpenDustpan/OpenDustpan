package job_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// queuedStore is a job.Store with one queued job and no freeze rule: Apply
// accepts every change, so only the service guard of the reconcile can refuse
// a dispatch in a freeze.
type queuedStore struct {
	job.Store

	queued  job.Job
	applied atomic.Int32
}

// Orphans implements job.Store.
func (*queuedStore) Orphans(context.Context, int) ([]job.Orphan, []job.DamagedRow, error) {
	return nil, nil, nil
}

// List implements job.Store. Only the dispatch read, which has no time bound,
// returns the job.
func (s *queuedStore) List(_ context.Context, f job.Filter) (job.Page, error) {
	if !f.StateChangedBefore.IsZero() {
		return job.Page{Jobs: nil, Damaged: nil, After: ""}, nil
	}

	return job.Page{Jobs: []job.Job{s.queued}, Damaged: nil, After: ""}, nil
}

// Apply implements job.Store.
func (s *queuedStore) Apply(context.Context, job.Change, namespace.Fence, []audit.Entry) (job.Job, error) {
	s.applied.Add(1)

	return s.queued, nil
}

// acmeNamespaces is a job.NamespaceReader that knows the namespace acme.
type acmeNamespaces struct{}

// GetNamespace implements job.NamespaceReader.
func (acmeNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{
		CreatedAt:           time.Time{},
		ID:                  "",
		Name:                name,
		PrimaryInstallation: "",
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
		FailoverVersion:     0,
	}, nil
}

// errFreezeRead is the error of a freeze read that fails.
var errFreezeRead = errors.New("freeze read failed")

// listedFreezes is a job.FreezeStore that lists its freezes.
type listedFreezes struct {
	job.FreezeStore

	freezes []job.Freeze
	err     error
}

// ListFreezes implements job.FreezeStore.
func (l listedFreezes) ListFreezes(context.Context, namespace.Name, time.Time) ([]job.Freeze, error) {
	return l.freezes, l.err
}

// freezingStore is a queuedStore that is also a job.FreezeStore.
type freezingStore struct {
	*queuedStore
	listedFreezes
}

// reconcileResult returns the result of a run with the given counts.
func reconcileResult(dispatched, waiting, failed int) job.ReconcileResult {
	return job.ReconcileResult{
		Ended: 0, Expired: 0, Dispatched: dispatched, Gated: 0, Replanned: 0, Waiting: waiting, Skipped: 0,
		Failed: failed,
	}
}

// TestReconcileDispatchGuardsTheFreeze checks the guard of the reconcile on its
// own: the store has no freeze rule, so each refusal comes from the service.
func TestReconcileDispatchGuardsTheFreeze(t *testing.T) {
	t.Parallel()

	unit, err := identity.UnitObject("opentofu", "acme", "network")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	active := &listedFreezes{FreezeStore: nil, freezes: []job.Freeze{{
		ID: "frz-1", Namespace: "acme", Reason: "release week", StartsAt: time.Now().Add(-time.Hour),
		EndsAt: time.Time{}, CreatedBy: "bo", EndedBy: "",
	}}, err: nil}

	// freezes nil gives a job store that is no job.FreezeStore.
	tests := []struct {
		name       string
		freezes    *listedFreezes
		breakGlass *job.BreakGlass
		want       job.ReconcileResult
		applied    int32
	}{
		{"active freeze keeps the job queued", active, nil, reconcileResult(0, 1, 0), 0},
		{"a store that is no freeze store fails closed", nil, nil, reconcileResult(0, 0, 1), 0},
		{
			"break-glass passes", active, &job.BreakGlass{Reason: "outage", Freeze: "frz-1"},
			reconcileResult(1, 0, 0), 1,
		},
		{
			"a failed freeze read fails closed",
			&listedFreezes{FreezeStore: nil, freezes: nil, err: errFreezeRead}, nil, reconcileResult(0, 0, 1), 0,
		},
		{
			"no freeze dispatches", &listedFreezes{FreezeStore: nil, freezes: nil, err: nil}, nil,
			reconcileResult(1, 0, 0), 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			queued := newFreezeJob(unit)
			queued.BreakGlass = tt.breakGlass
			store := &queuedStore{Store: nil, queued: *queued, applied: atomic.Int32{}}

			var jobs job.Store = store
			if tt.freezes != nil {
				jobs = freezingStore{queuedStore: store, listedFreezes: *tt.freezes}
			}

			r, err := job.NewReconciler(job.ReconcilerConfig{
				Store: jobs, Namespaces: acmeNamespaces{}, QueueTimeout: 0, DispatchTimeout: 0, StateTimeout: 0,
				MaxPlanAge: 0, Approvers: gate.ApproverPolicy{Production: false, IsDev: nil}, Batch: 0, Now: nil,
				Log: nil,
			})
			if err != nil {
				t.Fatalf("NewReconciler = %v", err)
			}

			res, err := r.Reconcile(t.Context())
			if err != nil || res != tt.want {
				t.Fatalf("Reconcile = %+v, %v; want %+v", res, err, tt.want)
			}

			if got := store.applied.Load(); got != tt.applied {
				t.Fatalf("Apply calls = %d, want %d", got, tt.applied)
			}
		})
	}
}
