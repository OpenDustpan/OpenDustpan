package trigger

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Store keeps the triggers. A SQL adapter serves it from the database of the
// namespace. Every write carries the fence of the namespace, and the store
// checks it in the transaction of the write, as every other store does. A
// write appends its audit entry in that transaction (fail closed).
//
// The store keeps the next slot of each enabled schedule trigger, and no clock.
// A schedule trigger gets its ticks from a claim of due slots (ClaimDue), with
// an atomic claim for each slot, so that two instances fire one slot once.
type Store interface {
	// Put creates the trigger t, or replaces the trigger of the same ID. A
	// replace cannot change the unit or the kind. In one transaction it checks
	// the fence, reads the follows triggers of the namespace, applies
	// CheckCycle, writes the trigger, and appends the audit entry e. The
	// caller stamps the time and the actor on e.
	//
	// It returns:
	//   - ErrInvalid when t.Validate fails (except for a self-follow, below),
	//     and when a replace changes the unit or the kind.
	//   - ErrCycle (a *CycleError) when t closes a loop of follows triggers. A
	//     trigger that is off closes none. So switching a trigger on is
	//     checked as a new write.
	//   - namespace.ErrFenced when the fence is stale or belongs to another
	//     namespace than t.Unit.
	//
	// A follows trigger whose source is its own unit returns ErrCycle, not
	// ErrInvalid.
	//
	// An adapter serializes the trigger writes of one namespace, for example
	// with a lock row of the namespace or with SERIALIZABLE transactions. So
	// two saves at the same time cannot each pass the cycle check over the
	// same rows and then both commit. An adapter that uses SERIALIZABLE
	// transactions retries a serialization failure inside Put, so the caller
	// sees only nil or ErrCycle, and never a serialization error.
	//
	// A stored follows trigger that fails Validate takes no part in the check
	// for a loop, because no plan starts for it. It does not make Put fail.
	//
	// On every error the store changes nothing. Put does not check access. The
	// caller checks the actions on the unit, and on the source unit of a
	// follows trigger.
	Put(ctx context.Context, t Trigger, fence namespace.Fence, e audit.Entry) error
	// Get returns the trigger id of the namespace ns, or ErrNotFound. It
	// returns ErrInvalid for an ID or a namespace that fails validation. Get
	// does not check access.
	Get(ctx context.Context, ns namespace.Name, id ID) (Trigger, error)
	// Stored returns the trigger id of the namespace ns as the row holds it,
	// with no check of the rules of a trigger. Only a delete uses it: Get
	// refuses a row that fails its checks, and such a row must still be found
	// to be removed. The caller treats every field as untrusted. It returns
	// ErrNotFound for an unknown trigger and ErrInvalid for an ID or a
	// namespace that fails validation. Stored does not check access.
	Stored(ctx context.Context, ns namespace.Name, id ID) (Trigger, error)
	// List returns the triggers of unit, ordered by ID. A unit with no trigger
	// gives an empty list and no error. It returns ErrInvalid for a unit that
	// fails validation.
	List(ctx context.Context, unit graph.Unit) ([]Trigger, error)
	// Followers returns the enabled follows triggers whose source is source,
	// ordered by ID. A source that nothing follows gives an empty list and no
	// error. It returns ErrInvalid for a unit that fails validation.
	Followers(ctx context.Context, source graph.Unit) ([]Trigger, error)
	// Delete removes the trigger id of the namespace ns and appends the audit
	// entry e in the transaction. It removes a row that fails its checks too.
	// It returns ErrNotFound for an unknown trigger, ErrInvalid, and
	// namespace.ErrFenced. The purge of a unit calls
	// it for each trigger of the unit, and for each trigger that follows the
	// unit.
	Delete(ctx context.Context, ns namespace.Name, id ID, fence namespace.Fence, e audit.Entry) error
	// NextSlot returns the next slot of the schedule trigger id of the
	// namespace ns: the first slot that a claim can still take. Put sets it to
	// the first slot after the clock of the store (NextSlot of the
	// package) when the trigger is an enabled schedule trigger and is new, was
	// off, or has another frequency. A replace that keeps an enabled schedule
	// trigger and its frequency keeps the slot. ClaimDue moves it. It returns
	// the zero time for a trigger that has no slot: a trigger of another kind
	// and a trigger that is off. An enabled schedule trigger always has a slot,
	// and the store refuses a row that has none. It returns ErrNotFound for an unknown trigger and
	// ErrInvalid for an ID or a namespace that fails validation. NextSlot does
	// not check access.
	NextSlot(ctx context.Context, ns namespace.Name, id ID) (time.Time, error)
	// ClaimDue takes the slot c.Slot of a schedule trigger for the instance that
	// calls. The claim and the move of the next slot to the slot after c.Slot
	// are one step in one transaction, so of many instances that claim one slot
	// exactly one succeeds. In that transaction it checks the fence, reads the
	// trigger, applies CheckClaim, moves the next slot, and appends the audit
	// entry e. The caller stamps the time and the actor on e.
	//
	// The store decides from its own clock whether a slot is due: it refuses a
	// slot later than that clock plus MaxClaimSkew with ErrInvalid. PostgreSQL
	// reads the clock of the database in the transaction of the claim, so an
	// instance with a wrong clock cannot move the next slot into the future. Put
	// takes the first slot after the same clock, and not after the time of its
	// audit entry.
	//
	// A claim can name a slot after the next slot: the slots between them are
	// skipped. So after an outage one claim for the latest missed slot starts
	// one run, and never one run for each missed slot.
	//
	// It returns:
	//   - nil for the one claim that took the slot,
	//   - ErrNotDue when c.Slot is before the next slot,
	//   - ErrDisabled for a trigger that is off,
	//   - ErrNotFound for an unknown trigger,
	//   - ErrInvalid when c fails Validate, when the trigger is not a valid
	//     schedule trigger, and when c.Slot is not a time that its schedule gives,
	//   - namespace.ErrFenced when the fence is stale or belongs to another
	//     namespace than c.Namespace,
	//   - audit.ErrInvalidEntry for an entry that fails Validate or names
	//     another namespace.
	//
	// On every error the store changes nothing and appends nothing. A claim
	// starts no job: the caller then starts the plan with Dispatcher.Scheduled,
	// whose idempotency key is ScheduleKey. ClaimDue does not check access.
	ClaimDue(ctx context.Context, c Claim, fence namespace.Fence, e audit.Entry) error
}

// PlanRequest is what the dispatcher gives to a Starter.
type PlanRequest struct {
	// Trigger is the ID of the trigger that fires.
	Trigger ID
	// Kind is the kind of that trigger.
	Kind Kind
	// IdempotencyKey makes a retry safe: two calls with one key give one job.
	// Use ScheduleKey, UpstreamKey or ManualKey.
	IdempotencyKey string
	// Reason is the text for the audit entry of the start. It names the
	// trigger and the cause.
	Reason string
	// Source is the source kind of the job. It is empty for a manual fire,
	// and the job then keeps the source kind of the caller (api). Else it is
	// schedule, forge or trigger by the kind of the trigger, and the start is
	// the start of the namespace service principal.
	Source job.SourceKind
	// Saver is the subject of the last saver of the trigger. The job records
	// it with the trigger ID, and the audit entry of the start names it.
	Saver string
	// Location is the code location of the plan, or the zero value to take the
	// location of the unit settings. Only a manual fire sets it: the caller
	// names the location, and the job service checks the extra action for it
	// as for any start by that caller. A start that no person makes never
	// carries a location, because the namespace service principal holds no
	// action for one.
	Location job.Location
	// Request describes the request of a manual fire, for the audit entry of the
	// job start. It is the zero value for a start that no person makes.
	Request RequestInfo
}

// Starter starts one plan job of a unit through the job service. It is the
// only way that a trigger starts a job. It has no method for an apply, so no
// trigger can start one. The adapter over job.Service resolves the unit to
// its reference, takes the executor, the code location and the runner pool
// from the settings of the unit, and sets the operation plan. Because the
// start passes through the job service, the gates, the approval rules, the
// freezes and the manual lock apply as to any job.
//
// A job.StartRequest does not yet have the fields that this needs: the source
// kind of the start, and the ID of the trigger that the job records. The
// slice that adds the job service routes adds the source kind.
type Starter interface {
	// StartPlan starts a plan of unit for the principal of actor and returns
	// the job ID. A manual lock on the unit does not make it fail: the job
	// service queues the job with the wait reason locked. It returns the errors
	// of job.Service.Start, for example job.ErrLimit, job.ErrFrozen,
	// job.ErrUnsupported and job.ErrPolicyDenied.
	StartPlan(ctx context.Context, actor job.Actor, unit graph.Unit, req PlanRequest) (job.ID, error)
}

// Outcome is the result of one trigger in a Dispatcher.Upstream call.
type Outcome struct {
	// Trigger is the trigger that fired.
	Trigger ID
	// Job is the job that started, or empty when Err is set.
	Job job.ID
	// Err is the error of the start of this trigger. An error here never
	// stops the other triggers of the call.
	Err error
}

// Dispatcher fires triggers. The schedule, the end of a job and the REST route
// call it. It starts plans through the Starter and nothing else. A start that
// the job service refuses is an error of that start.
//
// Defense in depth: each method runs Trigger.Validate on the trigger that it
// reads, and fails closed with ErrInvalid. A stored row that another version
// wrote, or that a restore brought back, and that no longer validates, starts
// no plan. The Store checks the same value at save and does not replace this
// check.
//
// For an event that no person starts, the dispatcher starts the plan as the
// namespace service principal. That principal holds only the action that
// starts a plan. For a job that a trigger starts, reads of the outputs of the
// source use the current grants of the last saver of the trigger, and the
// server denies when that saver is missing, deleted or disabled. A manual
// fire uses the actor of the caller.
//
// Before each start, the dispatcher checks that the last saver of the trigger
// still holds the settings action and the start action on the unit (SaverCheck).
// A saver that lost access, or whose principal is missing, deleted or
// disabled, gets no start: the dispatcher writes an audit entry of the refusal
// and returns an error that wraps ErrSaverDenied. The check applies to a manual
// fire as well, so a trigger that nobody maintains starts nothing.
//
// The kind review-request-push has no method yet. It joins when the Forge
// port exists.
type Dispatcher interface {
	// Scheduled starts the plan that the schedule trigger id of the
	// namespace ns owes for slot. The idempotency key is ScheduleKey(id,
	// slot), so two instances that fire the same slot start one job. It
	// returns ErrNotFound for an unknown trigger, ErrInvalid for a trigger
	// that is not a schedule trigger and for a zero slot, ErrDisabled for a
	// trigger that is off, and the errors of Starter.StartPlan.
	Scheduled(ctx context.Context, ns namespace.Name, id ID, slot time.Time) (job.ID, error)
	// Upstream starts a plan for each enabled follows trigger whose source is
	// applied.Unit. It checks with the job store that applied.Version is a
	// final state version of that unit, and returns ErrVersionMismatch with
	// no start otherwise. An event for a version older than the last final
	// version that the job store recorded for the unit also returns
	// ErrVersionMismatch, because the newer version fires. The idempotency key
	// of each start is UpstreamKey(id, applied.Version), so a second call for
	// one apply starts nothing new. It
	// returns one Outcome for each trigger, ordered by trigger ID. A unit that
	// nothing follows gives no outcome and no error. It returns ErrInvalid for
	// a value that fails validation.
	Upstream(ctx context.Context, applied Applied) ([]Outcome, error)
	// Manual starts the plan of a trigger now, for the principal of actor. A
	// person uses it to test a trigger. The idempotency key is
	// req.IdempotencyKey when the caller sent one. Else it is
	// ManualKey(req.Trigger, actor, req.Now), so a double click inside one
	// minute gives one job and two people who fire one trigger in one minute
	// give two. A double click across a minute boundary gives two jobs, unless
	// the caller sent a key. It
	// returns ErrNotFound, ErrDisabled, ErrInvalid, and the errors of
	// Starter.StartPlan.
	Manual(ctx context.Context, actor job.Actor, req ManualRequest) (job.ID, error)
}

// ManualRequest is what a person gives to Dispatcher.Manual.
type ManualRequest struct {
	// Namespace is the namespace of the trigger.
	Namespace namespace.Name
	// Trigger is the ID of the trigger.
	Trigger ID
	// Now is the time of the request. The server sets it.
	Now time.Time
	// Reason is the text of the person for the audit entry of the start.
	Reason string
	// Location is the code location of the plan, or the zero value. The job
	// service checks the extra action of a location for the caller.
	Location job.Location
	// IdempotencyKey is the key that the caller sent, or empty. A key that the
	// job service refuses fails the start.
	IdempotencyKey string
	// Request describes the request, for the audit entry of the job start.
	Request RequestInfo
}

// Applied is the event that the source of a follows trigger has applied. The
// tool package that wrote the final state version of the run sends it, whether
// a job, a workstation or a pipeline ran the tool. The server knows an apply by
// that version, not by a job. The sender filters the events: the Pulumi
// adapter sends only for an update of the kind update.
type Applied struct {
	// Unit is the unit that applied.
	Unit graph.Unit
	// Version is the ID of the final state version of the run (job.StateVersion
	// with Final set).
	Version string
}

// Validate returns an error that wraps ErrInvalid when a is not valid.
func (a Applied) Validate() error {
	if err := a.Unit.Validate(); err != nil {
		return fmt.Errorf("%w: unit: %w", ErrInvalid, err)
	}

	if a.Version == "" || len(a.Version) > MaxVersionBytes {
		return fmt.Errorf("%w: an applied event names its state version", ErrInvalid)
	}

	// Defense in depth: the version goes into an idempotency key, so the
	// check allows only the bytes that keep the key in one piece. The tool
	// package that sends the event checks the version too.
	for i := range len(a.Version) {
		if !versionByte(a.Version[i]) {
			return fmt.Errorf("%w: a state version has a byte that is not in a-z, A-Z, 0-9, \"-\", \"_\" or \".\"",
				ErrInvalid)
		}
	}

	return nil
}

// versionByte reports whether c is a byte that a state version ID may have.
func versionByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.'
}

// ScheduleKey returns the idempotency key of the plan that the schedule
// trigger id owes for slot. Two instances that fire one slot get one key.
func ScheduleKey(id ID, slot time.Time) string {
	return fmt.Sprintf("trigger:%s:schedule:%d", id, slot.UnixMilli())
}

// UpstreamKey returns the idempotency key of the plan that the follows
// trigger id starts after the state version applied was written. A second
// event for one version gets the same key.
func UpstreamKey(id ID, version string) string {
	return fmt.Sprintf("trigger:%s:upstream:%s", id, version)
}

// ManualKey returns the idempotency key of a manual fire of the trigger id by
// the person with the subject actor, at now, to the minute. A double click of
// one person gets one key. Another person gets another key, so the click of
// one person never hides the fire of another.
func ManualKey(id ID, actor string, now time.Time) string {
	return fmt.Sprintf("trigger:%s:manual:%q:%d", id, actor, now.UTC().Truncate(time.Minute).Unix())
}
