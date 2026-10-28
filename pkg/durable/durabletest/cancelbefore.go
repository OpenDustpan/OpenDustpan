package durabletest

import (
	"errors"
	"strconv"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/durable"
)

// guardedStep is the step that the CancelBefore cases guard.
const guardedStep = "swap"

// canceler holds the StepCanceler of a store.
type canceler struct {
	durable.StepCanceler
}

// stepCanceler returns the StepCanceler of s, and skips the case when the
// store does not implement it.
func stepCanceler(t *testing.T, s durable.Store) canceler {
	t.Helper()

	c, ok := s.(durable.StepCanceler)
	if !ok {
		t.Skip("the store does not implement durable.StepCanceler")
	}

	return canceler{StepCanceler: c}
}

func testCancelBeforeValidates(t *testing.T, h Harness) {
	t.Helper()

	s := h.Open(t, 0)
	c := stepCanceler(t, s)
	op := mustStart(t, s, "k", kindA)

	cases := []struct {
		name string
		id   durable.ID
		step string
		want error
	}{
		{name: "an empty step", id: op.ID, step: "", want: durable.ErrInvalid},
		{name: "an unknown ID", id: "no-such-operation", step: guardedStep, want: durable.ErrNotFound},
	}

	for _, tc := range cases {
		if err := c.CancelBefore(t.Context(), tc.id, tc.step); !errors.Is(err, tc.want) {
			t.Fatalf("CancelBefore with %s: error = %v, want %v", tc.name, err, tc.want)
		}
	}

	if got := mustGet(t, s, op.ID); got.State != durable.StatePending || got.CancelRequested {
		t.Fatalf("operation after refused CancelBefore calls = %+v, want it pending with no cancel", got)
	}
}

func testCancelBeforePending(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	c := stepCanceler(t, b)
	op := mustStart(t, a, "k", kindA)

	if err := c.CancelBefore(t.Context(), op.ID, guardedStep); err != nil {
		t.Fatalf("CancelBefore of a pending operation: %v", err)
	}

	if got := mustGet(t, a, op.ID); got.State != durable.StateCanceled {
		t.Fatalf("state after CancelBefore of a pending operation = %s, want canceled", got.State)
	}

	if err := c.CancelBefore(t.Context(), op.ID, guardedStep); err != nil {
		t.Fatalf("second CancelBefore: %v, want nil", err)
	}
}

func testCancelBeforeRunning(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	c := stepCanceler(t, b)
	mustStart(t, a, "k", kindA)
	held := mustClaim(t, a, ownerA, longLease, kindA)

	// A recorded step other than the guarded one does not stop the cancel.
	if err := a.RecordStep(t.Context(), held.Claim.Step("stage"), nil); err != nil {
		t.Fatalf("RecordStep of another step: %v", err)
	}

	if err := c.CancelBefore(t.Context(), held.ID, guardedStep); err != nil {
		t.Fatalf("CancelBefore of a running operation: %v", err)
	}

	got := mustGet(t, b, held.ID)
	if got.State != durable.StateRunning || !got.CancelRequested {
		t.Fatalf("operation after CancelBefore = %+v, want running with the cancel requested", got)
	}

	if err := a.RecordStep(t.Context(), held.Claim.Step(guardedStep), nil); !errors.Is(err, durable.ErrCanceled) {
		t.Fatalf("RecordStep of the guarded step after CancelBefore: error = %v, want ErrCanceled", err)
	}

	if _, done := mustGet(t, b, held.ID).Step(guardedStep); done {
		t.Fatal("the guarded step is recorded after CancelBefore, want no record")
	}

	if err := a.Release(t.Context(), held.Claim); err != nil {
		t.Fatalf("Release after CancelBefore: %v", err)
	}

	if got := mustGet(t, b, held.ID); got.State != durable.StateCanceled {
		t.Fatalf("state after the release = %s, want canceled", got.State)
	}
}

func testCancelBeforeRecorded(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	c := stepCanceler(t, b)
	mustStart(t, a, "k", kindA)
	held := mustClaim(t, a, ownerA, longLease, kindA)

	if err := a.RecordStep(t.Context(), held.Claim.Step(guardedStep), nil); err != nil {
		t.Fatalf("RecordStep of the guarded step: %v", err)
	}

	if err := c.CancelBefore(t.Context(), held.ID, guardedStep); !errors.Is(err, durable.ErrStepDone) {
		t.Fatalf("CancelBefore after the guarded step: error = %v, want ErrStepDone", err)
	}

	if got := mustGet(t, b, held.ID); got.State != durable.StateRunning || got.CancelRequested {
		t.Fatalf("operation after a refused CancelBefore = %+v, want running with no cancel", got)
	}

	if err := a.Complete(t.Context(), held.Claim); err != nil {
		t.Fatalf("Complete after a refused CancelBefore: %v", err)
	}

	if err := c.CancelBefore(t.Context(), held.ID, guardedStep); !errors.Is(err, durable.ErrFinished) {
		t.Fatalf("CancelBefore of a succeeded operation: error = %v, want ErrFinished", err)
	}
}

// testCancelBeforeRace races CancelBefore with the record of the guarded
// step. Exactly one of the two wins each round, and the store agrees with
// the winner.
func testCancelBeforeRace(t *testing.T, h Harness) {
	t.Helper()

	a, b := h.Open(t, 0), h.Open(t, 0)
	c := stepCanceler(t, b)

	for round := range racers {
		kind := durable.Kind("race-" + strconv.Itoa(round))
		mustStart(t, a, "k-"+strconv.Itoa(round), kind)
		held := mustClaim(t, a, ownerA, longLease, kind)

		var (
			wg                   sync.WaitGroup
			cancelErr, recordErr error
		)

		wg.Go(func() { cancelErr = c.CancelBefore(t.Context(), held.ID, guardedStep) })
		wg.Go(func() { recordErr = a.RecordStep(t.Context(), held.Claim.Step(guardedStep), nil) })
		wg.Wait()

		got := mustGet(t, b, held.ID)
		_, recorded := got.Step(guardedStep)

		switch {
		case cancelErr == nil && errors.Is(recordErr, durable.ErrCanceled):
			if recorded || !got.CancelRequested {
				t.Fatalf("round %d: the cancel won, and the operation is %+v", round, got)
			}
		case recordErr == nil && errors.Is(cancelErr, durable.ErrStepDone):
			if !recorded || got.CancelRequested {
				t.Fatalf("round %d: the step won, and the operation is %+v", round, got)
			}
		default:
			t.Fatalf("round %d: CancelBefore = %v and RecordStep = %v, want one winner", round, cancelErr, recordErr)
		}
	}
}
