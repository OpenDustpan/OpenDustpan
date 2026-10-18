package catalog

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/quota"
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
	// StateExpired: terminal, the job expired, or the revision passed its
	// deadline (RevisionDeadline).
	StateExpired RevisionState = "expired"
	// StateSuperseded: terminal, a revision from git replaced it before its
	// job delivered (CheckSupersede).
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
// the state next. A terminal revision does not change: ErrFinished. No
// revision goes back to StateAccepted: ErrTransition. A move to the same
// state is allowed and changes nothing. Every other move is allowed,
// because the job model decides the order of the job states. A move to
// StateSuperseded also needs CheckSupersede.
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
	default:
		return nil
	}
}

// CheckSupersede reports whether a revision from git can supersede old, the
// revision in flight of the same request, whose job is j, or nil when the
// caller did not read the job. It returns ErrFinished for an old revision
// that ended. It returns ErrConflict when the job of old delivered its
// change for good, a direct push or a merged review request: the change is
// in git and waits for its state version, so the job cannot be canceled,
// and a second change of the unit must wait for it. The error says so and
// names the job. Else a supersede is allowed, and the Catalog cancels the
// job of old after the store write.
func CheckSupersede(old *Revision, j *job.Job) error {
	switch {
	case old.State.Terminal():
		return fmt.Errorf("%w: the revision is %s", ErrFinished, old.State)
	case old.Job != "" && (old.State == StateAwaitingState || (j != nil && j.Delivery.Settled())):
		return fmt.Errorf("%w: the change of revision %d is already in git with the job %s; "+
			"wait for its state version, then render again", ErrConflict, old.Number, old.Job)
	default:
		return nil
	}
}

// The longest waits of a revision, before the grace of its reservation.
const (
	// GitRevisionWait is the longest wait of a revision from git for its
	// state version: the state wait of a write-back job.
	GitRevisionWait = job.DefaultStateTimeout
	// JobRevisionWait is the longest wait of a revision with a job: the
	// sum of the time limits of the job model for a Catalog job, which are
	// the queue, one approval gate, the dispatch of a plan and of an apply,
	// and the state wait.
	JobRevisionWait = job.DefaultQueueTimeout + job.DefaultApprovalTimeout + 2*job.DefaultDispatchTimeout +
		job.DefaultStateTimeout
)

// RevisionDeadline returns the deadline of a revision of origin that the
// store takes at the time at: at, plus the longest wait of the origin, plus
// quota.ReservationGrace. A revision from git waits for its state version
// for at most GitRevisionWait, so its deadline is 15 days after its render.
// The reservation of a create ends at the same time (quota.Reservation
// EndsAt), so the Catalog knows the end time at the reserve.
//
// A revision in flight at its deadline ends StateExpired. The Catalog
// cancels its job when the job model allows it, and releases its
// reservation. So a reservation never expires while its revision is in
// flight.
func RevisionDeadline(origin Origin, at time.Time) time.Time {
	wait := JobRevisionWait
	if origin == OriginGit {
		wait = GitRevisionWait
	}

	return at.Add(wait + quota.ReservationGrace)
}

// Expired reports whether r is in flight at now and past its deadline. A
// read of r treats it as StateExpired, and the expire schedule writes the
// state.
func (r *Revision) Expired(now time.Time) bool {
	return !r.State.Terminal() && !r.Deadline.IsZero() && !now.Before(r.Deadline)
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
