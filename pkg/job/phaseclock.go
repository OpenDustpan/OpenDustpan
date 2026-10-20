package job

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
)

// PhaseClock tells when the phase of a job started. It implements
// manuallock.PhaseClock, so that a tool lock request with the job token of a
// phase that started before a manual lock passes that lock.
type PhaseClock struct {
	// Jobs is the store that holds the job of the token.
	Jobs Store
}

// PhaseStartedAt returns when the current phase of the job jobID started. The
// phase must be live (dispatched, running or canceling) and the job must be a
// job of unit. It returns manuallock.ErrNoPhase for an unknown job, for a job
// of another unit, and for a job with no live phase, and it returns the error
// of the store for any other failure. A caller that cannot tell keeps the
// manual lock.
func (c PhaseClock) PhaseStartedAt(ctx context.Context, jobID string, unit collection.UnitRef) (time.Time, error) {
	j, err := c.Jobs.Get(ctx, ID(jobID))
	if errors.Is(err, ErrNotFound) {
		return time.Time{}, fmt.Errorf("job %s: %w", jobID, manuallock.ErrNoPhase)
	}

	if err != nil {
		return time.Time{}, fmt.Errorf("read job %s: %w", jobID, err)
	}

	// Defense in depth: the job token is for one unit and the lock request names
	// one unit. This check stays so that the token of a job of another unit
	// never passes the manual lock of this one.
	u := j.Spec.Unit
	if u.Namespace() != unit.Namespace || u.Tool() != unit.Tool || u.UnitName() != unit.Name {
		return time.Time{}, fmt.Errorf("job %s: %w", jobID, manuallock.ErrNoPhase)
	}

	if !j.State.live() || j.Current < 0 || j.Current >= len(j.Phases) {
		return time.Time{}, fmt.Errorf("job %s: %w", jobID, manuallock.ErrNoPhase)
	}

	return j.Phases[j.Current].StartedAt, nil
}
