package job

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Change is one transition of a job: from the version that the caller
// read, by an event, to the next state.
type Change struct {
	Job     ID
	Version int64
	Event   Event
	// Wait is the new wait reason of a job that stays queued.
	Wait WaitReason
	// Reason is the reason of a terminal state.
	Reason string
	// Dispatch is set on EventDispatch: the store starts the durable
	// operation in the transaction of the change. Its key is PhaseKey.
	Dispatch *durable.StartRequest
	// PlanNotBefore is now minus the maximum plan age, on EventDispatch of
	// an apply phase after a plan phase. A plan made before it is stale.
	PlanNotBefore time.Time
	// Executor names the runner or the instance, on EventClaim.
	Executor string
	// Delivery is set on EventDelivered: the store keeps it in the job.
	Delivery *DeliveryResult
	// MergedAt is set on EventMerged: the time of the merge, as the forge
	// reported it. The store keeps it in the delivery of the job.
	MergedAt time.Time
	// Approval is set when the change records an approval.
	Approval *Approval
}

// Filter selects jobs for List. A zero field matches every job. Limit 0
// takes the default page size of the adapter.
type Filter struct {
	Namespace namespace.Name
	// Unit selects the jobs of one unit, by its stable unit ID.
	UnitID string
	States []State
	Limit  int
	// After is the ID of the last job of the previous page.
	After ID
}

// Store is the persistence port for jobs. Each SQL store adapter implements
// it in its own SQL and runs the conformance suite in package jobtest.
//
// A job is data of its namespace: the backup of the namespace holds it, and
// every write checks fence and appends its audit entries in its
// transaction. The durable operations of its phases are control data, which
// a restore keeps. After a restore, the reconcile schedule fails each job
// that Orphans returns. A phase that changes resources never dispatches
// again on its own.
//
// One change at a time on a unit: a SQL adapter holds the rule with a
// partial unique index on the unit ID of the jobs table, over the rows
// whose state holds the unit (State.HoldsUnit) and whose current phase
// changes (PhaseKind.Changes). A second dispatch then fails on the index in
// its own transaction, and the adapter returns ErrUnitBusy.
//
// Every method that writes returns namespace.ErrFenced for a stale fence,
// and changes nothing on an error: its audit entries roll back with it.
type Store interface {
	// Create persists j as queued, with e, and returns it. When dispatch is
	// not nil, the job starts dispatched and the store starts the durable
	// operation in the same transaction, with the checks of a dispatch in
	// Apply: ErrUnitBusy and ErrFrozen. A second create with the key of j
	// returns the first job and created false, when the spec is equal. It
	// returns ErrKeyConflict when the key names a job with another spec,
	// and ErrLimit when the unit has MaxOpenPerUnit jobs that are not
	// terminal, or the namespace MaxOpenPerNamespace.
	Create(ctx context.Context, j Job, dispatch *durable.StartRequest, fence namespace.Fence,
		e []audit.Entry) (stored Job, created bool, err error)
	// Get returns the job id, or ErrNotFound.
	Get(ctx context.Context, id ID) (Job, error)
	// List returns the jobs that f selects, newest first.
	List(ctx context.Context, f Filter) ([]Job, error)
	// Apply makes c with Next, and appends e, in one transaction. It returns
	// ErrNotFound for an unknown job, ErrConflict when the job is not at
	// c.Version, and the error of Next for an event that the state does not
	// allow. It checks these rules in the transaction of the change:
	//   - EventDispatch of a phase that changes resources: ErrUnitBusy when
	//     another job holds the unit, and ErrFrozen when a freeze of the
	//     namespace is active and the job has no break-glass.
	//   - EventDispatch of an apply phase after a plan phase: the error of
	//     CheckPlan with the current state version of the unit and
	//     c.PlanNotBefore. ErrPlanStale leaves the job queued, and the
	//     caller applies EventReplan.
	//   - EventReplan: Current moves back to the plan phase before it, and
	//     the plans of that phase and the approvals of the next phase are
	//     cleared. The records of the old plans stay for retention.
	//   - EventCancel of a job with a live durable operation: it applies
	//     durable.Store.Cancel to the operation.
	//   - EventDelivered of a review request: it starts the operation of
	//     KindReviewWatch.
	//   - EventCancel, EventRejected or EventExpire of a job that delivered:
	//     it starts the operation of KindAbandon.
	Apply(ctx context.Context, c Change, fence namespace.Fence, e []audit.Entry) (Job, error)
	// ObserveStateVersion closes each write-back job of v.Unit that waits for
	// a state version, when v.Final is true. A direct push must deliver
	// before v.At. A review request must have a known merge before v.At:
	// with no merge yet, the job waits. It applies EventStateObserved with
	// v as the closing version, sets the Verification to pending, and starts
	// the operation of KindVerify for each closed job. It returns the closed
	// jobs, none when v is not final or no job waits. A tool package never
	// calls it: the store adapter applies its rule in the transaction of
	// each state write of any tool, so a state write and the close commit
	// together. The verify operation asks the forge later, outside that
	// transaction.
	ObserveStateVersion(ctx context.Context, v StateVersion, fence namespace.Fence) ([]Job, error)
	// SetVerification records the result of the verify operation of the
	// closed job id, with e. It returns ErrNotFound for an unknown job,
	// ErrInvalid for a job with no closing state version or for v pending,
	// and changes nothing when the job has v already.
	SetVerification(ctx context.Context, id ID, v Verification, fence namespace.Fence, e []audit.Entry) error
	// Orphans returns at most limit jobs that are dispatched, running or
	// canceling, but whose durable operation does not exist or ended. A
	// restore makes them. The reconcile schedule fails each one with the
	// reason restored.
	Orphans(ctx context.Context, limit int) ([]Job, error)
}

// StartRequest is what a caller gives to start a job. The REST API and
// every feature of the server that starts a job use it, through Service.
type StartRequest struct {
	// Unit is the unit reference.
	Unit      identity.Object
	Operation Operation
	// Executor is empty to take the executor that the unit settings name.
	Executor ExecutorKind
	// Location is empty to take the code location of the unit. Revision may
	// stay empty: the server resolves the head of the ref at start.
	Location Location
	// Pool is empty to take the pool of the unit settings. It must be a
	// pool of the namespace of the unit. A request has no steps: the server
	// takes them from the settings of the unit, its blueprint or its tool,
	// and only unit:define-steps changes those.
	Pool string
	// Edits and Delivery are for a write-back job.
	Edits    []FileEdit
	Delivery Delivery
	// IdempotencyKey makes a retry of the start safe: two starts with one key
	// and one spec give one job. Empty makes a new job each time.
	IdempotencyKey string
	// Reason is the text that the caller gives for the change. The audit
	// entry of the start holds it.
	Reason string
	// BreakGlass starts the job through an active freeze. It needs the
	// break-glass action on the namespace, and a person: an agent cannot
	// ask for it.
	BreakGlass *BreakGlass
	// Channel is where the request came from, for the audit entry.
	Channel audit.Channel
}

// Service is what a caller uses to start and drive a job. The REST API
// adapter and every feature of the server that starts a job call it. Dusty
// runs in the server, as an agent loop on durable operations, and calls it
// directly with an Actor that names the user who asked and the agent. The
// actions of the user decide. The service checks the actions, the freezes
// and the policy, writes the audit entries, and calls the Executor and the
// Store.
//
// Every method that takes a job ID returns ErrNotFound when the principal
// of a lacks job:read on the unit of the job, so that a caller cannot
// learn that the job exists. With job:read, a missing action of the
// operation (see StartRequirements) returns an error that wraps
// identity.ErrDenied.
type Service interface {
	// Start checks req, decides, and creates the job. It returns ErrInvalid
	// for a request that is not valid, ErrUnsupported for an operation that
	// the executor cannot do, ErrPolicyDenied with the reason of the policy,
	// ErrFrozen when a freeze is active and req has no break-glass,
	// ErrAgentDenied for a break-glass that an agent asks for, ErrLimit
	// above a limit of open jobs, and ErrKeyConflict for a key that names
	// another spec. The job may stay queued with a wait reason, or wait for
	// approval.
	Start(ctx context.Context, a Actor, req StartRequest) (Job, error)
	// Get returns the job id, or ErrNotFound.
	Get(ctx context.Context, a Actor, id ID) (Job, error)
	// Cancel asks to stop the job id. A job with no executor ends canceled
	// at once. A running job goes to canceling, and its executor stops at
	// its next write. A second cancel changes nothing. It returns the error
	// of CheckCancel: ErrFinished for a job that already ended, and
	// ErrDelivered for a write-back job whose change is in git for good.
	Cancel(ctx context.Context, a Actor, id ID, reason string) (Job, error)
	// Approve records the verdict of a on the gate of the current phase.
	// plan is the binding of the plans that the approver saw. It returns
	// ErrTransition when the job does not wait for approval,
	// ErrAgentDenied for an approval that an agent records (an agent can
	// record a rejection), ErrSelfApproval for an approval by the starter
	// that the gate does not allow, also when the starter used an agent,
	// ErrPlanChanged when plan is not the binding of the stored plans,
	// ErrPlanStale for plans older than the maximum plan age, and
	// ErrFinished for a job that ended.
	Approve(ctx context.Context, a Actor, id ID, v Verdict, plan PlanBinding, comment string) (Job, error)
}
