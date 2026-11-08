package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

// Errors of the change modes and of the Changes port.
var (
	// ErrDirectNotAllowed reports a direct change when the server setting,
	// the namespace opt-in or the person opt-in for direct changes is not
	// set, and every direct destroy.
	ErrDirectNotAllowed = errors.New("direct changes by agents are not allowed here")
	// ErrModeNotSupported reports a mode or an executor that the unit does
	// not support, for example a review request for a unit with no code
	// location.
	ErrModeNotSupported = errors.New("the unit does not support this change")
	// ErrChangeFrozen reports a change that a change freeze stops. An agent
	// never asks for the break-glass of a freeze.
	ErrChangeFrozen = errors.New("a change freeze stops changes to this unit")
	// ErrChangePolicyDenied reports a change that the policy of the job
	// model denies.
	ErrChangePolicyDenied = errors.New("policy denied the change")
	// ErrChangeNotFound reports a change ID that the job model does not
	// know, or a change that the actor cannot read.
	ErrChangeNotFound = errors.New("change not found")
	// ErrChangeKeyConflict reports a start whose key names a change with
	// another request.
	ErrChangeKeyConflict = errors.New("change key names another change")
	// ErrChangeFinished reports a cancel of a change that already ended.
	ErrChangeFinished = errors.New("change already ended")
	// ErrProposeAfterReveal reports a propose by a run that read secret
	// values in plain text.
	ErrProposeAfterReveal = errors.New("a run that read secret values cannot propose a change")
)

// Operation is the tool-neutral verb of a change. The values are the
// operations of the job model.
type Operation string

// The operations of a change.
const (
	OperationPlan    Operation = "plan"
	OperationApply   Operation = "apply"
	OperationDestroy Operation = "destroy"
	OperationRefresh Operation = "refresh"
)

// Valid reports whether o is one of the operations of this package.
func (o Operation) Valid() bool {
	switch o {
	case OperationPlan, OperationApply, OperationDestroy, OperationRefresh:
		return true
	default:
		return false
	}
}

// Executor names how a change runs. The values are the executors of the job
// model.
type Executor string

// The executors of a change.
const (
	// ExecutorWriteBack commits the change to the code location of the unit.
	ExecutorWriteBack Executor = "write-back"
	// ExecutorRunner runs the change on a runner that the team hosts.
	ExecutorRunner Executor = "runner"
)

// Delivery is how a write-back change reaches the forge. The values are the
// deliveries of the job model.
type Delivery string

// The deliveries of a write-back change.
const (
	DeliveryReviewRequest Delivery = "review-request"
	DeliveryDirectPush    Delivery = "direct-push"
)

// ChangeMode is how a proposed change goes in.
type ChangeMode string

// The change modes.
const (
	// ModeReviewRequest writes the change back to the code location of the
	// unit as a review request, never as a direct push. A person merges it,
	// and the team's own path applies it.
	ModeReviewRequest ChangeMode = "review-request"
	// ModeApproveFirst makes a job whose phases that change wait at the
	// confirmation gate for the person who asked.
	ModeApproveFirst ChangeMode = "approve-first"
	// ModeDirect makes a runner job that applies with no confirmation, or a
	// write-back job that pushes directly after the confirmation of its
	// delivery. Policies and change freezes still apply.
	ModeDirect ChangeMode = "direct"
)

// PhaseKind names a phase of a job. The values are the phase kinds of the
// job model.
type PhaseKind string

// The phase kinds that a confirmation gate can hold.
const (
	PhaseApply   PhaseKind = "apply"
	PhaseDeliver PhaseKind = "deliver"
)

// ChangePolicy holds the settings that decide the mode of a change by an
// agent. Direct changes need all three switches.
type ChangePolicy struct {
	// ServerDirect is the installation setting that allows direct changes
	// by agents. The default is off.
	ServerDirect bool
	// NamespaceDirect is the opt-in of the namespace of the unit.
	NamespaceDirect bool
	// UserDirect is the opt-in of the person of the call.
	UserDirect bool
}

// UnitSupport tells which executors a unit has, as the Changes port answers
// it. WriteBack is true when the unit has a code location on a connected
// forge. Runner is true when the unit names a runner pool.
type UnitSupport struct {
	WriteBack bool
	Runner    bool
}

// FileEdit is one change to one structured file at the code location of a
// unit: key paths and their new values as JSON. A null value deletes the
// key. It is the file edit of the job model.
type FileEdit struct {
	Path string                     `json:"path"`
	Keys map[string]json.RawMessage `json:"keys"`
}

// ChangeRequest asks the job model for a change. Key makes the request safe
// to repeat: the loop derives it from the run, the turn and the call, and
// the MCP endpoint from the session and the request ID. An empty Executor
// or Mode takes the default of ChangePolicy.Plan. A request never carries a
// break-glass of a change freeze, and has no field for one.
type ChangeRequest struct {
	Key       string
	Actor     Actor
	Unit      identity.Object
	Operation Operation
	Executor  Executor
	Mode      ChangeMode
	// Edits are the file edits of a write-back change.
	Edits []FileEdit
	// Reason is the text that the job model shows to the person at the
	// confirmation gate and to the reviewer, in plain words.
	Reason string
}

// ChangePlan is how the job model must start a change: the resolved mode,
// executor and delivery, and the phases that wait at the confirmation gate.
type ChangePlan struct {
	Mode     ChangeMode
	Executor Executor
	// Delivery is set for a write-back change only.
	Delivery Delivery
	// Confirm lists the phases that wait for the confirmation of the person
	// who asked. The deliver phase of a write-back change is always in it.
	Confirm []PhaseKind
}

// Plan returns the plan of req for a unit with support. The defaults are a
// write-back for an apply on a unit with a code location, a runner for the
// others, a review request for a write-back and approve-first for a runner.
// A direct change is never a default, and a destroy is never direct. It
// returns ErrInvalid for an unknown operation, executor or mode,
// ErrModeNotSupported and ErrDirectNotAllowed.
func (p ChangePolicy) Plan(support UnitSupport, req *ChangeRequest) (ChangePlan, error) {
	var zero ChangePlan

	if !req.Operation.Valid() {
		return zero, fmt.Errorf("%w: unknown operation %q", ErrInvalid, req.Operation)
	}

	exec, err := executorFor(support, req)
	if err != nil {
		return zero, err
	}

	mode, err := p.modeFor(exec, req)
	if err != nil {
		return zero, err
	}

	if exec == ExecutorWriteBack {
		delivery := DeliveryReviewRequest
		if mode == ModeDirect {
			delivery = DeliveryDirectPush
		}

		return ChangePlan{Mode: mode, Executor: exec, Delivery: delivery, Confirm: []PhaseKind{PhaseDeliver}}, nil
	}

	var confirm []PhaseKind
	if mode == ModeApproveFirst && req.Operation != OperationPlan {
		confirm = []PhaseKind{PhaseApply}
	}

	return ChangePlan{Mode: mode, Executor: exec, Delivery: "", Confirm: confirm}, nil
}

// executorFor returns the executor of req: the requested one, or the
// default for the unit.
func executorFor(support UnitSupport, req *ChangeRequest) (Executor, error) {
	exec := req.Executor
	if exec == "" {
		exec = ExecutorRunner
		if support.WriteBack && req.Operation == OperationApply {
			exec = ExecutorWriteBack
		}
	}

	switch exec {
	case ExecutorWriteBack:
		if !support.WriteBack || req.Operation != OperationApply {
			return "", fmt.Errorf("%w: write-back needs a code location and does apply only", ErrModeNotSupported)
		}
	case ExecutorRunner:
		if !support.Runner {
			return "", fmt.Errorf("%w: the unit has no runner pool", ErrModeNotSupported)
		}
	default:
		return "", fmt.Errorf("%w: unknown executor %q", ErrInvalid, exec)
	}

	return exec, nil
}

// modeFor returns the mode of req on exec: the requested one, or the
// default of the executor.
func (p ChangePolicy) modeFor(exec Executor, req *ChangeRequest) (ChangeMode, error) {
	switch req.Mode {
	case "":
		if exec == ExecutorWriteBack {
			return ModeReviewRequest, nil
		}

		return ModeApproveFirst, nil
	case ModeReviewRequest:
		if exec != ExecutorWriteBack {
			return "", fmt.Errorf("%w: a review request needs write-back", ErrModeNotSupported)
		}

		return ModeReviewRequest, nil
	case ModeApproveFirst:
		return ModeApproveFirst, nil
	case ModeDirect:
		if !p.ServerDirect || !p.NamespaceDirect || !p.UserDirect || req.Operation == OperationDestroy {
			return "", ErrDirectNotAllowed
		}

		return ModeDirect, nil
	default:
		return "", fmt.Errorf("%w: unknown change mode %q", ErrInvalid, req.Mode)
	}
}

// ChangeID identifies a change. It is the ID of its job.
type ChangeID string

// ChangeState is the state of a change: the state of its job.
type ChangeState string

// The states of a change, the same as the states of a job. Succeeded,
// failed, canceled, rejected and expired are terminal.
const (
	ChangeQueued           ChangeState = "queued"
	ChangeAwaitingApproval ChangeState = "awaiting-approval"
	ChangeDispatched       ChangeState = "dispatched"
	ChangeRunning          ChangeState = "running"
	ChangeCanceling        ChangeState = "canceling"
	ChangeAwaitingState    ChangeState = "awaiting-state"
	ChangeSucceeded        ChangeState = "succeeded"
	ChangeFailed           ChangeState = "failed"
	ChangeCanceled         ChangeState = "canceled"
	ChangeRejected         ChangeState = "rejected"
	ChangeExpired          ChangeState = "expired"
)

// ChangeStates lists every state of a change, in the order of the job
// model.
func ChangeStates() []ChangeState {
	return []ChangeState{
		ChangeQueued, ChangeAwaitingApproval, ChangeDispatched, ChangeRunning, ChangeCanceling,
		ChangeAwaitingState, ChangeSucceeded, ChangeFailed, ChangeCanceled, ChangeRejected, ChangeExpired,
	}
}

// Terminal reports whether s is an end state.
func (s ChangeState) Terminal() bool {
	return slices.Contains(
		[]ChangeState{ChangeSucceeded, ChangeFailed, ChangeCanceled, ChangeRejected, ChangeExpired},
		s,
	)
}

// Change is what the job model answers about a change. AwaitingConfirmation
// is true while the current phase waits at the confirmation gate: State is
// then ChangeAwaitingApproval. ConfirmURL is the console page where the
// person who asked confirms, and ReviewURL the review request after the
// delivery, or empty. StartedBy is the subject of the person, and Agent the
// agent that acted for it.
type Change struct {
	ID                   ChangeID
	Key                  string
	Unit                 identity.Object
	Operation            Operation
	Plan                 ChangePlan
	State                ChangeState
	AwaitingConfirmation bool
	ConfirmURL           string
	ReviewURL            string
	StartedBy            string
	Agent                Ref
	CreatedAt            time.Time
}

// Changes is the port to the shared job model, for agents. The job model
// implements it on its Service: Start, Get and Cancel map to the methods of
// the same names, with the person of the actor as the principal and the
// agent of the actor recorded on the job. An agent calls it for every
// change: it never writes a unit, a code location or a setting another way.
// The port has no approve and no break-glass: an agent never records an
// approve verdict and never passes a change freeze. The job model refuses an
// approve verdict that carries an agent, and accepts a reject verdict. Each implementation runs the
// conformance suite in package agenttest.
type Changes interface {
	// Support returns the executors of unit, for the actor. It returns an
	// error that wraps identity.ErrDenied when the actor cannot read the
	// jobs of the unit.
	Support(ctx context.Context, actor Actor, unit identity.Object) (UnitSupport, error)
	// StartActions returns the actions that the start of plan for req
	// needs on req.Unit, as the start requirements of the job model give
	// them, with no break-glass. req must not be nil. The method does not
	// change req and does not keep it after it returns. The Toolset checks
	// each one for the person before Start, and the audit entry of the call
	// names them. It returns ErrInvalid and ErrModeNotSupported.
	StartActions(req *ChangeRequest, plan ChangePlan) ([]identity.Action, error)
	// Start resolves req with ChangePolicy.Plan, checks the actions of the
	// start for the person, puts the confirmation gate in front of each
	// phase of the plan, and starts the job. req must not be nil. The method
	// does not change req and does not keep it after it returns. It returns
	// the change that req.Key already names when the request is the same.
	// The confirmation gate holds even when no policy is installed, and only
	// the person who asked passes it, in the console, with a fresh MFA step.
	// It returns ErrInvalid, ErrServicePrincipal for an actor with
	// ServicePrincipal (nobody could pass its confirmation gate),
	// ErrModeNotSupported, ErrDirectNotAllowed, ErrChangeFrozen,
	// ErrChangePolicyDenied, ErrChangeKeyConflict, and an error that wraps
	// identity.ErrDenied when the person does not hold an action of the start.
	Start(ctx context.Context, req *ChangeRequest) (Change, error)
	// Get returns the change id. It returns ErrChangeNotFound, also for a
	// change that the person of the actor cannot read.
	Get(ctx context.Context, actor Actor, id ChangeID) (Change, error)
	// Cancel asks to stop the change id and returns it. A change with no
	// executor ends canceled at once. A running change goes to canceling,
	// and a second cancel of it changes nothing. It returns
	// ErrChangeNotFound, also for a change that the person cannot read,
	// ErrChangeFinished for a change that ended, also a canceled one, and an
	// error that wraps identity.ErrDenied when the person can read the
	// change but not cancel it.
	Cancel(ctx context.Context, actor Actor, id ChangeID, reason string) (Change, error)
}
