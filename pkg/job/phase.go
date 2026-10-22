package job

import (
	"fmt"
	"slices"
)

// DefaultPhases returns the phases of a job of op through k:
//
//	executor    operation  phases
//	runner      plan       [plan]
//	runner      apply      [plan apply]
//	runner      destroy    [plan apply]
//	runner      refresh    [apply]
//	write-back  apply      [deliver]
//
// A drift check (PurposeDriftCheck) is the operation refresh with the one
// phase plan. See PhasesFor.
//
// An apply and a destroy plan first, so that a person can read the plan and
// a gate can bind its approvals to it. It returns ErrUnsupported for a
// write-back of another operation, and ErrInvalid for an unknown executor or
// operation.
func DefaultPhases(k ExecutorKind, op Operation) ([]PhaseKind, error) {
	return PhasesFor(k, op, PurposeNone)
}

// PhasesFor returns the phases of a job of op and purpose p through k. With
// no purpose it is DefaultPhases. A drift check has the one phase plan: it
// reads the real resources and changes none, so it never holds the unit. It
// returns ErrUnsupported for a drift check through write-back, and ErrInvalid
// for a drift check of an operation other than refresh, and for an unknown
// purpose.
func PhasesFor(k ExecutorKind, op Operation, p Purpose) ([]PhaseKind, error) {
	if !p.Valid() {
		return nil, fmt.Errorf("%w: unknown purpose %q", ErrInvalid, p)
	}

	if _, err := startActionFor(k, op, p); err != nil {
		return nil, err
	}

	if p == PurposeDriftCheck {
		return []PhaseKind{PhasePlan}, nil
	}

	if k == ExecutorWriteBack {
		return []PhaseKind{PhaseDeliver}, nil
	}

	switch op {
	case OperationPlan:
		return []PhaseKind{PhasePlan}, nil
	case OperationRefresh:
		return []PhaseKind{PhaseApply}, nil
	case OperationApply, OperationDestroy:
		return []PhaseKind{PhasePlan, PhaseApply}, nil
	default:
		return nil, fmt.Errorf("%w: unknown operation %q", ErrInvalid, op)
	}
}

// FinishEvent returns the event of the end of a phase of kind k with o.
// Last is true for the last phase of the job. A phase that did not succeed
// gives EventPhaseFailed. A deliver phase that succeeded gives
// EventDelivered, because the job then waits for the state version.
func FinishEvent(k PhaseKind, last bool, o Outcome) Event {
	switch {
	case !o.Succeeded:
		return EventPhaseFailed
	case k == PhaseDeliver:
		return EventDelivered
	case last:
		return EventLastPhaseDone
	default:
		return EventPhaseDone
	}
}

// AfterLostClaim returns the event for a phase whose claim ended with no
// end write. records are the step records of every claim of the phase so
// far. ev is EventClaim for a takeover of the operation by a new claim, or
// EventRelease for an executor that gives the phase back.
//
// When any earlier claim recorded the begin of a step that changes
// resources, the phase fails, whether or not that claim recorded the end
// of the step: the change may be half done, or done, and a new run of the
// phase would repeat it. The reason tells the operator to read the state
// of the unit. OpenDustpan never repeats a change. Otherwise ev stands.
// After a takeover, the new claim runs the phase again from its first
// step, on a fresh checkout.
func AfterLostClaim(steps []Step, records []StepRecord, ev Event) (Event, string) {
	for i := range records {
		r := &records[i]

		s, ok := stepNamed(steps, r.Name)
		if !ok || !s.ChangesResources {
			continue
		}

		if r.FinishedAt.IsZero() {
			return EventPhaseFailed, fmt.Sprintf("the executor stopped during step %q, which can change "+
				"resources: read the state of the unit before you start a new job", s.Name)
		}

		return EventPhaseFailed, fmt.Sprintf("the executor stopped after step %q, which can change "+
			"resources, and before the end of the phase: read the state of the unit before you start a new job",
			s.Name)
	}

	return ev, ""
}

// stepNamed returns the step called name, and false when steps has none.
func stepNamed(steps []Step, name string) (*Step, bool) {
	i := slices.IndexFunc(steps, func(s Step) bool { return s.Name == name })
	if i < 0 {
		return nil, false
	}

	return &steps[i], true
}

// ReasonRestored is the reason of a job whose phase operation a restore
// lost.
const ReasonRestored = "restored"

// AfterOperationEnded returns the event that a reconcile schedule applies to
// the job of o. A later change adds that schedule: no code in the server
// calls this function yet. o is a job that is dispatched, running or
// canceling while the durable operation of its current phase ended with no
// Finish, or does not exist (o.Missing, after a restore). Store.Orphans
// returns such jobs. The rule:
//   - Dispatched: EventPhaseFailed. No executor saw the phase.
//   - Running or canceling: the rule of AfterLostClaim with EventRelease.
//     A step that changes resources and began under any claim fails the
//     phase. Else a canceling job ends canceled (EventRelease), and a
//     running job fails, because its operation cannot run again.
//
// The reason is ReasonRestored for a missing operation, unless a change
// began. It returns ErrTransition for a job in another state.
func AfterOperationEnded(o *Orphan) (Event, string, error) {
	j := &o.Job

	reason := "the operation of the phase ended before the phase did"
	if o.Missing {
		reason = ReasonRestored
	}

	if j.State == StateDispatched {
		return EventPhaseFailed, reason, nil
	}

	if j.State != StateRunning && j.State != StateCanceling {
		return "", "", fmt.Errorf("%w: job %s is %s", ErrTransition, j.ID, j.State)
	}

	var records []StepRecord
	if j.Current < len(j.Phases) {
		records = j.Phases[j.Current].Steps
	}

	ev, why := AfterLostClaim(j.Spec.Steps, records, EventRelease)
	switch {
	case ev == EventPhaseFailed:
		return ev, why, nil
	case j.State == StateCanceling:
		return EventRelease, reason, nil
	default:
		return EventPhaseFailed, reason, nil
	}
}

// CheckCancel returns the error of a cancel of j: ErrFinished for a job
// that ended, and ErrDelivered for a write-back job whose change is in git
// for good. Such a job stays, and waits for its state version, so that
// the job never hides what happened. It returns nil when a cancel is
// allowed.
func CheckCancel(j *Job) error {
	switch {
	case j.State.Terminal():
		return fmt.Errorf("%w: job %s is %s", ErrFinished, j.ID, j.State)
	case j.Delivery.Settled():
		return fmt.Errorf("%w: job %s", ErrDelivered, j.ID)
	default:
		return nil
	}
}
