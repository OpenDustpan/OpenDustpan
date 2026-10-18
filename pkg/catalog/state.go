package catalog

import (
	"errors"
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/job"
)

// RevisionState is where a revision of a request is in its life. After
// StateAccepted, the state of a revision comes from the state of its job
// (StateOf). The job is the record: the revision keeps a copy for lists and
// filters.
type RevisionState string

// The states of a revision.
const (
	// StateAccepted: the submit passed its checks, and the Catalog makes
	// the unit and starts the job.
	StateAccepted RevisionState = "accepted"
	// StateQueued: the job waits in the queue.
	StateQueued RevisionState = "queued"
	// StateAwaitingApproval: the job waits at a gate.
	StateAwaitingApproval RevisionState = "awaiting-approval"
	// StateRunning: a phase of the job runs, or stops after a cancel.
	StateRunning RevisionState = "running"
	// StateAwaitingMerge: a write-back job delivered a review request, and
	// the forge did not merge it yet.
	StateAwaitingMerge RevisionState = "awaiting-merge"
	// StateAwaitingState: the change is in git, and the team's CI did not
	// write the final state version yet.
	StateAwaitingState RevisionState = "awaiting-state"
	// StateApplied: terminal, the job succeeded.
	StateApplied RevisionState = "applied"
	// StateFailed: terminal, the job or its start failed.
	StateFailed RevisionState = "failed"
	// StateRejected: terminal, a person, a review request or a recheck
	// rejected it.
	StateRejected RevisionState = "rejected"
	// StateCanceled: terminal, a person canceled it.
	StateCanceled RevisionState = "canceled"
	// StateExpired: terminal, the job expired.
	StateExpired RevisionState = "expired"
	// StateSuperseded: terminal, a revision from git replaced it while it
	// waited for a merge or a state version.
	StateSuperseded RevisionState = "superseded"
)

// Valid reports whether s is one of the states of this package.
func (s RevisionState) Valid() bool {
	switch s {
	case StateAccepted, StateQueued, StateAwaitingApproval, StateRunning, StateAwaitingMerge,
		StateAwaitingState, StateApplied, StateFailed, StateRejected, StateCanceled, StateExpired,
		StateSuperseded:
		return true
	default:
		return false
	}
}

// Terminal reports whether a revision in s has ended.
func (s RevisionState) Terminal() bool {
	return slices.Contains([]RevisionState{
		StateApplied, StateFailed, StateRejected, StateCanceled, StateExpired, StateSuperseded,
	}, s)
}

// StateOf returns the state of a revision whose job is j: queued,
// awaiting-approval and the terminal states map one to one, a job that
// runs or stops is running, and a job that waits for its state version is
// awaiting-merge while its review request has no merge, else
// awaiting-state. It returns ErrInvalid for a state that this package does
// not know.
func StateOf(j *job.Job) (RevisionState, error) {
	switch j.State {
	case job.StateQueued:
		return StateQueued, nil
	case job.StateAwaitingApproval:
		return StateAwaitingApproval, nil
	case job.StateDispatched, job.StateRunning, job.StateCanceling:
		return StateRunning, nil
	case job.StateAwaitingState:
		if _, merged := j.Delivery.CloseAfter(); !merged {
			return StateAwaitingMerge, nil
		}

		return StateAwaitingState, nil
	case job.StateSucceeded:
		return StateApplied, nil
	case job.StateFailed:
		return StateFailed, nil
	case job.StateRejected:
		return StateRejected, nil
	case job.StateCanceled:
		return StateCanceled, nil
	case job.StateExpired:
		return StateExpired, nil
	default:
		return "", fmt.Errorf("%w: job state %q", ErrInvalid, j.State)
	}
}

// CheckTransition reports whether a revision can move from the state old to
// the state next. A
// terminal revision does not change: ErrFinished. No revision goes back to
// StateAccepted, and only a revision that waits for a merge or a state
// version is superseded: ErrTransition. A move to the same state is
// allowed and changes nothing. Every other move is allowed, because the
// job model decides the order of the job states.
func CheckTransition(old, next RevisionState) error {
	switch {
	case !old.Valid() || !next.Valid():
		return fmt.Errorf("%w: from %q to %q", ErrInvalid, old, next)
	case old == next:
		return nil
	case old.Terminal():
		return fmt.Errorf("%w: the revision is %s", ErrFinished, old)
	case next == StateAccepted:
		return fmt.Errorf("%w: from %s back to %s", ErrTransition, old, next)
	case next == StateSuperseded && old != StateAwaitingMerge && old != StateAwaitingState:
		return fmt.Errorf("%w: a %s revision is not superseded", ErrTransition, old)
	default:
		return nil
	}
}

// StartFailure returns the state of a revision whose job start failed with
// err, and false for an error that the start operation tries again: a
// policy denial, a freeze and an agent denial reject the revision, and a
// limit, a request that is not valid and an operation that the executor
// does not support fail it.
func StartFailure(err error) (RevisionState, bool) {
	switch {
	case errors.Is(err, job.ErrPolicyDenied), errors.Is(err, job.ErrFrozen), errors.Is(err, job.ErrAgentDenied):
		return StateRejected, true
	case errors.Is(err, job.ErrLimit), errors.Is(err, job.ErrInvalid), errors.Is(err, job.ErrUnsupported):
		return StateFailed, true
	default:
		return "", false
	}
}
