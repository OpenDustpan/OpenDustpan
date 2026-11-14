package maintenance_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/placementtest"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// racingState is the real store with a hook that runs before the
// compare-and-set of an enter, as an update that starts between the check
// and the write does.
type racingState struct {
	*sqlite.Store

	beforeSetIf func()
}

func (r *racingState) SetStateIf(ctx context.Context, expected, next maintenance.State) error {
	if fn := r.beforeSetIf; fn != nil && expected == maintenance.StateNormal && next == maintenance.StateMaintenance {
		r.beforeSetIf = nil

		fn()
	}

	return r.Store.SetStateIf(ctx, expected, next)
}

// realStack is the real store with the services that a forced enter works on.
type realStack struct {
	st         *sqlite.Store
	namespaces *namespace.Service
	stacks     *stack.Service
	updates    *stack.Updates
	workspaces *workspace.Service
}

func newRealStack(t *testing.T) *realStack {
	t.Helper()

	ctx := t.Context()

	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: testRegion})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	if _, createErr := namespaces.Create(ctx, testNamespace); createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	stacks, err := stack.NewService(
		stack.Config{Store: st, Namespaces: namespaces, Now: nil, State: nil, Retention: 0, ManualLocks: nil},
	)
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:      secrettest.NewMemorySealer(),
		BlobGuard:   blobguardtest.None{},
		ManualLocks: nil, Updates: st, Tokens: nil, Checkpoints: st, Events: st, Namespaces: namespaces,
		Stacks: stacks, Blobs: newTestBlobs(t), Now: nil, Logger: nil, MaxCheckpointBytes: 0, Journal: nil,
		Replayer: nil, Refusals: stacktest.NewRefusals(),
	})
	if err != nil {
		t.Fatalf("update service: %v", err)
	}

	workspaces, err := workspace.NewService(workspace.Config{
		BlobGuard:   blobguardtest.None{},
		ManualLocks: nil, Workspaces: st, States: st, Blobs: newTestBlobs(t), Namespaces: namespaces,
		OutOfScope: workspacetest.NewRefusals(),
		Sealer:     stubSealer{}, Logger: nil, Now: nil, MaxStateBytes: 0, MaxPendingVersions: 0, Retention: 0,
	})
	if err != nil {
		t.Fatalf("workspace service: %v", err)
	}

	return &realStack{st: st, namespaces: namespaces, stacks: stacks, updates: updates, workspaces: workspaces}
}

// startUpdate creates a stack and starts an update on it.
func (r *realStack) startUpdate(t *testing.T, name string) {
	t.Helper()

	ctx := t.Context()

	ref, err := stack.ParseRef(testNamespace.String(), "web", name)
	if err != nil {
		t.Fatalf("parse stack reference: %v", err)
	}

	ns, err := r.namespaces.Get(ctx, testNamespace)
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	place := placementtest.Stack(t, r.st, ns.Fence())

	_, createErr := r.stacks.CreatePlaced(ctx, ref, nil, place)
	if createErr != nil {
		t.Fatalf("create stack %s: %v", name, createErr)
	}

	plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""}

	u, err := r.updates.Create(ctx, ref, plan)
	if err != nil {
		t.Fatalf("create update of %s: %v", name, err)
	}

	if _, startErr := r.updates.Start(ctx, ref, u.ID); startErr != nil {
		t.Fatalf("start update of %s: %v", name, startErr)
	}
}

func (r *realStack) service(t *testing.T, state maintenance.Store) *maintenance.Service {
	t.Helper()

	service, err := maintenance.NewService(maintenance.Config{
		Store: state, Namespaces: r.namespaces, Stacks: r.stacks, Updates: r.updates, Workspaces: r.workspaces,
		Hold: nil,
	})
	if err != nil {
		t.Fatalf("maintenance service: %v", err)
	}

	return service
}

func (r *realStack) wantState(t *testing.T, label string, want maintenance.State) {
	t.Helper()

	if got, err := r.st.GetState(t.Context()); err != nil || got != want {
		t.Fatalf("state %s = %q, %v, want %q without error", label, got, err, want)
	}
}

// TestForceEnterWithRacedStartOnTheRealStore proves, with the checked writes
// of a real store, that a forced enter whose write raced an update start
// does not fail on a refused cancel: the attempt moves back to normal, the
// next attempt cancels the new update before the write, and the answer names
// both updates.
func TestForceEnterWithRacedStartOnTheRealStore(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rs := newRealStack(t)
	raced := false
	state := &racingState{Store: rs.st, beforeSetIf: func() {
		if !raced {
			raced = true

			rs.startUpdate(t, "staging")
		}
	}}
	service := rs.service(t, state)

	rs.startUpdate(t, "prod")

	res, err := service.Enter(ctx, true)
	if err != nil {
		t.Fatalf("force enter with a raced start: %v", err)
	}

	want := []string{"pulumi:acme/web/prod", "pulumi:acme/web/staging"}
	if !slices.Equal(res.Canceled, want) {
		t.Fatalf("force enter canceled = %v, want %v", res.Canceled, want)
	}

	rs.wantState(t, "after the enter", maintenance.StateMaintenance)
}
