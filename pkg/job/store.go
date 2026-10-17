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
// a restore keeps. The reconcile schedule applies AfterOperationEnded to
// each job that Orphans returns. A phase that changes resources never
// dispatches again on its own.
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
	// Create persists j as queued with its wait reason j.Wait, with e, and
	// returns it. When dispatch is not nil, the job starts dispatched and
	// the store starts the durable operation in the same transaction, with
	// the checks of a dispatch in Apply: ErrUnitBusy and ErrFrozen. Such an
	// error stores nothing. Service.Start then calls Create again with no
	// dispatch and j.Wait set to WaitUnitBusy, and the reconcile schedule
	// dispatches the job when the unit is free. A second create with the
	// key of j returns the first job and created false, when the spec is
	// equal. It returns ErrKeyConflict when the key names a job with
	// another spec, and ErrLimit when the unit has MaxOpenPerUnit jobs that
	// are not terminal, or the namespace MaxOpenPerNamespace.
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
	//     CheckPlan with the plan of that phase, the binding of each
	//     approval of the apply phase, the current state version of the
	//     unit and c.PlanNotBefore. A plan phase that stored no plan is
	//     ErrPlanStale too. ErrPlanStale leaves the job queued, and the
	//     caller applies EventReplan.
	//   - EventClaim of a plan phase: it sets Phase.StateVersion to the
	//     current state version of the unit, at each claim.
	//   - EventReplan: Current moves back to the plan phase before it, and
	//     the plan of that phase and the approvals of the next phase are
	//     cleared. The old plan stays as a history item until its
	//     retention ends.
	//   - EventCancel of a job with a live durable operation: it applies
	//     durable.Store.Cancel to the operation.
	//   - EventMerged and EventDelivered: when the last final state version
	//     of the unit that ObserveStateVersion recorded closes the job
	//     (Job.ClosedBy), it also applies EventStateObserved with that
	//     version, sets the Verification to pending and starts the
	//     operation of KindVerify, as ObserveStateVersion does. So a merge
	//     that the review watcher sees after the state version, or a
	//     commit that a takeover finds by its trailer after the state
	//     version, still closes the job.
	//   - EventDelivered of a review request that stays in awaiting-state:
	//     it starts the operation of KindReviewWatch.
	//   - EventCancel, EventRejected or EventExpire of a job that delivered:
	//     it starts the operation of KindAbandon.
	Apply(ctx context.Context, c Change, fence namespace.Fence, e []audit.Entry) (Job, error)
	// ObserveStateVersion records v as the last final state version of
	// v.Unit when v.Final is true, also when no job waits, and closes each
	// write-back job of the unit that v closes (Job.ClosedBy): a direct
	// push must deliver at or before v.At, and a review request must have
	// a known merge at or before v.At. With no merge yet, the job waits,
	// and EventMerged closes it later on the recorded version. It applies
	// EventStateObserved with v as the closing version, sets the
	// Verification to pending, and starts the operation of KindVerify for
	// each closed job. It returns the closed jobs, none when v is not final
	// or no job waits. A tool package never
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
	// canceling, but whose durable operation does not exist or ended with
	// no Finish. A restore makes the first kind. A claim that expires
	// during a cancel, or a durable failure at the limit of expired
	// claims, makes the second. The reconcile schedule applies the event of
	// AfterOperationEnded to each one: a change that began fails the job,
	// and a canceling job with no change ends canceled.
	Orphans(ctx context.Context, limit int) ([]Orphan, error)
}

// Orphan is a job that Store.Orphans returns. Missing is true when the
// operation of its current phase does not exist, and false when it ended.
type Orphan struct {
	Job     Job
	Missing bool
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
	// Start checks req, decides, and creates the job. For an agent, it
	// applies CheckAgent with the switches of the server, the namespace and
	// the user, and adds the confirmation gates of WithConfirmations to the
	// gates of PolicyGates. When the
	// first phase has no gate, it creates the job with a dispatch. When that
	// create fails with ErrUnitBusy, it creates the job again with no
	// dispatch, queued with WaitUnitBusy, and returns it with no error. It
	// returns ErrInvalid for a request that is not valid, ErrUnsupported
	// for an operation that the executor cannot do, ErrPolicyDenied with
	// the reason of the policy, ErrFrozen when a freeze is active and req
	// has no break-glass, ErrAgentDenied for a break-glass that an agent
	// asks for and for a start that the agent mode does not allow, ErrLimit
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
	//
	// Approve records verdicts for the gates of type GateApproval. A
	// confirmation goes through Confirm.
	Approve(ctx context.Context, a Actor, id ID, v Verdict, plan PlanBinding, comment string) (Job, error)
	// Confirm records the verdict of the person who asked the agent, on the
	// confirmation gate of the current phase. It needs the start action of
	// the job on its unit, not job:approve. The request must come from the
	// person, with no agent: it records the time of the last MFA step of
	// the session in Approval.MFAAt. It returns ErrTransition when the job
	// does not wait for a confirmation, ErrAgentDenied for an agent,
	// ErrNotRequester for a principal that is not Job.StartedBy, ErrMFAStale
	// when the last MFA step is older than ConfirmFreshness, ErrPlanChanged
	// and ErrPlanStale as Approve does, and ErrFinished for a job that
	// ended. A rejection ends the job rejected.
	Confirm(ctx context.Context, a Actor, id ID, v Verdict, plan PlanBinding) (Job, error)
	// RedactLog replaces each match of subs in the log version with the ID
	// version, of the job id, writes the result as the next version with
	// ArtifactStore.ReplaceLog, and then redacts version through the
	// history model, with reason as the reason of the redaction. It needs
	// the redact action of the history model on the unit. It returns the
	// new version, ErrLogOpen for a log that is not closed, ErrRedacted
	// for a version that is redacted already, and ErrInvalid for no
	// substitution or an empty match. When an earlier RedactLog of version
	// wrote the next version and its redaction of version failed, a retry
	// only redacts version.
	RedactLog(ctx context.Context, a Actor, id ID, version string, subs []Substitution, reason string) (LogRef,
		error)
}
