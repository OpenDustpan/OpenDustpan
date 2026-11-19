package backup

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
)

// applyRecorder records the decisions that reach ApplyRestoreArea. The
// embedded interface is nil: a call that needs another method panics.
type applyRecorder struct {
	RestoreLive

	applied int
	err     error
}

// ApplyRestoreArea implements RestoreStaging.
func (a *applyRecorder) ApplyRestoreArea(
	context.Context, durable.StepFence, StagingArea, SwapDecision,
) (KeepAccessResult, error) {
	a.applied++

	return KeepAccessResult{DroppedTempAdmins: 0, ExpiredUpdateLeases: 0}, a.err
}

// TestStepSwapRefusesAnInvalidDecision proves that the swap step checks the
// decision itself: a decision that keeps or widens the access tables without
// the live row of each kept setting never reaches the store, and the step
// returns ErrCorrupt, marked permanent in the code, so the restore fails.
func TestStepSwapRefusesAnInvalidDecision(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		decision SwapDecision
	}{
		{name: "keeps the access tables and no setting row", decision: SwapDecision{
			KeepAccessTables: true, Widened: false, KeepSettingKeys: nil, SettingBefore: "", SettingAfter: "",
			SettingStatic: false,
		}},
		{name: "widens and keeps no setting row", decision: SwapDecision{
			KeepAccessTables: false, Widened: true, KeepSettingKeys: nil, SettingBefore: "", SettingAfter: "",
			SettingStatic: false,
		}},
		{name: "both widens and keeps", decision: SwapDecision{
			KeepAccessTables: true, Widened: true, KeepSettingKeys: KeptSettingKeys(), SettingBefore: "",
			SettingAfter: "", SettingStatic: false,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id := MintRestoreID()

			area, err := NewStagingArea(id, 1)
			if err != nil {
				t.Fatalf("make staging area: %v", err)
			}

			live := &applyRecorder{RestoreLive: nil, applied: 0, err: nil}
			svc := &RestoreService{
				live: live, operations: nil, canceler: nil, backups: nil, maintenance: nil, blobs: nil,
				leases: nil, target: nil, key: nil, installation: "", afterApply: nil, admit: nil,
				allowDev: false, devHint: "", lease: 0, lockWait: 0, now: nil,
				logger: slog.New(slog.DiscardHandler), runner: nil, accessSetting: nil,
				accessSettingStatic: nil,
				decide:              func(context.Context, restoreInput) SwapDecision { return tc.decision },
			}

			var op durable.Operation

			op.Steps = []durable.StepRecord{{
				Name: RestoreStepStage, Result: []byte(`{"area":"` + area.String() + `"}`), DoneAt: time.Time{},
			}}

			var (
				sc durable.StepContext
				in restoreInput
			)

			sc.Operation, in.ID = op, id

			_, err = svc.stepSwap(t.Context(), sc, in)
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("stepSwap error = %v, want ErrCorrupt", err)
			}

			if live.applied != 0 {
				t.Errorf("the store applied %d invalid decisions, want 0", live.applied)
			}
		})
	}
}

// TestStepSwapFailsTheOperationAtOnce proves that the refusal of an invalid
// decision is permanent: the step runs in a durable.Runner that allows many
// attempts, and the operation ends failed after one attempt, with no retry
// and no call of the store.
func TestStepSwapFailsTheOperationAtOnce(t *testing.T) {
	t.Parallel()

	const kind durable.Kind = "restore-swap-permanent-test"

	id := MintRestoreID()

	area, err := NewStagingArea(id, 1)
	if err != nil {
		t.Fatalf("make staging area: %v", err)
	}

	live := &applyRecorder{RestoreLive: nil, applied: 0, err: nil}
	svc := &RestoreService{
		live: live, operations: nil, canceler: nil, backups: nil, maintenance: nil, blobs: nil,
		leases: nil, target: nil, key: nil, installation: "", afterApply: nil, admit: nil,
		allowDev: false, devHint: "", lease: 0, lockWait: 0, now: nil,
		logger: slog.New(slog.DiscardHandler), runner: nil, accessSetting: nil,
		accessSettingStatic: nil,
		decide: func(context.Context, restoreInput) SwapDecision {
			return SwapDecision{
				KeepAccessTables: false, Widened: true, KeepSettingKeys: nil, SettingBefore: "", SettingAfter: "",
				SettingStatic: false,
			}
		},
	}

	steps := []durable.Step{
		{Name: RestoreStepStage, Run: func(context.Context, durable.StepContext) ([]byte, error) {
			return []byte(`{"area":"` + area.String() + `"}`), nil
		}},
		{Name: RestoreStepSwap, Run: func(ctx context.Context, sc durable.StepContext) ([]byte, error) {
			var in restoreInput

			in.ID = id

			return svc.stepSwap(ctx, sc, in)
		}},
	}

	store := durabletest.NewMem().Store()

	runner, err := durable.NewRunner(durable.RunnerConfig{
		Store: store, Owner: "test", Kinds: []durable.KindSpec{{Kind: kind, Version: 1, MinVersion: 1, Steps: steps}},
		Lease: time.Minute, RenewEvery: 0, PollEvery: time.Millisecond, MaxAttempts: 50, ExpiryLimit: 50, DeadlineReleaseLimit: 0,
		Backoff:      durable.Backoff{Base: time.Millisecond, Max: time.Millisecond},
		ClaimBackoff: durable.Backoff{Base: 0, Max: 0}, WriteTimeout: time.Second, Deferred: nil,
		StopOnRenewalError: false, Logger: nil, Rand: nil,
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	req, err := runner.StartRequest(kind, "swap-permanent", nil)
	if err != nil {
		t.Fatalf("start request: %v", err)
	}

	op, _, err := store.Start(t.Context(), req)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if runErr := runner.RunOnce(t.Context()); runErr != nil {
		t.Logf("RunOnce: %v", runErr)
	}

	got, err := store.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}

	if got.State != durable.StateFailed || got.Failures != 1 {
		t.Fatalf("operation state %q after %d failures, want failed after 1", got.State, got.Failures)
	}

	if live.applied != 0 {
		t.Errorf("the store applied %d invalid decisions, want 0", live.applied)
	}
}

// TestStepSwapFailsTheOperationOnStoreRefusal proves that a refusal of the
// store (ErrCorrupt from ApplyRestoreArea, for a widened swap while the live
// setting row does not say true) is permanent: the operation ends failed
// after one attempt, with no retry.
func TestStepSwapFailsTheOperationOnStoreRefusal(t *testing.T) {
	t.Parallel()

	const kind durable.Kind = "restore-swap-store-refusal-test"

	id := MintRestoreID()

	area, err := NewStagingArea(id, 1)
	if err != nil {
		t.Fatalf("make staging area: %v", err)
	}

	live := &applyRecorder{RestoreLive: nil, applied: 0, err: ErrCorrupt}
	svc := &RestoreService{
		live: live, operations: nil, canceler: nil, backups: nil, maintenance: nil, blobs: nil,
		leases: nil, target: nil, key: nil, installation: "", afterApply: nil, admit: nil,
		allowDev: false, devHint: "", lease: 0, lockWait: 0, now: nil,
		logger: slog.New(slog.DiscardHandler), runner: nil, accessSetting: nil,
		accessSettingStatic: nil,
		decide: func(context.Context, restoreInput) SwapDecision {
			return SwapDecision{
				KeepAccessTables: false, Widened: false, KeepSettingKeys: nil, SettingBefore: "", SettingAfter: "",
				SettingStatic: false,
			}
		},
	}

	steps := []durable.Step{
		{Name: RestoreStepStage, Run: func(context.Context, durable.StepContext) ([]byte, error) {
			return []byte(`{"area":"` + area.String() + `"}`), nil
		}},
		{Name: RestoreStepSwap, Run: func(ctx context.Context, sc durable.StepContext) ([]byte, error) {
			var in restoreInput

			in.ID = id

			return svc.stepSwap(ctx, sc, in)
		}},
	}

	store := durabletest.NewMem().Store()

	runner, err := durable.NewRunner(durable.RunnerConfig{
		Store: store, Owner: "test", Kinds: []durable.KindSpec{{Kind: kind, Version: 1, MinVersion: 1, Steps: steps}},
		Lease: time.Minute, RenewEvery: 0, PollEvery: time.Millisecond, MaxAttempts: 50, ExpiryLimit: 50, DeadlineReleaseLimit: 0,
		Backoff:      durable.Backoff{Base: time.Millisecond, Max: time.Millisecond},
		ClaimBackoff: durable.Backoff{Base: 0, Max: 0}, WriteTimeout: time.Second, Deferred: nil,
		StopOnRenewalError: false, Logger: nil, Rand: nil,
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	req, err := runner.StartRequest(kind, "swap-store-refusal", nil)
	if err != nil {
		t.Fatalf("start request: %v", err)
	}

	op, _, err := store.Start(t.Context(), req)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if runErr := runner.RunOnce(t.Context()); runErr != nil {
		t.Logf("RunOnce: %v", runErr)
	}

	got, err := store.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}

	if got.State != durable.StateFailed || got.Failures != 1 {
		t.Fatalf("operation state %q after %d failures, want failed after 1", got.State, got.Failures)
	}

	if live.applied != 1 {
		t.Errorf("the store was called %d times, want 1", live.applied)
	}
}
