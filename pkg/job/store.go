package job

import (
	"context"

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
	// operation in the transaction of the change.
	Dispatch *durable.StartRequest
	// Executor names the runner or the instance, on EventClaim.
	Executor string
	// Delivery is set on EventDelivered: the store keeps it in the job.
	Delivery *DeliveryResult
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
// that runs or waits for an executor but has no live operation. A phase
// that changes resources never dispatches again on its own.
//
// Every method that writes returns namespace.ErrFenced for a stale fence,
// and changes nothing on an error.
type Store interface {
	// Create persists j as queued, with e, and returns it. When dispatch is
	// not nil, the job starts dispatched and the store starts the durable
	// operation in the same transaction. A second create with the key of j
	// returns the first job and created false, when the spec is equal. It
	// returns ErrKeyConflict when the key names a job with another spec.
	Create(ctx context.Context, j Job, dispatch *durable.StartRequest, fence namespace.Fence,
		e []audit.Entry) (stored Job, created bool, err error)
	// Get returns the job id, or ErrNotFound.
	Get(ctx context.Context, id ID) (Job, error)
	// List returns the jobs that f selects, newest first.
	List(ctx context.Context, f Filter) ([]Job, error)
	// Apply makes c with Next, and appends e, in one transaction. It returns
	// ErrNotFound for an unknown job, ErrConflict when the job is not at
	// c.Version, and the error of Next for an event that the state does not
	// allow. On EventDispatch of a phase that changes resources, it returns
	// ErrUnitBusy when another job holds the unit, and ErrFrozen when a
	// freeze of the namespace is active and the job has no break-glass. It
	// checks both in the transaction of the dispatch. On EventCancel of a
	// job with a durable operation, it applies durable.Store.Cancel to the
	// operation in the same transaction.
	Apply(ctx context.Context, c Change, fence namespace.Fence, e []audit.Entry) (Job, error)
	// ObserveStateVersion closes each write-back job of v.Unit that waits for
	// a state version and delivered before v.At: it applies
	// EventStateObserved with v as the closing version. A review request
	// with a known merge must merge before v.At. It returns the closed jobs,
	// none when no job waits. A tool package never calls it: the store
	// adapter applies its rule in the transaction of each state write of
	// any tool, so a state write and the close commit together.
	ObserveStateVersion(ctx context.Context, v StateVersion, fence namespace.Fence) ([]Job, error)
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
	// Steps replace the steps of the unit settings and of its tool. A
	// blueprint gives them. Empty takes those defaults.
	Steps []Step
	Pool  string
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
	// break-glass action on the namespace.
	BreakGlass *BreakGlass
	// Channel is where the request came from, for the audit entry.
	Channel audit.Channel
}

// Service is what a caller uses to start and drive a job. The REST API
// adapter and every feature of the server that starts a job call it. It
// checks the actions of the caller, the freezes and the policy, writes the
// audit entries, and calls the Executor and the Store. Every method checks
// the actions of the operation (see RequiredActions) on the unit, and
// returns an error that wraps identity.ErrDenied when one is missing.
type Service interface {
	// Start checks req, decides, and creates the job. It returns ErrInvalid
	// for a request that is not valid, ErrUnsupported for an operation that
	// the executor cannot do, ErrPolicyDenied with the reason of the policy,
	// ErrFrozen when a freeze is active and req has no break-glass, and
	// ErrKeyConflict for a key that names another spec. The job may stay
	// queued with a wait reason, or wait for approval.
	Start(ctx context.Context, p identity.Principal, req StartRequest) (Job, error)
	// Get returns the job id, or ErrNotFound. It returns ErrNotFound also
	// when p may not read the job, so that a caller cannot learn that the
	// job exists.
	Get(ctx context.Context, p identity.Principal, id ID) (Job, error)
	// Cancel asks to stop the job id. A job with no executor ends canceled
	// at once. A running job goes to canceling, and its executor stops at
	// its next write. A second cancel changes nothing. It returns
	// ErrFinished for a job that already ended.
	Cancel(ctx context.Context, p identity.Principal, id ID, reason string) (Job, error)
	// Approve records the verdict of p on the gate of the current phase. It
	// returns ErrTransition when the job does not wait for approval,
	// ErrSelfApproval for an approval by the starter that the gate does not
	// allow, ErrPlanChanged when planSHA256 is not the hash of the plan
	// that the gate binds to, and ErrFinished for a job that ended.
	Approve(ctx context.Context, p identity.Principal, id ID, v Verdict, planSHA256, comment string) (Job, error)
}
