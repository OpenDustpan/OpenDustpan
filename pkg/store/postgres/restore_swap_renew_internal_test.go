package postgres

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// renewWait is how long a renewal may take while the swap is held open. A
// renewal that waits for the swap transaction takes as long as the hold.
const renewWait = 2 * time.Second

// Durations of the slow swap test. The swap is held for longer than the whole
// lease, so it ends only when the renewals keep the claim.
const (
	slowSwapLease = 3 * time.Second
	slowSwapRenew = 500 * time.Millisecond
	slowSwapHold  = 2 * slowSwapLease
)

// TestRestoreSwapKeepsClaimRenewable proves that the claim of a restore can
// be renewed while its swap transaction runs, and that the renewal stays in
// the live schema after the swap. A swap that locked the row of its own
// operation at its start made each renewal wait for the end of the swap, so
// a slow swap lost its claim to itself.
func TestRestoreSwapKeepsClaimRenewable(t *testing.T) {
	t.Parallel()

	for _, keepAccess := range []bool{false, true} {
		t.Run(map[bool]string{false: "swap", true: "swap keeping access"}[keepAccess], func(t *testing.T) {
			t.Parallel()

			checkRenewalDuringSwap(t, keepAccess)
		})
	}
}

// checkRenewalDuringSwap holds a swap open after its checks, renews the claim
// of the restore while it is held, and checks the claim after the swap.
func checkRenewalDuringSwap(t *testing.T, keepAccess bool) {
	t.Helper()

	s := openAt(t, membershipURL(t), "")
	staged := storetest.StageRestoreArea(t, s)
	ops := s.DurableOperationsPort()
	op := mustGetOperation(t, ops, staged.Swap.Operation)

	entered := make(chan struct{})
	release := make(chan struct{})

	s.swapHook = func(context.Context) {
		close(entered)
		<-release
	}

	done := make(chan error, 1)

	go func() {
		_, applyErr := s.ApplyRestoreArea(t.Context(), staged.Swap, staged.Area, keepAccess)
		done <- applyErr
	}()

	<-entered

	ctx, cancel := context.WithTimeout(t.Context(), renewWait)
	renewed, renewErr := ops.Extend(ctx, op.Claim, time.Hour)

	cancel()
	// The release comes after the renewal: a renewal that waits for the swap
	// ends with its timeout first.
	close(release)

	if applyErr := <-done; applyErr != nil {
		t.Fatalf("swap = %v, want nil", applyErr)
	}

	if renewErr != nil {
		t.Fatalf("renewal while the swap runs = %v, want nil within %v", renewErr, renewWait)
	}

	got := mustGetOperation(t, ops, staged.Swap.Operation)

	if !got.Claim.ExpiresAt.Equal(renewed.ExpiresAt) {
		t.Fatalf("claim expiry after the swap = %v, want the renewal %v", got.Claim.ExpiresAt, renewed.ExpiresAt)
	}

	if _, ok := got.Step(backup.RestoreStepSwap); !ok {
		t.Fatal("swap step is not recorded after the swap")
	}
}

// TestSlowRestoreSwapKeepsItsClaim proves that a swap that runs longer than
// the lease keeps the claim of its restore and runs once. The runner renews
// the claim while the swap transaction is open, as the restore runner does,
// with a stop at the first renewal that fails.
func TestSlowRestoreSwapKeepsItsClaim(t *testing.T) {
	t.Parallel()

	s := openAt(t, membershipURL(t), "")
	staged := storetest.StageRestoreArea(t, s)
	ops := s.DurableOperationsPort()

	// The runner takes the operation with its own claim.
	if err := ops.Release(t.Context(), mustGetOperation(t, ops, staged.Swap.Operation).Claim); err != nil {
		t.Fatalf("release the claim of the stage: %v", err)
	}

	var swaps atomic.Int32

	s.swapHook = func(ctx context.Context) {
		swaps.Add(1)

		select {
		case <-time.After(slowSwapHold):
		case <-ctx.Done():
		}
	}

	swap := func(ctx context.Context, sc durable.StepContext) ([]byte, error) {
		_, applyErr := s.ApplyRestoreArea(ctx, sc.Fence, staged.Area, false)

		return nil, applyErr
	}

	runner, err := durable.NewRunner(durable.RunnerConfig{
		Store: ops, Owner: "instance-b",
		Kinds: []durable.KindSpec{
			{
				Kind:       backup.RestoreOperationKind,
				Version:    backup.RestoreOperationVersion,
				MinVersion: backup.RestoreOperationVersion,
				Steps:      []durable.Step{{Name: backup.RestoreStepSwap, Run: swap}},
			},
		},
		Lease: slowSwapLease, RenewEvery: slowSwapRenew, PollEvery: 0, MaxAttempts: 1, ExpiryLimit: 0,
		Backoff: durable.Backoff{Base: 0, Max: 0}, ClaimBackoff: durable.Backoff{Base: 0, Max: 0},
		WriteTimeout: 0, Deferred: nil, StopOnRenewalError: true, Logger: nil, Rand: nil,
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	if runErr := runner.RunOnce(t.Context()); runErr != nil {
		t.Fatalf("run the restore operation = %v, want nil", runErr)
	}

	got := mustGetOperation(t, ops, staged.Swap.Operation)

	if got.State != durable.StateSucceeded || got.Attempt != 2 || got.Expiries != 0 {
		t.Fatalf("restore operation = %s, attempt %d, expiries %d; want succeeded in the attempt of the runner "+
			"with no expiry", got.State, got.Attempt, got.Expiries)
	}

	if n := swaps.Load(); n != 1 {
		t.Fatalf("swap runs = %d, want 1", n)
	}
}

// mustGetOperation returns the operation id of ops.
func mustGetOperation(t *testing.T, ops durable.Store, id durable.ID) durable.Operation {
	t.Helper()

	op, err := ops.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("get operation %s: %v", id, err)
	}

	return op
}

// TestRestoreSwapRefusesAChangeDuringTheSwap proves that the check of the
// fence under the row lock, at the end of the swap transaction, refuses a
// cancel or a takeover that commits while the swap runs, after its first
// check. The swap then rolls back fully: the staging area stays and no step
// is recorded.
func TestRestoreSwapRefusesAChangeDuringTheSwap(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		change func(t *testing.T, ops *DurableOperations, held durable.Operation)
		want   error
	}{
		{name: "a cancel", want: durable.ErrCanceled, change: func(t *testing.T, ops *DurableOperations, held durable.Operation) {
			t.Helper()

			if err := ops.CancelBefore(t.Context(), held.ID, backup.RestoreStepSwap); err != nil {
				t.Fatalf("cancel during the swap: %v", err)
			}
		}},
		{name: "a takeover", want: durable.ErrClaimLost, change: func(t *testing.T, ops *DurableOperations, held durable.Operation) {
			t.Helper()

			if err := ops.Release(t.Context(), held.Claim); err != nil {
				t.Fatalf("release during the swap: %v", err)
			}

			req := durable.ClaimRequest{
				Owner: "instance-b", Kinds: []durable.Kind{backup.RestoreOperationKind}, MaxVersions: nil,
				Lease: time.Hour, TakeCanceled: false,
			}

			if _, err := ops.Claim(t.Context(), req); err != nil {
				t.Fatalf("takeover during the swap: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkChangeDuringSwap(t, tc.change, tc.want)
		})
	}
}

// checkChangeDuringSwap holds a swap after its checks, commits change from
// outside, and requires that the swap ends with want and changes nothing.
func checkChangeDuringSwap(
	t *testing.T, change func(t *testing.T, ops *DurableOperations, held durable.Operation), want error,
) {
	t.Helper()

	s := openAt(t, membershipURL(t), "")
	staged := storetest.StageRestoreArea(t, s)
	ops := s.DurableOperations()
	held := mustGetOperation(t, ops, staged.Swap.Operation)

	entered := make(chan struct{})
	release := make(chan struct{})

	s.swapHook = func(context.Context) {
		close(entered)
		<-release
	}

	done := make(chan error, 1)

	go func() {
		_, applyErr := s.ApplyRestoreArea(t.Context(), staged.Swap, staged.Area, false)
		done <- applyErr
	}()

	<-entered
	change(t, ops, held)
	close(release)

	if err := <-done; !errors.Is(err, want) {
		t.Fatalf("swap after the change = %v, want %v", err, want)
	}

	areas, err := s.ListRestoreAreas(t.Context())
	if err != nil || !slices.Contains(areas, staged.Area) {
		t.Fatalf("restore areas = %v, %v; want %s kept", areas, err, staged.Area)
	}

	if _, ok := mustGetOperation(t, ops, staged.Swap.Operation).Step(backup.RestoreStepSwap); ok {
		t.Fatal("swap step is recorded after a refused swap")
	}
}
