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
	ActionUnitRunPlan identity.Action = "unit:run-plan"
	// ActionUnitRunApply allows starting a runner job that applies or
	// refreshes. Its object is a unit.
	ActionUnitRunApply identity.Action = "unit:run-apply"
	// ActionUnitRunDestroy allows starting a runner job that destroys the
	// resources of a unit. Its object is a unit.
	ActionUnitRunDestroy identity.Action = "unit:run-destroy"
	// ActionUnitWriteBack allows starting a write-back job: a commit to the
	// code location of a unit. Its object is a unit.
	ActionUnitWriteBack identity.Action = "unit:write-back"
	// ActionUnitDefineSteps allows changing the custom steps of a unit in
	// its settings, or of a blueprint. Custom steps run on the runners of
	// the namespace, so their change is its own action, apart from the
	// start of a job. Its object is a unit, or the blueprint.
	ActionUnitDefineSteps identity.Action = "unit:define-steps"
	// ActionJobRead allows reading the jobs of a unit: the spec, the state,
	// the phases, the steps, the approvals and the metadata of plans and
	// logs. Its object is a unit, or the namespace for a list.
	ActionJobRead identity.Action = "job:read"
	// ActionJobReadLogs allows reading the content of job logs. Its object
	// is a unit.
	ActionJobReadLogs identity.Action = "job:read-logs"
	// ActionJobReadPlans allows reading the content of stored plans, with
	// the values that the tool marks as sensitive masked. The unmasked plan
	// also needs identity.ActionUnitReadSecrets. Its object is a unit.
	ActionJobReadPlans identity.Action = "job:read-plans"
	// ActionJobCancel allows canceling a job. Its object is a unit.
	ActionJobCancel identity.Action = "job:cancel"
	// ActionJobApprove allows approving or rejecting a job that waits for
	// approval. Its object is a unit. The gate can also limit the approvers
	// to groups.
	ActionJobApprove identity.Action = "job:approve"
	// ActionJobRedactLogs allows writing a redacted version of a job log and
	// shredding the older version. It cannot be undone. Its object is a
	// unit.
	ActionJobRedactLogs identity.Action = "job:redact-logs"
	// ActionNamespaceManageFreezes allows creating and ending the change
	// freezes of a namespace. Its object is a namespace.
	ActionNamespaceManageFreezes identity.Action = "namespace:manage-freezes"
	// ActionNamespaceBreakFreeze allows starting a job through an active
	// change freeze. Its object is a namespace. It is in another family than
	// ActionNamespaceManageFreezes, so a principal that can end a freeze
	// does not also get break-glass, and the reverse.
	ActionNamespaceBreakFreeze identity.Action = "namespace:break-freeze"
	// ActionNamespaceManageRunners allows creating and deleting the runner
	// pools of a namespace, and registering and removing runners. Its
	// object is a namespace.
	ActionNamespaceManageRunners identity.Action = "namespace:manage-runners"
	// ActionRunnerClaim allows a runner to claim the phases of its pool and
	// to report on them. Only a runner credential holds it. Its object is a
	// runner pool.
	ActionRunnerClaim identity.Action = "runner:claim"
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

	action, err := startAction(spec.Executor, spec.Operation)
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
