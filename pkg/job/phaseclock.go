package job

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
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
// job of unit. It compares the stable namespace ID and unit ID, so a rename of
// the unit changes no answer. An empty unit ID is for a unit that does not
// exist yet, and the job must then carry the same namespace ID, tool and name.
// It returns manuallock.ErrNoPhase for an unknown job, for a job of another
// unit, for an empty namespace ID and for a job with no live phase, and it
// returns the error of the store for any other failure. A caller that cannot
// tell keeps the manual lock.
func (c PhaseClock) PhaseStartedAt(ctx context.Context, jobID string, unit manuallock.Unit) (time.Time, error) {
	j, err := c.Jobs.Get(ctx, ID(jobID))
	if errors.Is(err, ErrNotFound) {
		return time.Time{}, fmt.Errorf("job %s: %w", jobID, manuallock.ErrNoPhase)
	}

	if err != nil {
		return time.Time{}, fmt.Errorf("read job %s: %w", jobID, err)
	}

	// Defense in depth: the job token is for one unit and the lock request names
	// one unit. This check stays so that the token of a job of another unit
	// never passes the manual lock of this one. It uses the stable IDs, so a
	// rename changes no answer. An empty namespace ID matches no job. An empty
	// unit ID is for a unit that does not exist yet, and the job must then
	// carry the tool and the name of the request.
	if !sameUnit(j.Spec.Unit, j.NamespaceID, unit) {
		return time.Time{}, fmt.Errorf("job %s on %s: %w", jobID, unit.Ref, manuallock.ErrNoPhase)
	}

	if !j.State.live() || j.Current < 0 || j.Current >= len(j.Phases) {
		return time.Time{}, fmt.Errorf("job %s: %w", jobID, manuallock.ErrNoPhase)
	}

	return j.Phases[j.Current].StartedAt, nil
}

// sameUnit reports whether the unit of a job and its namespace are the unit of
// a lock request. An existing unit has a unit ID on both sides, and the two
// must be equal. A unit that does not exist yet has no unit ID, and the job
// must then carry its tool and name. An empty namespace ID on either side
// matches nothing.
func sameUnit(object identity.Object, namespaceID namespace.ID, unit manuallock.Unit) bool {
	if unit.NamespaceID == "" || namespaceID == "" || unit.NamespaceID != namespaceID {
		return false
	}

	if unit.UnitID != "" {
		return object.UnitID() != "" && object.UnitID() == unit.UnitID
	}

	return object.Tool() == unit.Ref.Tool && object.UnitName() == unit.Ref.Name
}
