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

	tests := []struct {
		name string
		// swapAfter is the number of lookups that see the first stable ID.
		swapAfter int64
		wantErr   error
	}{
		{"the stable ID stays", 100, nil},
		{"the stable ID changes after the first lookup", 1, outputshare.ErrUnknownProducer},
	}

	for _, tt := range tests {
		for _, read := range []string{"latest", "outputs"} {
			t.Run(tt.name+" on "+read, func(t *testing.T) {
				t.Parallel()

				f := newUpdates(t)
				saveFirstCheckpoint(t, f)

				states := recreatingStates(t, f, tt.swapAfter)

				unit, err := identity.UnitObject("pulumi", f.ref.Namespace, "web/dev")
				if err != nil {
					t.Fatalf("unit object: %v", err)
				}

				unit = unit.WithUnitID("stk-test")

				var got error

				switch read {
				case "latest":
					_, got = states.Latest(t.Context(), unit)
				default:
					_, got = states.Outputs(t.Context(), unit, stack.StateID("stk-test", 1))
				}

				if !errors.Is(got, tt.wantErr) {
					t.Fatalf("%s error = %v, want %v", read, got, tt.wantErr)
				}
			})
		}
	}
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
	body := checkpoint(1, string(rootDeployment(`{"url":"u"}`)))

	if err := f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, body); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	if _, err := f.svc.Complete(ctx, f.ref, s.Update.ID, s.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete: %v", err)
	}
}
