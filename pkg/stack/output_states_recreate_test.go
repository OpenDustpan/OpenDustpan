package stack_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/stack"
)

// swappingStacks is a stack.Store whose GetStack answers the stable ID first
// for the first swapAfter reads, and the stable ID swapped for each read after
// them. It
// models a delete and a create of the same name between two reads.
type swappingStacks struct {
	stack.Store

	inner     stack.CheckpointStacks
	first     stack.ID
	swapped   stack.ID
	swapAfter int64
	reads     atomic.Int64
}

func (m *swappingStacks) GetStack(ctx context.Context, ref stack.Ref) (stack.Stack, error) {
	st, err := m.inner.GetStack(ctx, ref)
	if err != nil {
		return stack.Stack{}, fmt.Errorf("get stack %s: %w", ref, err)
	}

	if m.reads.Add(1) > m.swapAfter {
		st.ID = m.swapped
	} else {
		st.ID = m.first
	}

	return st, nil
}

// TestOutputStatesRefuseARecreatedStack proves that a stack that a delete and
// a create replace between the lookup and the read of the checkpoint is an
// unknown producer, for the State and for the Outputs, and that a stack that
// keeps its stable ID answers as before.
func TestOutputStatesRefuseARecreatedStack(t *testing.T) {
	t.Parallel()

	tests := []recreateCase{
		{"the stable ID stays", false, 100, nil},
		{"the stable ID changes after the first lookup", false, 1, outputshare.ErrUnknownProducer},
		{"no state, the ID stays", true, 100, nil},
		{"no state, the ID changes", true, 1, outputshare.ErrUnknownProducer},
	}

	for _, tt := range tests {
		reads := []string{"latest", "outputs"}
		if tt.noState {
			reads = []string{"latest"}
		}

		for _, read := range reads {
			t.Run(tt.name+" on "+read, func(t *testing.T) {
				t.Parallel()

				checkRecreatedStack(t, tt, read)
			})
		}
	}
}

// recreateCase is one row of TestOutputStatesRefuseARecreatedStack.
type recreateCase struct {
	name string
	// noState is true when no update saves a checkpoint. Only Latest runs
	// these rows: Outputs answers a stack with no state through gone.
	noState bool
	// swapAfter is the number of lookups that see the first stable ID.
	swapAfter int64
	wantErr   error
}

// checkRecreatedStack runs read on the stack of a fixture, with the stable ID
// that changes after tc.swapAfter lookups. A noState stack has no checkpoint,
// and then Latest answers the zero State when the ID stays.
func checkRecreatedStack(t *testing.T, tc recreateCase, read string) {
	t.Helper()

	f := newUpdates(t)
	if !tc.noState {
		saveFirstCheckpoint(t, f)
	}

	states := recreatingStates(t, f, tc.swapAfter)

	unit, err := identity.UnitObject("pulumi", f.ref.Namespace, "web/dev")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	unit = unit.WithUnitID("stk-test")

	gotState, got := readOnce(t.Context(), states, unit, read)

	if !errors.Is(got, tc.wantErr) {
		t.Fatalf("%s error = %v, want %v", read, got, tc.wantErr)
	}

	zero := outputshare.State{ID: "", ClientEncrypted: false}
	if tc.noState && tc.wantErr == nil && gotState != zero {
		t.Fatalf("latest state = %+v, want the zero State", gotState)
	}
}

// readOnce runs the read named by read on unit. For "latest" it returns the
// State of the read, and for "outputs" it asks for the version of the first
// checkpoint.
func readOnce(
	ctx context.Context, states *stack.OutputStates, unit identity.Object, read string,
) (outputshare.State, error) {
	zero := outputshare.State{ID: "", ClientEncrypted: false}

	if read == "latest" {
		st, err := states.Latest(ctx, unit)
		if err != nil {
			return zero, fmt.Errorf("latest: %w", err)
		}

		return st, nil
	}

	if _, err := states.Outputs(ctx, unit, stack.StateID("stk-test", 1)); err != nil {
		return zero, fmt.Errorf("outputs: %w", err)
	}

	return zero, nil
}

// recreatingStates returns the OutputStates over the checkpoints of f, with a
// stack store that swaps the stable ID of the stack after swapAfter reads.
func recreatingStates(t *testing.T, f updatesFixture, swapAfter int64) *stack.OutputStates {
	t.Helper()

	store := &swappingStacks{
		Store: nil, inner: f.store, first: "stk-test", swapped: "stk-other", swapAfter: swapAfter,
		reads: atomic.Int64{},
	}

	stacks, err := stack.NewService(stack.Config{
		Store: store, Namespaces: f.namespaces, Now: nil, State: nil, Retention: 0, ManualLocks: nil,
	})
	if err != nil {
		t.Fatalf("new stack service: %v", err)
	}

	return stack.NewOutputStates(stacks, f.svc)
}

// saveFirstCheckpoint finishes an update of the stack of f that saves version
// 1 with one root output.
func saveFirstCheckpoint(t *testing.T, f updatesFixture) {
	t.Helper()

	ctx := t.Context()
	s := f.started(t, stack.KindUpdate)
	body := checkpoint(1, envelope(string(rootDeployment(`{"url":"u"}`))))

	if err := f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, body); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	if _, err := f.svc.Complete(ctx, f.ref, s.Update.ID, s.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete: %v", err)
	}
}
