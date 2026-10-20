package maintenance_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
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

// TestForceEnterWithRacedStartOnTheRealStore proves, with the checked writes
// of a real store, that a forced enter whose write raced an update start
// does not fail on a refused cancel: it moves back to normal, names the
// update, and the next forced enter cancels it before the write.
func TestForceEnterWithRacedStartOnTheRealStore(t *testing.T) {
	t.Parallel()

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

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: testRegion})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	if _, createErr := namespaces.Create(ctx, testNamespace); createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	stacks, err := stack.NewService(stack.Config{Store: st, Namespaces: namespaces, Now: nil, State: nil, Retention: 0})
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		ManualLocks: nil, Updates: st, Tokens: nil, Checkpoints: st, Events: st, Namespaces: namespaces,
		Stacks: stacks, Blobs: newTestBlobs(t), Now: nil, Logger: nil, MaxCheckpointBytes: 0, Journal: nil,
		Replayer: nil,
	})
	if err != nil {
		t.Fatalf("update service: %v", err)
	}

	workspaces, err := workspace.NewService(workspace.Config{
		ManualLocks: nil, Workspaces: st, States: st, Blobs: newTestBlobs(t), Namespaces: namespaces,
		Sealer: stubSealer{}, Logger: nil, Now: nil, MaxStateBytes: 0, MaxPendingVersions: 0, Retention: 0,
	})
	if err != nil {
		t.Fatalf("workspace service: %v", err)
	}

	startUpdate := func(name string) {
		t.Helper()

		ref, refErr := stack.ParseRef(testNamespace.String(), "web", name)
		if refErr != nil {
			t.Fatalf("parse stack reference: %v", refErr)
		}

		if _, createErr := stacks.Create(ctx, ref, nil); createErr != nil {
			t.Fatalf("create stack %s: %v", name, createErr)
		}

		plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0}

		u, createErr := updates.Create(ctx, ref, plan)
		if createErr != nil {
			t.Fatalf("create update of %s: %v", name, createErr)
		}

		if _, startErr := updates.Start(ctx, ref, u.ID); startErr != nil {
			t.Fatalf("start update of %s: %v", name, startErr)
		}
	}

	state := &racingState{Store: st, beforeSetIf: func() { startUpdate("staging") }}

	service, err := maintenance.NewService(maintenance.Config{
		Store: state, Namespaces: namespaces, Stacks: stacks, Updates: updates, Workspaces: workspaces, Hold: nil,
	})
	if err != nil {
		t.Fatalf("maintenance service: %v", err)
	}

	startUpdate("prod")

	_, err = service.Enter(ctx, true)
	if !errors.Is(err, maintenance.ErrActiveWork) || !strings.Contains(err.Error(), "pulumi:acme/web/staging") {
		t.Fatalf("force enter with a raced start: err = %v, want ErrActiveWork naming pulumi:acme/web/staging", err)
	}

	if got, getErr := st.GetState(ctx); getErr != nil || got != maintenance.StateNormal {
		t.Fatalf("state after the refused enter = %q, %v, want normal without error", got, getErr)
	}

	res, err := service.Enter(ctx, true)
	if err != nil {
		t.Fatalf("second force enter: %v", err)
	}

	if len(res.Canceled) != 1 || res.Canceled[0] != "pulumi:acme/web/staging" {
		t.Fatalf("second force enter canceled = %v, want [pulumi:acme/web/staging]", res.Canceled)
	}

	if got, getErr := st.GetState(ctx); getErr != nil || got != maintenance.StateMaintenance {
		t.Fatalf("state after the second enter = %q, %v, want maintenance without error", got, getErr)
	}
}
