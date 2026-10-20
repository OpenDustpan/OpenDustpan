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
// The store keeps no schedule clock. How a schedule trigger gets its
// ticks is open (RFC 24). The slice that adds it adds the port for it.
type Store interface {
	// Put creates the trigger t, or replaces the trigger of the same ID. A
	// replace cannot change the unit or the kind. In one transaction it checks
	// the fence, reads the follows triggers of the namespace, applies
	// CheckCycle, writes the trigger, and appends the audit entry e. The
	// caller stamps the time and the actor on e.
	//
	// It returns:
	//   - ErrInvalid when t.Validate fails, and when a replace changes the unit
	//     or the kind.
	//   - ErrCycle (a *CycleError) when t closes a loop of follows triggers. A
	//     trigger that is off closes none. So switching a trigger on is
	//     checked as a new write.
	//   - namespace.ErrFenced when the fence is stale or belongs to another
	//     namespace than t.Unit.
	//
	// On every error the store changes nothing. Put does not check access. The
	// caller checks the actions on the unit, and on the source unit of a
	// follows trigger.
	Put(ctx context.Context, t Trigger, fence namespace.Fence, e audit.Entry) error
	// Get returns the trigger id of the namespace ns, or ErrNotFound. It
	// returns ErrInvalid for an ID or a namespace that fails validation. Get
	// does not check access.
	Get(ctx context.Context, ns namespace.Name, id ID) (Trigger, error)
	// List returns the triggers of unit, ordered by ID. A unit with no trigger
	// gives an empty list and no error. It returns ErrInvalid for a unit that
	// fails validation.
	List(ctx context.Context, unit graph.Unit) ([]Trigger, error)
	// Followers returns the enabled follows triggers whose source is source,
	// ordered by ID. A source that nothing follows gives an empty list and no
	// error. It returns ErrInvalid for a unit that fails validation.
	Followers(ctx context.Context, source graph.Unit) ([]Trigger, error)
	// Delete removes the trigger id of the namespace ns and appends the audit
	// entry e in the transaction. It returns ErrNotFound for an unknown
	// trigger, ErrInvalid, and namespace.ErrFenced. The purge of a unit calls
	// it for each trigger of the unit, and for each trigger that follows the
	// unit.
	Delete(ctx context.Context, ns namespace.Name, id ID, fence namespace.Fence, e audit.Entry) error
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
// call it. It starts plans through the Starter and nothing else, and it
// checks nothing about the people who set the triggers: the Store caller did
// that at save. A start that the job service refuses is an error of that start.
//
// The dispatcher starts a plan with the principal of the trigger for the
// events that no person starts. Which principal that is, and what it may
// do, is open (RFC 24). A manual fire uses the actor of the caller.
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
	// no start otherwise. The idempotency key of each start is
	// UpstreamKey(id, applied.Version), so a second call for one apply starts
	// nothing new. It
	// returns one Outcome for each trigger, ordered by trigger ID. A unit that
	// nothing follows gives no outcome and no error. It returns ErrInvalid for
	// a value that fails validation.
	Upstream(ctx context.Context, applied Applied) ([]Outcome, error)
	// Manual starts the plan of a trigger now, for the principal of actor. A
	// person uses it to test a trigger. The idempotency key is
	// ManualKey(req.Trigger, req.Now), so a double click gives one job. It
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
}

// Applied is the event that the source of a follows trigger has applied. The
// tool package that wrote the final state version of the run sends it, whether
// a job, a workstation or a pipeline ran the tool. The server knows an apply by
// that version, not by a job.
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

	return nil
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

// ManualKey returns the idempotency key of a manual fire of the trigger id at
// now, to the minute. A double click gets one key.
func ManualKey(id ID, now time.Time) string {
	return fmt.Sprintf("trigger:%s:manual:%d", id, now.UTC().Truncate(time.Minute).Unix())
}
