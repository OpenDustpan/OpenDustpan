package durable_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
)

// TestRunnerRunsAnOperationStartedWithItsClaim proves that an operation that
// a start claimed for the runner runs on the caller with RunClaimed, and that
// no other claimer can take it while it runs.
func TestRunnerRunsAnOperationStartedWithItsClaim(t *testing.T) {
	t.Parallel()

	mem := durabletest.NewMem()
	store := mem.Store()
	rec := newRecorder()
	other := newTestRunner(t, store, "worker", rec.step("other", nil))

	var claimedByOther error

	step := rec.step("one", func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
		claimedByOther = other.RunOnce(ctx)

		return nil, nil
	})

	r := newTestRunner(t, store, "starter", step)

	claimedAt := time.Now()

	op, err := mem.StartClaimedWith(t.Context(),
		durable.StartRequest{Key: "k", Kind: testKind, Version: 1, Input: nil}, r.StartClaim(), durabletest.Hooks{
			Before: nil, After: nil,
		})
	if err != nil {
		t.Fatalf("start with the claim of the runner: %v", err)
	}

	if op.State != durable.StateRunning || op.Claim.Owner != "starter" {
		t.Fatalf("started operation = %s, owner %q; want running with the claim of the starter", op.State,
			op.Claim.Owner)
	}

	if err := r.RunClaimed(t.Context(), op, claimedAt); err != nil {
		t.Fatalf("RunClaimed: %v", err)
	}

	if !errors.Is(claimedByOther, durable.ErrNoWork) {
		t.Fatalf("claim of another runner while the step ran = %v, want ErrNoWork", claimedByOther)
	}

	waitFor(t, store, op.ID, "succeeded", inState(durable.StateSucceeded))

	if n := rec.count("one"); n != 1 {
		t.Fatalf("step runs = %d, want 1", n)
	}
}

// TestRunClaimedRefusesAnOperationWithoutItsClaim proves that RunClaimed runs
// only a running operation of a kind of the runner with a claim of its owner,
// and changes nothing otherwise.
func TestRunClaimedRefusesAnOperationWithoutItsClaim(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	rec := newRecorder()
	r := newTestRunner(t, store, "starter", rec.step("one", nil))
	start(t, store, "claimed")

	claimed, err := store.Claim(t.Context(), durable.ClaimRequest{
		Owner: "worker", Kinds: []durable.Kind{testKind}, MaxVersions: nil, Lease: time.Minute, TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("claim for the worker: %v", err)
	}

	pending := start(t, store, "pending")

	unknown := claimed
	unknown.Kind = "other"
	unknown.Claim.Owner = "starter"

	for name, op := range map[string]durable.Operation{
		"a pending operation":          pending,
		"a claim of another owner":     claimed,
		"a kind that the runner lacks": unknown,
	} {
		if err := r.RunClaimed(t.Context(), op, time.Now()); !errors.Is(err, durable.ErrInvalid) {
			t.Errorf("RunClaimed of %s = %v, want ErrInvalid", name, err)
		}
	}

	if n := rec.count("one"); n != 0 {
		t.Fatalf("step runs = %d, want 0", n)
	}

	if got := mustGet(t, store, claimed.ID); got.State != durable.StateRunning || got.Claim.Owner != "worker" {
		t.Fatalf("operation after the refusals = %s, owner %q; want the claim of the worker", got.State,
			got.Claim.Owner)
	}
}
