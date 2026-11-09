package job

import (
	"context"
	"fmt"
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
	// Reason is the reason of a terminal state, or the reason of a replan
	// that keeps the job queued.
	Reason string
	// Dispatch is set on EventDispatch: the store starts the durable
	// operation in the transaction of the change. Its key is PhaseKey.
	Dispatch *durable.StartRequest
	// PlanNotBefore is now minus the maximum plan age, on EventDispatch of
	// an apply phase after a plan phase. A plan made before it is stale.
	PlanNotBefore time.Time
	// Executor names the runner or the instance, on EventClaim.
	Executor string
	// Claim is set on EventClaim of a phase that a runner claimed as a
	// durable operation. The store checks it in the transaction of the
	// change, before it applies the event: the job must be at the phase of
	// the operation of Claim, and Claim must be the live claim of that
	// operation (its token current, its lease not expired on the clock of
	// the store). Else the change fails with ErrClaimLost and changes
	// nothing, so a holder that lost its claim to a takeover leaves neither
	// the executor, the claim count nor the audit entry of the claim in the
	// job. It is nil for an executor that holds no durable claim.
	Claim *durable.Claim
	// Delivery is set on EventDelivered: the store keeps it in the job.
	Delivery *DeliveryResult
	// MergedAt is set on EventMerged: the time of the merge, as the forge
	// reported it. The store keeps it in the delivery of the job.
	MergedAt time.Time
	// Approval is set when the change records an approval.
	Approval *Approval
	// End is set on EventRelease, EventPhaseFailed, EventPhaseDone,
	// EventLastPhaseDone and EventDelivered of a claimed phase: the store
	// ends the durable operation of the claim in the transaction of the
	// change, and so the end and the event commit together or not at all.
	// The claim binds the event: it applies only while the job is at the
	// phase of the operation of the claim and the claim is the live claim of
	// that operation.
	End *OperationEnd
}

// EndKind says how Change.End ends the durable operation of a claim.
type EndKind string

// The kinds of OperationEnd.
const (
	// EndComplete ends the operation succeeded. While a cancel is requested
	// it ends the operation canceled, as EndRelease does. So for the last
	// phase of a job during a cancel, the event (EventLastPhaseDone) ends the
	// job succeeded, because the change happened, and the durable record of
	// the operation says canceled.
	EndComplete EndKind = "complete"
	// EndFail ends the attempt with OperationEnd.Failure, as
	// durable.Store.Fail does.
	EndFail EndKind = "fail"
	// EndRelease returns the operation to pending, or ends it canceled
	// while a cancel is requested, as durable.Store.Release does. A cancel
	// can end the operation canceled while the job fails instead, when a
	// step that changes resources began: the operation is canceled and the
	// job is failed.
	EndRelease EndKind = "release"
)

// OperationEnd is the end of the durable operation of Claim, with the rule
// of Kind.
type OperationEnd struct {
	Claim durable.Claim
	Kind  EndKind
	// Failure is the failure of EndFail, and empty for the other kinds.
	Failure durable.Failure
}

// Validate returns ErrInvalid for an end that names no operation, no owner or
// no token, or a kind that is unknown.
func (e *OperationEnd) Validate() error {
	switch {
	case e.Claim.Operation == "" || e.Claim.Owner == "" || e.Claim.Token <= 0:
		return fmt.Errorf("%w: the end of an operation needs a claim with an operation, an owner and a token",
			ErrInvalid)
	case e.Kind != EndComplete && e.Kind != EndFail && e.Kind != EndRelease:
		return fmt.Errorf("%w: unknown end %q of operation %s", ErrInvalid, e.Kind, e.Claim.Operation)
	default:
		return nil
	}
}

// ValidateClaim returns ErrInvalid for a change that carries a claim on
// another event than EventClaim, or a claim that names no operation, no owner
// or no token. A change with no claim is valid.
func (c *Change) ValidateClaim() error {
	switch {
	case c.Claim == nil:
		return nil
	case c.Event != EventClaim:
		return fmt.Errorf("%w: event %s cannot carry a claim", ErrInvalid, c.Event)
	case c.Claim.Operation == "" || c.Claim.Owner == "" || c.Claim.Token <= 0:
		return fmt.Errorf("%w: the claim of an event needs an operation, an owner and a token", ErrInvalid)
	default:
		return nil
	}
}

// AllowsEnd reports whether ev can carry Change.End: the events that end or
// give back a claimed phase.
func AllowsEnd(ev Event) bool {
	switch ev { //nolint:exhaustive // Every other event ends no claim.
	case EventRelease, EventPhaseFailed, EventPhaseDone, EventLastPhaseDone, EventDelivered:
		return true
	default:
		return false
	}
}

// Filter selects jobs for List. A zero field matches every job. Limit 0
// takes the default page size of the adapter.
type Filter struct {
	Namespace namespace.Name
	// Unit selects the jobs of one unit, by its stable unit ID.
	UnitID string
	States []State
	Limit  int
	// After is the ID of the last row of the page before, as Page.After
	// gives it. The ID may name a damaged row.
	After ID
	// StateChangedBefore selects the jobs whose state last changed before this
	// time. The zero time selects every job.
	StateChangedBefore time.Time
	// StateChangedAfter selects the jobs whose state last changed after this
	// time. The zero time selects every job.
	StateChangedAfter time.Time
}

// DamagedRow is one stored job row that Store.List or Store.Orphans read and
// could not decode: the body is not JSON, or it holds a value that this
// version of the server does not know. The row stays in the store.
type DamagedRow struct {
	// ID is the job ID of the row.
	ID ID
	// UnitID is the stable ID of the unit of the row, from its unit_id
	// column. It never comes from the body: the body did not decode.
	UnitID string
	// Reason names the kind of the damage, for example "the body is not
	// JSON". It never holds a value that the body stored.
	Reason string
}

// Page is one page of Store.List: the jobs that decoded, the damaged rows
// that did not, and the cursor of the next page.
type Page struct {
	// Jobs holds the jobs of the page that decoded, newest first.
	Jobs []Job
	// Damaged holds one entry for each row of the page that did not decode,
	// newest first. A damaged row is not in Jobs.
	Damaged []DamagedRow
	// After is the ID of the last row of the page, whether it decoded or
	// not, and it is empty when the page reached the end of the list. A
	// caller gives it as Filter.After for the next page, so a damaged row
	// never hides the rows behind it.
	After ID
}

// Store is the persistence port for jobs. Each SQL store adapter implements
// it in its own SQL and runs the conformance suite in package jobtest.
//
// A job is data of its namespace: the backup of the namespace holds it, and
// every write checks fence and appends its audit entries in its
// transaction. The durable operations of its phases are control data, which
// a restore keeps. The reconcile schedule (Reconciler) applies
// AfterOperationEnded to each job that Orphans returns, expires the jobs past
// a timeout (Filter.StateChangedBefore) and dispatches the queued jobs. A phase
// that changes resources never dispatches again on its own.
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
	// the checks of a dispatch in Apply: ErrLocked, ErrUnitGone, ErrUnitBusy
	// and ErrFrozen. Such an error stores nothing. Service.Start then calls
	// Create again with no dispatch and j.Wait set to WaitLocked for
	// ErrLocked or WaitUnitBusy for ErrUnitBusy, and the reconcile schedule
	// dispatches the job when the unit is free. A create with no dispatch
	// stores the job whatever manual lock covers the unit: the lock stops
	// the dispatch, not the queue. A second create with the
	// key of j returns the first job and created false, when the spec is
	// equal. It returns ErrKeyConflict when the key names a job with
	// another spec, and ErrLimit when the unit has MaxOpenPerUnit jobs that
	// are not terminal, or the namespace MaxOpenPerNamespace.
	Create(ctx context.Context, j Job, dispatch *durable.StartRequest, fence namespace.Fence,
		e []audit.Entry) (stored Job, created bool, err error)
	// Get returns the job id, or ErrNotFound. It returns an error that wraps
	// ErrDamaged for a row whose body does not decode.
	Get(ctx context.Context, id ID) (Job, error)
	// List returns the jobs that f selects, newest first, in a Page. A row
	// that does not decode is in Page.Damaged, with its job ID, its unit ID
	// and the kind of the damage, and is not in Page.Jobs: one damaged row
	// never hides the jobs of a page, and Page.After continues behind it. A
	// batch reader skips a damaged row: the server never dispatches, expires
	// or closes it.
	List(ctx context.Context, f Filter) (Page, error)
	// CancelDamaged sets the state of the job id to canceled, with e, and
	// reads no body: a row that a reader cannot decode can still be
	// canceled by its ID, and a damaged row is not dispatched, expired or
	// closed by any reader. It returns ErrNotFound for an unknown job and
	// for a job of another namespace, ErrInvalid for a row that is not
	// damaged, ErrFinished for a job that already ended, and the error of a
	// stale fence. The row keeps the rest of its columns and its body. A
	// caller uses it only for a row that List or Orphans reported as
	// damaged: a job whose body decodes is canceled through Apply with
	// EventCancel, which also ends its operation.
	CancelDamaged(ctx context.Context, id ID, fence namespace.Fence, e []audit.Entry) error
	// Apply makes c with Next, and appends e, in one transaction. It returns
	// ErrNotFound for an unknown job, ErrConflict when the job is not at
	// c.Version, and the error of Next for an event that the state does not
	// allow. It checks these rules in the transaction of the change:
	//   - EventDispatch of any phase, also a plan phase: ErrLocked while a
	//     manual lock covers the unit (its own lock, the lock of its
	//     collection now, or the lock of its namespace). Break-glass does not
	//     pass it. ErrUnitGone when the inventory holds no unit with the unit
	//     ID of the job: no lock can be read, so the dispatch fails closed,
	//     and the job stays queued. The adapter reads the lock in the
	//     transaction of the change, with the same coverage as
	//     manuallock.Store.CoveringLock. A SQL adapter under READ COMMITTED
	//     reads the lock in a statement that runs after the transaction holds
	//     the namespace row FOR UPDATE. The
	//     append of the audit entry takes that row in every fenced audited
	//     change, and so do PutLock and this change. A manual lock that
	//     committed before is then seen, and a PutLock that starts later waits
	//     for the dispatch. A read before the row is held can miss a lock that
	//     commits before the dispatch does. A higher isolation level does not
	//     replace this order: the lock write does not read the job tables, so
	//     the database finds no conflict. An adapter needs a test that
	//     dispatches and locks in parallel. The job
	//     stays queued and keeps its queue timeout: EventExpire still
	//     ends it. No other event is refused for a lock, so a phase that
	//     runs when the lock arrives claims, writes and ends as before.
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
	//   - c.End: it ends the durable operation of c.End.Claim in the same
	//     transaction, with the rules of durable.Store.Complete, Fail or
	//     Release, and checks the token of the claim and its expiry on the
	//     clock of the store. The job must be at the phase whose operation
	//     the claim names. An error of the end (durable.ErrClaimLost, with
	//     ErrClaimLost) or of the event changes nothing: the operation and the
	//     job both stay as they were. So a holder that lost its claim to a
	//     takeover changes neither the operation nor the job, and nobody can
	//     see the end without the event. c.End on another event is
	//     ErrInvalid.
	//   - EventClaim with c.Claim: it checks the claim in the same transaction,
	//     before it applies the event (see Change.Claim). A claim that is not
	//     live is ErrClaimLost, with durable.ErrClaimLost.
	//   - EventCancel of a job with a live durable operation: it applies
	//     durable.Store.Cancel to the operation.
	//   - EventMerged and EventDelivered: when the last final state version
	//     of the unit closes the job (Job.ClosedBy), it also applies
	//     EventStateObserved with that version, sets the Verification to
	//     pending and starts the operation of KindVerify. So a merge that
	//     the review watcher sees after the state version, or a commit that
	//     a takeover finds by its trailer after the state version, still
	//     closes the job.
	//   - EventDelivered of a review request that stays in awaiting-state:
	//     it starts the operation of KindReviewWatch.
	//   - EventCancel, EventRejected or EventExpire of a job that delivered:
	//     it starts the operation of KindAbandon.
	Apply(ctx context.Context, c Change, fence namespace.Fence, e []audit.Entry) (Job, error)
	// SetVerification records the result of the verify operation of the
	// closed job id, with e. It returns ErrNotFound for an unknown job,
	// ErrInvalid for a job with no closing state version or for v pending,
	// and changes nothing when the job has v already.
	SetVerification(ctx context.Context, id ID, v Verification, fence namespace.Fence, e []audit.Entry) error
	// Orphans returns at most limit jobs that are dispatched, running or
	// canceling, but whose durable operation does not exist or ended with
	// no Finish. A row that does not decode is in the damaged result, with
	// its job ID, its unit ID and the kind of the damage, and is not in the
	// jobs. A restore makes the first kind. A durable failure at the
	// limit of expired claims makes the second. A claim that expires during
	// a cancel does not: the claimer takes such an operation and ends the
	// phase (durable.ClaimRequest.TakeCanceled). The claimer never leaves a
	// job with an operation that ended and no event, because it ends the
	// operation and applies the event in one transaction (Change.End). The reconcile schedule
	// (Reconciler) applies the event of AfterOperationEnded to each job that
	// this method returns: a change that began fails the job,
	// and a canceling job with no change ends canceled.
	Orphans(ctx context.Context, limit int) ([]Orphan, []DamagedRow, error)
}

// FinalVersion is the last final state version of one unit, as the state
// write of the unit recorded it.
type FinalVersion struct {
	UnitID    string
	VersionID string
	Commit    string
	Job       ID
	At        time.Time
}

// FinalVersionReader reads the last final state version of a unit. It is a
// separate port, so that other implementations of Store stay valid, as for
// FreezeStore.
type FinalVersionReader interface {
	// FinalVersion returns the last final state version of the unit unitID
	// in ns. It returns false and no error when the store recorded no final
	// version for the unit in ns. It returns an error that wraps ErrInvalid
	// for an empty ns or an empty unitID. It changes nothing.
	FinalVersion(ctx context.Context, ns namespace.Name, unitID string) (FinalVersion, bool, error)
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
	// Purpose is empty, or PurposeDriftCheck for a drift check. A drift check
	// has the operation refresh and the one phase plan, and needs the action
	// of a plan on the unit.
	Purpose Purpose
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
	// Source tells where the start came from. Empty takes SourceAgent for an
	// agent and SourceAPI for any other actor. Only a feature of the server
	// sets it: the REST API always starts a job with SourceAPI. A source
	// kind that is not in SourceKinds fails with ErrInvalid.
	Source SourceKind
	// IdempotencyKey makes a retry of the start safe: two starts with one key
	// and one spec by one principal give one job. Empty makes a new job each
	// time.
	IdempotencyKey string
	// Reason is the text that the caller gives for the change. The audit
	// entry of the start holds it.
	Reason string
	// BreakGlass starts the job through an active freeze. It needs the
	// break-glass action on the namespace, and a person: an agent cannot
	// ask for it.
	BreakGlass *BreakGlass
	// Trigger names the run trigger that starts the job. Only a feature of the
	// server sets it: the REST API of jobs never does. The job keeps it.
	// Source tells how the trigger started the job: a manual fire keeps the
	// source of the caller, and any other start by a trigger is the start of
	// the namespace service principal.
	Trigger *TriggerRef
	// Channel is where the request came from, for the audit entry.
	Channel audit.Channel
	// SourceIP, UserAgent and RequestID describe the request for the audit
	// entry. They are empty for a start that no request made. The service
	// holds them as given: the caller bounds them.
	SourceIP  string
	UserAgent string
	RequestID string
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
	// dispatch, queued with WaitUnitBusy, and returns it with no error. For
	// ErrLocked it does the same with WaitLocked, also for a job with
	// break-glass, and for a plan phase. It
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
	//
	// This version does not implement the contract below: each call returns
	// ErrUnsupported and changes nothing.
	Approve(ctx context.Context, a Actor, id ID, v Verdict, plan PlanBinding, comment string) (Job, error)
	// Confirm records the verdict of the person who asked the agent, on the
	// confirmation gate of the current phase. It needs the start action of
	// the job on its unit, not job:approve. The request must come from the
	// person, with no agent, and with a fresh authentication of the kind
	// a.ConfirmAuth: a new MFA step, or a new authentication at the SSO
	// provider with ConfirmFreshness as its maximum age. It records
	// a.ConfirmAuth in Approval.Auth and a.AuthAt in Approval.AuthAt. It
	// returns ErrTransition when the job does not wait for a confirmation,
	// ErrAgentDenied for an agent, ErrNotRequester for a principal that is
	// not Job.StartedBy, ErrAuthStale when the fresh authentication is
	// missing or older than ConfirmFreshness, ErrPlanChanged and
	// ErrPlanStale as Approve does, and ErrFinished for a job that ended. A
	// rejection ends the job rejected.
	//
	// This version does not implement the contract below: each call returns
	// ErrUnsupported and changes nothing.
	Confirm(ctx context.Context, a Actor, id ID, v Verdict, plan PlanBinding) (Job, error)
	// RedactLog replaces each match of subs in the log version with the ID
	// version, of the job id, writes the result as the next version with
	// ArtifactStore.ReplaceLog, and then redacts version through
	// history.Redactor, with history.CauseRequest and reason as the reason
	// of the redaction. It needs the action unit:redact on the unit. It
	// returns the new version, history.ErrInProgress for a log that is not
	// closed, history.ErrRedacted for a version that is redacted already,
	// the error of history.ValidateReason for a bad reason, and ErrInvalid
	// for no substitution or an empty match. When an earlier RedactLog of
	// version wrote the next version and its redaction of version failed, a
	// retry only redacts version. The audit entry never holds the text of
	// subs.
	RedactLog(ctx context.Context, a Actor, id ID, version string, subs []Substitution, reason string) (LogRef,
		error)
}
