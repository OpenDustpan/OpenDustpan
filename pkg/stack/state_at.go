package stack

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// InventoryTool is the tool code of the Pulumi CLI in the inventory and its
// change log.
const InventoryTool = "pulumi"

// ExportAt returns the checkpoint that held the state of the stack at the time
// at, and the StateAt that names its update. A time before the first
// checkpoint returns ErrNoStateAt, and its text names the end time of the
// first update that saved a checkpoint. When an update ran at the time, the
// StateAt names it in Running, and the checkpoint is still the one of the
// update before it. The checkpoint is the deployment as ExportVersion
// returns it.
func (s *Updates) ExportAt(ctx context.Context, ref Ref, at time.Time) (Checkpoint, StateAt, error) {
	if s.stateAt == nil {
		return Checkpoint{}, StateAt{}, fmt.Errorf("%w: state at a time", ErrMissingDependency)
	}

	// The three reads (stack, state, checkpoint) are not one snapshot. A
	// delete between them makes the call return ErrNotFound.
	st, err := s.stacks.GetStack(ctx, ref)
	if err != nil {
		return Checkpoint{}, StateAt{}, fmt.Errorf("state of %s at %s: %w", ref, at.UTC().Format(time.RFC3339), err)
	}

	found, err := s.stateAt.StateAt(ctx, st.ID, at)

	if none, ok := errors.AsType[*NoStateAtError](err); ok {
		if none.First.IsZero() {
			return Checkpoint{}, StateAt{}, fmt.Errorf("%w: %s has no checkpoint yet", ErrNoStateAt, ref)
		}

		return Checkpoint{}, StateAt{}, fmt.Errorf(
			"%w: the first state of %s is at %s (%d ms)",
			ErrNoStateAt, ref, none.First.UTC().Format(time.RFC3339), none.First.UnixMilli(),
		)
	}

	if err != nil {
		return Checkpoint{}, StateAt{}, fmt.Errorf("state of %s at %s: %w", ref, at.UTC().Format(time.RFC3339), err)
	}

	if s.names != nil {
		found.Names, err = s.names.UnitNames(ctx, ref.Namespace, InventoryTool, st.ID.String(), at)
		if err != nil {
			return Checkpoint{}, StateAt{}, fmt.Errorf("names of %s at %s: %w", ref, at.UTC().Format(time.RFC3339), err)
		}
	}

	cp, err := s.ExportVersion(ctx, ref, int64(found.Version))
	if err != nil {
		return Checkpoint{}, StateAt{}, err
	}

	return cp, found, nil
}
