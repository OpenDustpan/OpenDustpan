// Package job defines jobs: work that OpenDustpan asks an executor to do on
// one unit. A job plans, applies or destroys the resources of a unit, or
// writes a change to the code location of the unit in git.
//
// A job belongs to exactly one unit, named by its unit reference: the tool,
// the namespace and the tool's own name. The package never reads the model
// of a tool. Each tool package gives the default steps of its operations
// through the StepPlanner port, and reports its state versions through the
// rule of Store.ObserveStateVersion.
//
// A job is a list of phases. An executor runs one phase at a time. Each
// dispatch of a phase is one durable operation, so the claim, its expiry,
// the fencing token, the retry and the cancel of a phase are the rules of
// package durable. The job record holds what a person reads: the state of
// the job, its phases, its steps, its plans and its logs. Gates between
// phases (approvals, change freezes, one change at a time on a unit) are
// rules of this package.
//
// Two executors implement the Executor port. The write-back executor
// commits a change to git through a forge, and the team's own CI applies
// it. The job ends when the new state version of the unit arrives. A runner
// is a process that the team hosts. It claims phases and runs their steps
// as argument lists, with no shell.
//
// The package is tool-neutral. It never imports a store adapter, a forge
// adapter or a transport.
package job

import (
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

var (
	// ErrInvalid reports a request with an empty or bad field.
	ErrInvalid = errors.New("job request is not valid")
	// ErrNotFound reports a job ID that the store does not know.
	ErrNotFound = errors.New("job not found")
	// ErrKeyConflict reports a start whose idempotency key names a job with
	// another spec.
	ErrKeyConflict = errors.New("idempotency key names a job with another spec")
	// ErrTransition reports an event that the state machine does not allow
	// in the current state of the job.
	ErrTransition = errors.New("job state does not allow this event")
	// ErrConflict reports a write that read an older version of the job.
	// The caller reads the job again and decides again.
	ErrConflict = errors.New("job changed since it was read")
	// ErrUnsupported reports an operation that the executor of the job
	// cannot do, for example a destroy through write-back.
	ErrUnsupported = errors.New("executor does not support this operation")
	// ErrUnitBusy reports a dispatch of a phase that changes resources while
	// another job holds the unit for a change.
	ErrUnitBusy = errors.New("another job holds the unit for a change")
	// ErrFrozen reports a start or a dispatch of a phase that changes
	// resources while a change freeze of the namespace is active, without
	// break-glass.
	ErrFrozen = errors.New("a change freeze of the namespace is active")
	// ErrPolicyDenied reports a start that the policy decision denied.
	ErrPolicyDenied = errors.New("policy denied the job")
	// ErrFinished reports a cancel or an approval of a job that already
	// ended.
	ErrFinished = errors.New("job already ended")
	// ErrSelfApproval reports an approval by the principal that started the
	// job, when the decision does not allow it.
	ErrSelfApproval = errors.New("the principal that started the job cannot approve it")
	// ErrPlanChanged reports an approval or an apply for a plan hash that is
	// not the hash of the stored plan.
	ErrPlanChanged = errors.New("plan changed since the approval")
	// ErrClaimLost reports a write by an executor whose claim on the phase is
	// no longer live. It wraps durable.ErrClaimLost.
	ErrClaimLost = errors.New("claim on the job phase is lost")
	// ErrCanceled reports a write by an executor to a phase that a cancel
	// stops. It wraps durable.ErrCanceled.
	ErrCanceled = errors.New("job is canceled")
)

// ID identifies a job. The service makes it at start, with the prefix job-,
// before it builds the start keys of the durable operations of the job.
type ID string

// IDPrefix starts every job ID.
const IDPrefix = "job-"

// Operation is the tool-neutral verb of a job. Each tool maps it to its own
// command: a Pulumi preview, up, destroy or refresh, or an OpenTofu plan,
// apply, apply -destroy or apply -refresh-only. User-facing text keeps the
// words of the tool.
type Operation string

// The operations of a job.
const (
	// OperationPlan shows the changes and changes no resource.
	OperationPlan Operation = "plan"
	// OperationApply changes resources to match the program and its config.
	OperationApply Operation = "apply"
	// OperationDestroy deletes the resources of the unit.
	OperationDestroy Operation = "destroy"
	// OperationRefresh reads the resources and writes a new state version.
	// It changes no resource, but it writes state.
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

// WritesState reports whether a job of the operation can write a state
// version of the unit. Only a plan cannot.
func (o Operation) WritesState() bool {
	return o.Valid() && o != OperationPlan
}

// ExecutorKind names the executor of a job.
type ExecutorKind string

// The executors of a job.
const (
	// ExecutorWriteBack commits a change to the code location of the unit, and the
	// team's own CI applies it.
	ExecutorWriteBack ExecutorKind = "write-back"
	// ExecutorRunner dispatches the phases to a runner that the team hosts.
	ExecutorRunner ExecutorKind = "runner"
)

// Valid reports whether k is one of the executors of this package.
func (k ExecutorKind) Valid() bool {
	return k == ExecutorWriteBack || k == ExecutorRunner
}

// PhaseKind names what one phase of a job does.
type PhaseKind string

// The kinds of phase.
const (
	// PhasePlan runs the plan steps on a runner and stores the plan. It
	// changes no resource.
	PhasePlan PhaseKind = "plan"
	// PhaseApply runs the steps that change resources or write state, on a
	// runner.
	PhaseApply PhaseKind = "apply"
	// PhaseDeliver commits the change of a write-back job to its code location:
	// a direct push or a review request.
	PhaseDeliver PhaseKind = "deliver"
)

// Changes reports whether a phase of kind k changes resources, state or
// the code in git. A freeze and the one-change rule of a unit gate only such
// phases.
func (k PhaseKind) Changes() bool {
	return k == PhaseApply || k == PhaseDeliver
}

// Location names where the program and the config of a unit are in git: a
// forge connection, a repository, a ref and a directory. Revision is the commit
// that a runner checks out. A start may leave it empty, and the server then
// resolves the head of Ref at start, so that every phase of the job runs on
// one commit.
type Location struct {
	Connection string
	Repository string
	Ref        string
	Directory  string
	Revision   string
}

// Delivery names how a write-back job delivers its commit.
type Delivery string

// The deliveries of a write-back job.
const (
	// DeliveryReviewRequest commits to a new branch and opens a review
	// request. It is the default.
	DeliveryReviewRequest Delivery = "review-request"
	// DeliveryDirectPush commits to the ref of the code location. The branch
	// protection of the forge decides whether the push is allowed.
	DeliveryDirectPush Delivery = "direct-push"
)

// FileEdit is one change to one structured file at the code location of a
// write-back job: a set of keys and their new values. The executor reads the
// file at the base commit, changes only these keys, and refuses the edit
// when a key changed after BaseRevision.
type FileEdit struct {
	// Path is relative to the directory of the code location.
	Path string
	// Keys maps a key path in the file to its new value as JSON. A null value
	// deletes the key.
	Keys map[string][]byte
}

// Spec is what a job does. The server fixes it at start: a later change of
// the unit settings, a blueprint or a profile does not change a job that
// exists.
type Spec struct {
	// Unit is the unit reference, with the stable unit ID set
	// (identity.Object.WithUnitID).
	Unit      identity.Object
	Operation Operation
	Executor  ExecutorKind
	Location  Location
	// Steps are the steps of a runner job, resolved at start from the
	// settings of the unit, its blueprint or the defaults of its tool.
	// A write-back job has no steps.
	Steps []Step
	// Pool names the runner pool of a runner job.
	Pool string
	// Edits are the file edits of a write-back job.
	Edits []FileEdit
	// Delivery is the delivery of a write-back job.
	Delivery Delivery
}

// BreakGlass records a start through an active change freeze. Reason is
// the text that the principal gave. It is in the audit entry of the start.
type BreakGlass struct {
	Reason string
	Freeze string
}

// Job is what any instance can read about a job.
type Job struct {
	ID    ID
	Spec  Spec
	State State
	// Wait tells why a queued job does not dispatch now. It is empty when
	// the job is not queued, or when nothing holds it.
	Wait WaitReason
	// Reason tells why a terminal job ended, for example the reason of a
	// failure.
	Reason string
	// Phases are the phases of the job, in order. Executor.Phases fixes
	// them at start.
	Phases []Phase
	// Current is the index in Phases of the phase that runs or runs next.
	// It equals len(Phases) after the last phase.
	Current int
	// StartedBy is the subject of the principal that started the job.
	StartedBy string
	// BreakGlass is set when the job started through an active freeze.
	BreakGlass *BreakGlass
	// Delivery is the result of the deliver phase of a write-back job.
	Delivery *DeliveryResult
	// Closed is the state version that ended a write-back job.
	Closed *StateVersion
	// IdempotencyKey is the key of the start. Two starts with one key give
	// one job.
	IdempotencyKey string
	// Version grows on each change of the job record. A write names the
	// version that it read, so a write on a stale read fails with
	// ErrConflict.
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Phase is one phase of a job and its record.
type Phase struct {
	Kind PhaseKind
	// Operation is the durable operation of the last dispatch of the phase,
	// or empty before the first dispatch.
	Operation string
	// Dispatches counts the dispatches of the phase. A release of an
	// executor that did not start a step that changes resources dispatches
	// the phase again.
	Dispatches int
	// Executor names the runner or the instance that holds or held the
	// claim of the last dispatch.
	Executor string
	// Steps are the recorded steps of the phase, in order.
	Steps []StepRecord
	// Plan is the stored plan of a plan phase.
	Plan *PlanRef
	// Gate is the gate in front of the phase, evaluated before its
	// dispatch.
	Gate Gate
	// Approvals are the approvals that the gate got.
	Approvals  []Approval
	StartedAt  time.Time
	FinishedAt time.Time
}

// DeliveryResult is what the deliver phase of a write-back job wrote.
type DeliveryResult struct {
	Delivery Delivery
	// Commit is the commit that the executor made.
	Commit string
	// Branch is the branch of the commit.
	Branch string
	// ReviewRequest is the URL of the review request, or empty for a direct
	// push.
	ReviewRequest string
	// MergedAt is the time that the forge reported a merge of the review
	// request, or the zero time.
	MergedAt    time.Time
	DeliveredAt time.Time
}

// StateVersion is a new state version of a unit, as the tool package that
// wrote it reports it. Commit is the commit that the tool reported with the
// write, or empty when the tool reported none.
type StateVersion struct {
	Unit      identity.Object
	VersionID string
	Commit    string
	At        time.Time
	// Verified is true when Commit contains the commit of the delivery. A
	// write without a commit closes the job unverified.
	Verified bool
}
