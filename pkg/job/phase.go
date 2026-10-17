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
// An apply and a destroy plan first, so that a person can read the plan and
// a gate can bind its approvals to it. It returns ErrUnsupported for a
// write-back of another operation, and ErrInvalid for an unknown executor or
// operation.
func DefaultPhases(k ExecutorKind, op Operation) ([]PhaseKind, error) {
	if _, err := startAction(k, op); err != nil {
		return nil, err
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
// end write. ev is EventClaim for a takeover of the operation by a new
// claim, or EventRelease for an executor that gives the phase back.
//
// When a step that changes resources has a begin record and no end record,
// the change may be half done. The phase then fails, and the reason tells
// the operator to read the state of the unit: OpenDustpan never repeats a
// change. Otherwise ev stands. After a takeover, the new claim runs the
// phase again from its first step, on a fresh checkout.
func AfterLostClaim(steps []Step, begun, ended []string, ev Event) (Event, string) {
	for i := range steps {
		s := &steps[i]
		if s.ChangesResources && slices.Contains(begun, s.Name) && !slices.Contains(ended, s.Name) {
			return EventPhaseFailed, fmt.Sprintf("the executor stopped during step %q, which can change "+
				"resources: read the state of the unit before you start a new job", s.Name)
		}
	}

	return ev, ""
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
