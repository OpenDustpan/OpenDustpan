package job

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// NamespaceReader reads a namespace for its current fence. namespace.Store
// and every SQL store adapter implement it.
type NamespaceReader interface {
	GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// claimAttempts is how many times the claimer reads a job again after a
// concurrent change of it, before it gives the operation back.
const claimAttempts = 5

// reasonNoPhase is the reason of an operation that no job phase holds. The
// claimer ends such an operation failed, so it never blocks a kind.
const reasonNoPhase = "the operation does not belong to the current phase of a job"

// DurableClaimer is the Claimer over a durable.Store and a Store. A phase is
// a durable operation: the claim, its expiry and its cancel are the rules of
// package durable, and the claimer applies the matching event to the job
// after each of them.
//
// The durable store checks the token of the claim and the time of its expiry
// on its own clock, in its own transaction, and the job store checks the
// version of the job and the fence in the transaction of each event. So of
// several callers of one claim, one wins, and a holder that lost its claim
// changes nothing. A step that stops between the two writes leaves a state
// that the next claim or the reconcile schedule ends: a phase whose
// operation ended with no event is an Orphan, and a phase whose claim
// expired is a takeover.
type DurableClaimer struct {
	jobs       Store
	operations durable.Store
	namespaces NamespaceReader
}

var _ Claimer = (*DurableClaimer)(nil)

// NewClaimer returns the claimer of the phases of jobs. namespaces gives the
// current fence of the namespace of a unit.
func NewClaimer(jobs Store, operations durable.Store, namespaces NamespaceReader) *DurableClaimer {
	return &DurableClaimer{jobs: jobs, operations: operations, namespaces: namespaces}
}

// Claim implements Claimer.
func (c *DurableClaimer) Claim(ctx context.Context, req ClaimRequest) (Assignment, error) {
	dreq := durable.ClaimRequest{Owner: req.Owner, Kinds: req.Kinds, MaxVersions: req.MaxVersions, Lease: req.Lease}
	if err := dreq.Validate(); err != nil {
		return Assignment{}, claimError(fmt.Errorf("claim request: %w", err))
	}

	for {
		op, err := c.operations.Claim(ctx, dreq)
		if err != nil {
			return Assignment{}, claimError(err)
		}

		a, ok, err := c.assign(ctx, &op)
		if err != nil {
			return Assignment{}, err
		}

		if ok {
			return a, nil
		}
	}
}

// Renew implements Claimer.
func (c *DurableClaimer) Renew(ctx context.Context, pc PhaseClaim, d time.Duration) (PhaseClaim, error) {
	if d <= 0 {
		return PhaseClaim{}, fmt.Errorf("%w: a lease must be positive", ErrInvalid)
	}

	if err := c.checkFence(ctx, &pc); err != nil {
		return PhaseClaim{}, err
	}

	claim, err := c.operations.Extend(ctx, pc.Claim, d)
	if err != nil {
		return PhaseClaim{}, claimError(err)
	}

	pc.Claim = claim

	return pc, nil
}

// Release implements Claimer.
func (c *DurableClaimer) Release(ctx context.Context, pc PhaseClaim) error {
	j, err := c.running(ctx, &pc)
	if err != nil {
		return err
	}

	ev, why := AfterLostClaim(j.Spec.Steps, j.Phases[pc.Phase].Steps, EventRelease)

	if ev == EventPhaseFailed {
		err = c.operations.Fail(ctx, pc.Claim, durable.Failure{Message: why, Final: true, RetryAfter: 0})
	} else {
		err = c.operations.Release(ctx, pc.Claim)
	}

	if err != nil {
		return claimError(err)
	}

	return c.applyEnd(ctx, pc.Job, &ending{fence: pc.Fence, event: ev, reason: why, delivery: nil})
}

// Finish implements Claimer.
func (c *DurableClaimer) Finish(ctx context.Context, pc PhaseClaim, o Outcome) error {
	j, err := c.running(ctx, &pc)
	if err != nil {
		return err
	}

	ev := FinishEvent(j.Phases[pc.Phase].Kind, pc.Phase == len(j.Phases)-1, o)

	var delivery *DeliveryResult

	if ev == EventDelivered {
		if j.Delivery == nil {
			return fmt.Errorf("%w: job %s ended its deliver phase before it recorded the delivery", ErrInvalid, j.ID)
		}

		d := *j.Delivery
		delivery = &d
	}

	reason := ""

	if o.Succeeded {
		err = c.operations.Complete(ctx, pc.Claim)
		if errors.Is(err, durable.ErrCanceled) {
			// A cancel arrived while the phase ran. The phase did end, so the
			// event of the job says so, and the release ends the operation
			// canceled.
			err = c.operations.Release(ctx, pc.Claim)
		}
	} else {
		reason = o.Message
		if reason == "" {
			reason = "the executor reported that the phase failed"
		}

		err = c.operations.Fail(ctx, pc.Claim, durable.Failure{Message: reason, Final: true, RetryAfter: 0})
	}

	if err != nil {
		return claimError(err)
	}

	return c.applyEnd(ctx, pc.Job, &ending{fence: pc.Fence, event: ev, reason: reason, delivery: delivery})
}

// errRetryClaim marks a claim attempt that read a job which changed before
// the event: the claimer reads the job again.
var errRetryClaim = errors.New("job changed while it was claimed")

// assign moves the job of the claimed operation op to running. It reports
// false when the claim does not run a phase: the operation belongs to no
// phase, the job moved on, or the takeover rule failed the phase. Claim then
// looks for another operation.
func (c *DurableClaimer) assign(ctx context.Context, op *durable.Operation) (Assignment, bool, error) {
	id, phase, ok := parsePhaseKey(op.Key)
	if !ok {
		return Assignment{}, false, c.endOperation(ctx, op.Claim, reasonNoPhase)
	}

	var lastErr error

	for range claimAttempts {
		a, ok, err := c.claimOnce(ctx, op, id, phase)
		if errors.Is(err, errRetryClaim) {
			lastErr = err

			continue
		}

		return a, ok, err
	}

	return Assignment{}, false, c.giveBack(ctx, op.Claim, lastErr)
}

// claimOnce reads the job id and applies the event of the claim of op to it.
// It returns errRetryClaim when the job or its fence changed since the read.
func (c *DurableClaimer) claimOnce(
	ctx context.Context, op *durable.Operation, id ID, phase int,
) (Assignment, bool, error) {
	j, err := c.jobs.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return Assignment{}, false, c.endOperation(ctx, op.Claim, reasonNoPhase)
	}

	if err != nil {
		return Assignment{}, false, c.giveBack(ctx, op.Claim, err)
	}

	if j.Current != phase || phase >= len(j.Phases) || j.Phases[phase].Operation != string(op.ID) {
		return Assignment{}, false, c.endOperation(ctx, op.Claim, reasonNoPhase)
	}

	ev, why, ok := claimEvent(&j)
	if !ok {
		// The job moved on without this operation, for example by a cancel.
		// Releasing it ends it canceled when a cancel is requested.
		return Assignment{}, false, c.giveBack(ctx, op.Claim, nil)
	}

	fence, err := c.fence(ctx, &j)
	if err != nil {
		return Assignment{}, false, c.giveBack(ctx, op.Claim, err)
	}

	if ev == EventPhaseFailed {
		return Assignment{}, false, c.failPhase(ctx, op.Claim, &j, fence, why)
	}

	claimed, err := c.jobs.Apply(ctx, Change{
		Job: j.ID, Version: j.Version, Event: EventClaim, Wait: WaitNone, Reason: "", Dispatch: nil,
		PlanNotBefore: time.Time{}, Executor: op.Claim.Owner, Delivery: nil, MergedAt: time.Time{}, Approval: nil,
	}, fence, nil)
	if errors.Is(err, ErrConflict) || errors.Is(err, namespace.ErrFenced) {
		return Assignment{}, false, fmt.Errorf("%w: %w", errRetryClaim, err)
	}

	if err != nil {
		return Assignment{}, false, c.giveBack(ctx, op.Claim, err)
	}

	return assignmentOf(op, fence, &claimed), true, nil
}

// claimEvent returns the event of a new claim on the current phase of j and,
// for EventPhaseFailed, the reason. It reports false for a job in a state
// that holds no phase for a claim. A dispatched job takes its first claim. A
// running job takes a takeover, which AfterLostClaim can turn into a failure.
func claimEvent(j *Job) (Event, string, bool) {
	switch j.State { //nolint:exhaustive // Every other state holds no phase that a claim can take.
	case StateDispatched:
		return EventClaim, "", true
	case StateRunning:
		ev, why := AfterLostClaim(j.Spec.Steps, j.Phases[j.Current].Steps, EventClaim)

		return ev, why, true
	default:
		return "", "", false
	}
}

// assignmentOf returns the assignment of the claim of the operation op on
// the current phase of j.
func assignmentOf(op *durable.Operation, fence namespace.Fence, j *Job) Assignment {
	a := Assignment{
		Claim: PhaseClaim{Job: j.ID, Phase: j.Current, Claim: op.Claim, Fence: fence},
		Job:   *j,
		Plan:  nil,
	}

	if j.Current > 0 && j.Phases[j.Current].Kind == PhaseApply && j.Phases[j.Current-1].Kind == PhasePlan {
		a.Plan = j.Phases[j.Current-1].Plan
	}

	return a
}

// failPhase ends the claimed operation failed and the current phase of j
// with reason, as the rule of AfterLostClaim says. The claim runs nothing.
func (c *DurableClaimer) failPhase(
	ctx context.Context, claim durable.Claim, j *Job, fence namespace.Fence, reason string,
) error {
	err := c.operations.Fail(ctx, claim, durable.Failure{Message: reason, Final: true, RetryAfter: 0})
	if errors.Is(err, durable.ErrClaimLost) {
		// Another claimer holds the operation now, and ends the phase.
		return nil
	}

	if err != nil {
		return claimError(err)
	}

	return c.applyEnd(ctx, j.ID, &ending{fence: fence, event: EventPhaseFailed, reason: reason, delivery: nil})
}

// endOperation ends the claimed operation failed, because it has no phase
// to run, and returns the error of that write.
func (c *DurableClaimer) endOperation(ctx context.Context, claim durable.Claim, reason string) error {
	err := c.operations.Fail(ctx, claim, durable.Failure{Message: reason, Final: true, RetryAfter: 0})
	if err != nil && !errors.Is(err, durable.ErrClaimLost) {
		return claimError(err)
	}

	return nil
}

// giveBack releases the claim of an operation that the claimer cannot run
// now, and returns cause. The release uses a context that outlives the
// request, so a canceled request does not leave the lease on the operation.
func (c *DurableClaimer) giveBack(ctx context.Context, claim durable.Claim, cause error) error {
	err := c.operations.Release(context.WithoutCancel(ctx), claim)
	if err != nil && !errors.Is(err, durable.ErrClaimLost) {
		return errors.Join(cause, claimError(err))
	}

	return cause
}

// fence returns the current fence of the namespace of the unit of j.
func (c *DurableClaimer) fence(ctx context.Context, j *Job) (namespace.Fence, error) {
	ns, err := c.namespaces.GetNamespace(ctx, j.Spec.Unit.Namespace())
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("read namespace %s: %w", j.Spec.Unit.Namespace(), err)
	}

	return ns.Fence(), nil
}

// running checks the fence of pc, and returns the job of pc while it runs the
// phase of pc. It returns ErrClaimLost for a job that moved on.
func (c *DurableClaimer) running(ctx context.Context, pc *PhaseClaim) (Job, error) {
	if err := c.checkFence(ctx, pc); err != nil {
		return Job{}, err
	}

	j, err := c.jobs.Get(ctx, pc.Job)
	if err != nil {
		return Job{}, fmt.Errorf("read job %s: %w", pc.Job, err)
	}

	// Defense in depth: the token check of the durable store decides who holds
	// the phase. This check stays so that a claim for another phase or
	// another namespace than the job has cannot end the phase of that job.
	if j.Current != pc.Phase || pc.Phase >= len(j.Phases) ||
		j.Phases[pc.Phase].Operation != string(pc.Claim.Operation) ||
		j.Spec.Unit.Namespace() != pc.Fence.Namespace ||
		(j.State != StateRunning && j.State != StateCanceling) {
		return Job{}, fmt.Errorf("%w: %w: job %s is %s at phase %d", ErrClaimLost, durable.ErrClaimLost, j.ID, j.State,
			j.Current)
	}

	return j, nil
}

// checkFence returns namespace.ErrFenced when the fence of pc is not the
// current fence of its namespace. The job store checks the fence again in
// the transaction of each event.
func (c *DurableClaimer) checkFence(ctx context.Context, pc *PhaseClaim) error {
	ns, err := c.namespaces.GetNamespace(ctx, pc.Fence.Namespace)
	if err != nil {
		return fmt.Errorf("read namespace %s: %w", pc.Fence.Namespace, err)
	}

	if ns.FailoverVersion != pc.Fence.FailoverVersion {
		return fmt.Errorf("claim of job %s: %w", pc.Job, namespace.ErrFenced)
	}

	return nil
}

// ending is the event that ends or gives back a phase, with its fence, its
// reason and, for a deliver phase, its delivery.
type ending struct {
	fence    namespace.Fence
	event    Event
	reason   string
	delivery *DeliveryResult
}

// applyEnd applies the event of e to the job id, after the durable operation
// of the phase ended. It reads the job again after a concurrent change, for
// example a cancel, because every event it applies is valid in both running
// and canceling.
func (c *DurableClaimer) applyEnd(ctx context.Context, id ID, e *ending) error {
	var lastErr error

	for range claimAttempts {
		j, err := c.jobs.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("read job %s: %w", id, err)
		}

		_, err = c.jobs.Apply(ctx, Change{
			Job: id, Version: j.Version, Event: e.event, Wait: WaitNone, Reason: e.reason, Dispatch: nil,
			PlanNotBefore: time.Time{}, Executor: "", Delivery: e.delivery, MergedAt: time.Time{}, Approval: nil,
		}, e.fence, nil)
		if errors.Is(err, ErrConflict) {
			lastErr = err

			continue
		}

		if err != nil {
			return fmt.Errorf("apply %s to job %s: %w", e.event, id, err)
		}

		return nil
	}

	return fmt.Errorf("apply %s to job %s: %w", e.event, id, lastErr)
}

// claimError wraps the error of the durable store in the error of this
// port: the claim is lost or canceled, or the request is not valid.
func claimError(err error) error {
	switch {
	case errors.Is(err, durable.ErrClaimLost):
		return fmt.Errorf("%w: %w", ErrClaimLost, err)
	case errors.Is(err, durable.ErrCanceled):
		return fmt.Errorf("%w: %w", ErrCanceled, err)
	case errors.Is(err, durable.ErrInvalid):
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	default:
		return err
	}
}

// parsePhaseKey returns the job ID and the phase index of a start key made
// by PhaseKey, and false for any other key.
func parsePhaseKey(key string) (ID, int, bool) {
	i := strings.LastIndexByte(key, '/')
	if i <= 0 {
		return "", 0, false
	}

	phase, err := strconv.Atoi(key[i+1:])
	if err != nil || phase < 0 {
		return "", 0, false
	}

	id := ID(key[:i])
	if !strings.HasPrefix(string(id), IDPrefix) {
		return "", 0, false
	}

	return id, phase, true
}
