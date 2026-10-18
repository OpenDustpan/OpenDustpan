package stack

import (
	"context"
	"errors"
	"fmt"
	"time"
)

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

	st, err := s.stacks.GetStack(ctx, ref)
	if err != nil {
		return Checkpoint{}, StateAt{}, fmt.Errorf("state of %s at %s: %w", ref, at.UTC().Format(time.RFC3339), err)
	}

	found, err := s.stateAt.StateAt(ctx, st.ID, at)
	if errors.Is(err, ErrNoStateAt) {
		if found.EndedAt.IsZero() {
			return Checkpoint{}, StateAt{}, fmt.Errorf("%w: %s has no checkpoint yet", ErrNoStateAt, ref)
		}

		return Checkpoint{}, StateAt{}, fmt.Errorf(
			"%w: the first state of %s is at %s (%d ms)",
			ErrNoStateAt, ref, found.EndedAt.UTC().Format(time.RFC3339), found.EndedAt.UnixMilli(),
		)
	}

	if err != nil {
		return Checkpoint{}, StateAt{}, fmt.Errorf("state of %s at %s: %w", ref, at.UTC().Format(time.RFC3339), err)
	}

	cp, err := s.ExportVersion(ctx, ref, int64(found.Version))
	if err != nil {
		return Checkpoint{}, StateAt{}, err
	}

	return cp, found, nil
}
