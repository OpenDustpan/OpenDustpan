package job

import (
	"fmt"

	"opendustpan.org/opendustpan/pkg/identity"
)

// Actions of jobs, runners and freezes. Each comment names the object of
// the action. The REST API declares them on its routes.
const (
	// ActionUnitRunPlan allows starting a runner job that only plans. Its
	// object is a unit.
	ActionUnitRunPlan = identity.ActionUnitRunPlan
	// ActionUnitRunApply allows starting a runner job that applies or
	// refreshes. Its object is a unit.
	ActionUnitRunApply = identity.ActionUnitRunApply
	// ActionUnitRunDestroy allows starting a runner job that destroys the
	// resources of a unit. Its object is a unit.
	ActionUnitRunDestroy = identity.ActionUnitRunDestroy
	// ActionUnitWriteBack allows starting a write-back job: a commit to the
	// code location of a unit. Its object is a unit.
	ActionUnitWriteBack = identity.ActionUnitWriteBack
	// ActionUnitDefineSteps allows changing the custom steps of a unit in
	// its settings, or of a blueprint. Custom steps run on the runners of
	// the namespace, so their change is its own action, apart from the
	// start of a job. Its object is a unit, or the blueprint.
	ActionUnitDefineSteps = identity.ActionUnitDefineSteps
	// ActionJobRead allows reading the jobs of a unit: the spec, the state,
	// the phases, the steps, the approvals and the metadata of plans and
	// logs. Its object is a unit, or the namespace for a list.
	ActionJobRead = identity.ActionJobRead
	// ActionJobReadLogs allows reading the content of job logs. Its object
	// is a unit. A redaction of a plan or of a log, also by substitution,
	// needs the action unit:redact of the history model: the job model has
	// no redact action of its own.
	ActionJobReadLogs = identity.ActionJobReadLogs
	// ActionJobReadPlans allows reading the content of stored plans, with
	// the values that the tool marks as sensitive masked. The unmasked plan
	// also needs identity.ActionUnitReadSecrets. Its object is a unit. Each
	// built-in role with identity.ActionUnitRead also gets it.
	ActionJobReadPlans = identity.ActionJobReadPlans
	// ActionJobCancel allows canceling a job. Its object is a unit.
	ActionJobCancel = identity.ActionJobCancel
	// ActionJobApprove allows approving or rejecting a job that waits for
	// approval. Its object is a unit. The gate can also limit the approvers
	// to groups.
	ActionJobApprove = identity.ActionJobApprove
	// ActionNamespaceManageFreezes allows creating and ending the change
	// freezes of a namespace. Its object is a namespace.
	ActionNamespaceManageFreezes = identity.ActionNamespaceManageFreezes
	// ActionNamespaceBreakFreeze allows starting a job through an active
	// change freeze. Its object is a namespace. It is in another family than
	// ActionNamespaceManageFreezes, so a principal that can end a freeze
	// does not also get break-glass, and the reverse.
	ActionNamespaceBreakFreeze = identity.ActionNamespaceBreakFreeze
	// ActionNamespaceManageRunners allows creating and deleting the runner
	// pools of a namespace, and registering and removing runners. Its
	// object is a namespace.
	ActionNamespaceManageRunners = identity.ActionNamespaceManageRunners
	// ActionRunnerClaim allows a runner to claim the phases of its pool and
	// to report on them. Only a runner credential holds it. Its object is a
	// runner pool.
	ActionRunnerClaim = identity.ActionRunnerClaim
)

// Requirement is one action that a request needs, on one object.
type Requirement struct {
	Action identity.Action
	Object identity.Object
}

// StartRequirements returns the actions that the start of spec needs: the
// action of its executor and operation on the unit, and, when bg is not
// nil, ActionNamespaceBreakFreeze on the namespace of the unit. It returns
// ErrUnsupported for a write-back of an operation other than apply, and
// ErrInvalid for an unknown executor or operation, or a unit object that is
// not a unit.
func StartRequirements(spec Spec, bg *BreakGlass) ([]Requirement, error) {
	if !spec.Unit.IsUnit() {
		return nil, fmt.Errorf("%w: the object of a job must be a unit", ErrInvalid)
	}

	action, err := startActionFor(spec.Executor, spec.Operation, spec.Purpose)
	if err != nil {
		return nil, err
	}

	reqs := []Requirement{{Action: action, Object: spec.Unit}}

	if bg != nil {
		reqs = append(reqs, Requirement{
			Action: ActionNamespaceBreakFreeze,
			Object: identity.NamespaceObject(spec.Unit.Namespace()),
		})
	}

	return reqs, nil
}

// RequestRequirements returns the extra actions that the fields of req need,
// apart from the start action of its operation. A request that names a runner
// pool needs ActionNamespaceManageRunners on the namespace of the unit, and a
// request that names a code location needs ActionUnitDefineSteps on the unit:
// choosing the hosts and choosing the code are choosing what runs where. A
// request that names neither needs no extra action, and the server takes the
// default pool and the code location of the unit.
func RequestRequirements(req *StartRequest, unit identity.Object) []Requirement {
	var reqs []Requirement

	if req.Pool != "" {
		reqs = append(reqs, Requirement{
			Action: ActionNamespaceManageRunners, Object: identity.NamespaceObject(unit.Namespace()),
		})
	}

	if !req.Location.IsZero() {
		reqs = append(reqs, Requirement{Action: ActionUnitDefineSteps, Object: unit})
	}

	return reqs
}

// startActionFor returns the action that starts a job of op and purpose p
// through k. A drift check needs the action of a plan, because it changes
// nothing: it returns ErrUnsupported for a drift check through write-back and
// ErrInvalid for a drift check of an operation other than refresh.
func startActionFor(k ExecutorKind, op Operation, p Purpose) (identity.Action, error) {
	if p != PurposeDriftCheck {
		return startAction(k, op)
	}

	if _, err := startAction(k, op); err != nil {
		return "", err
	}

	switch {
	case k != ExecutorRunner:
		return "", fmt.Errorf("%w: a %s runs on a runner, not through %s", ErrUnsupported, p, k)
	case op != OperationRefresh:
		return "", fmt.Errorf("%w: a %s has the operation %s, not %s", ErrInvalid, p, OperationRefresh, op)
	default:
		return ActionUnitRunPlan, nil
	}
}

// startAction returns the action that starts a job of op through k.
func startAction(k ExecutorKind, op Operation) (identity.Action, error) {
	if !op.Valid() {
		return "", fmt.Errorf("%w: unknown operation %q", ErrInvalid, op)
	}

	switch k {
	case ExecutorWriteBack:
		if op != OperationApply {
			return "", fmt.Errorf("%w: write-back does %s only, not %s", ErrUnsupported, OperationApply, op)
		}

		return ActionUnitWriteBack, nil
	case ExecutorRunner:
		switch op {
		case OperationPlan:
			return ActionUnitRunPlan, nil
		case OperationApply, OperationRefresh:
			return ActionUnitRunApply, nil
		case OperationDestroy:
			return ActionUnitRunDestroy, nil
		default:
			return "", fmt.Errorf("%w: unknown operation %q", ErrInvalid, op)
		}
	default:
		return "", fmt.Errorf("%w: unknown executor %q", ErrInvalid, k)
	}
}
