package job

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
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

// maxSkippedClaims is how many operations one Claim ends or gives up before
// it returns durable.ErrNoWork. Each skipped operation leaves its kind, so the
// bound is only a guard: a poll never spins on one operation.
const maxSkippedClaims = 64

// endTimeout is the time that the end of a claim has, from its start. The end
// does not use the context of the request, so a runner that drops its
// connection cannot stop it between steps.
const endTimeout = 30 * time.Second

// reasonNoPhase is the reason of an operation that no job phase holds. The
// claimer ends such an operation failed, so it never blocks a kind.
const reasonNoPhase = "the operation does not belong to the current phase of a job"

// reasonNotRunnable is the reason of an operation whose job cannot take a
// claim, for example a job that expired while it was dispatched.
const reasonNotRunnable = "the job cannot take a claim on its current phase"

// DurableClaimer is the Claimer over a durable.Store and a Store. A phase is
// a durable operation: the claim, its expiry and its cancel are the rules of
// package durable, and the claimer applies the matching event to the job.
//
// The end of a claim is one write. Release, Finish and the failure of a
// takeover pass the end of the durable operation in Change.End, so the store
// ends the operation and applies the event of the job in one transaction.
// That transaction checks the token of the claim and its expiry on the clock
// of the store, and the job store checks the fence, the version of the job
// and that the job is at the phase of the operation. So of several callers of
// one claim, one wins, and a holder that lost its claim to a takeover changes
// neither the operation nor the job. Nobody can see the end of the operation
// without its event, so a crash, a conflict, a stale fence or a dropped
// request leaves the phase either ended or as it was. The claim itself is two
// writes (the claim of the operation, then the event of the job): when a
// process stops between them, the claim expires and the next Claim takes the
// phase over.
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

	for range maxSkippedClaims {
		if err := ctx.Err(); err != nil {
			return Assignment{}, fmt.Errorf("claim: %w", err)
		}

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

	return Assignment{}, claimError(fmt.Errorf("claim: %d operations skipped: %w", maxSkippedClaims, durable.ErrNoWork))
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

// Release implements Claimer. It ends the claim with the context of
// endContext, so a canceled request does not stop it.
func (c *DurableClaimer) Release(ctx context.Context, pc PhaseClaim) error {
	ctx, cancel := endContext(ctx)
	defer cancel()

	return c.end(ctx, &pc, func(j *Job) (phaseEnd, error) {
		ev, why := AfterLostClaim(j.Spec.Steps, j.Phases[pc.Phase].Steps, EventRelease)

		if ev == EventPhaseFailed {
			return phaseEnd{
				event: ev, reason: why, delivery: nil,
				operation: OperationEnd{
					Claim: pc.Claim, Kind: EndFail, Failure: durable.Failure{Message: why, Final: true, RetryAfter: 0},
				},
			}, nil
		}

		return phaseEnd{
			event: ev, reason: why, delivery: nil,
			operation: OperationEnd{Claim: pc.Claim, Kind: EndRelease, Failure: noFailure()},
		}, nil
	})
}

// Finish implements Claimer. It ends the claim with the context of
// endContext, so a canceled request does not stop it. It returns ErrInvalid
// for a successful end of a deliver phase while the job has no delivery: the
// write-back executor calls Recorder.Delivered first.
func (c *DurableClaimer) Finish(ctx context.Context, pc PhaseClaim, o Outcome) error {
	ctx, cancel := endContext(ctx)
	defer cancel()

	return c.end(ctx, &pc, func(j *Job) (phaseEnd, error) {
		ev := FinishEvent(j.Phases[pc.Phase].Kind, pc.Phase == len(j.Phases)-1, o)

		var delivery *DeliveryResult

		if ev == EventDelivered {
			if j.Delivery == nil {
				return phaseEnd{}, fmt.Errorf("%w: job %s ended its deliver phase before it recorded the delivery",
					ErrInvalid, j.ID)
			}

			d := *j.Delivery
			delivery = &d
		}

		if o.Succeeded {
			return phaseEnd{
				event: ev, reason: "", delivery: delivery,
				operation: OperationEnd{Claim: pc.Claim, Kind: EndComplete, Failure: noFailure()},
			}, nil
		}

		reason := o.Message
		if reason == "" {
			reason = "the executor reported that the phase failed"
		}

		return phaseEnd{
			event: ev, reason: reason, delivery: delivery,
			operation: OperationEnd{
				Claim: pc.Claim, Kind: EndFail, Failure: durable.Failure{Message: reason, Final: true, RetryAfter: 0},
			},
		}, nil
	})
}

// endContext returns a context that outlives the request of ctx and has the
// time of endTimeout.
func endContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), endTimeout)
}

// noFailure is the failure of an end that is not a failure.
func noFailure() durable.Failure {
	return durable.Failure{Message: "", Final: false, RetryAfter: 0}
}

// phaseEnd is how a phase ends: the event of the job, its reason and delivery,
// and the end of the durable operation that commits with it.
type phaseEnd struct {
	event     Event
	reason    string
	delivery  *DeliveryResult
	operation OperationEnd
}

// end ends the claim pc in one write. plan returns the end for the job as it
// reads it. The store ends the durable operation and applies the event in
// one transaction, bound to the claim of pc. A concurrent change of the job,
// for example a cancel or a step record of the holder, makes the claimer read
// the job again and plan again.
func (c *DurableClaimer) end(ctx context.Context, pc *PhaseClaim, plan func(j *Job) (phaseEnd, error)) error {
	var lastErr error

	for range claimAttempts {
		j, err := c.running(ctx, pc)
		if err != nil {
			return err
		}

		pe, err := plan(&j)
		if err != nil {
			return err
		}

		_, err = c.jobs.Apply(ctx, Change{
			Job: pc.Job, Version: j.Version, Event: pe.event, Wait: WaitNone, Reason: pe.reason, Dispatch: nil,
			PlanNotBefore: time.Time{}, Executor: "", Delivery: pe.delivery, MergedAt: time.Time{}, Approval: nil,
			End: &pe.operation,
		}, pc.Fence, nil)
		if errors.Is(err, ErrConflict) {
			lastErr = err

			continue
		}

		if err != nil {
			return fmt.Errorf("end phase %d of job %s with %s: %w", pc.Phase, pc.Job, pe.event, claimError(err))
		}

		return nil
	}

	return fmt.Errorf("end phase %d of job %s: %w", pc.Phase, pc.Job, lastErr)
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
		// The job will not run this operation: it ended without it, for example
		// by a cancel or an expiry. Giving the operation back would make the next
		// Claim take it again without end, so the claimer ends it. A cancel that
		// is requested ends it canceled.
		return Assignment{}, false, c.endOperation(ctx, op.Claim, reasonNotRunnable)
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
		End: nil,
	}, fence, claimEntries(op, &j))
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
// with reason, as the rule of AfterLostClaim says, in one write bound to the
// claim. The claim runs nothing. It returns errRetryClaim when the job or its
// fence changed since the read.
func (c *DurableClaimer) failPhase(
	ctx context.Context, claim durable.Claim, j *Job, fence namespace.Fence, reason string,
) error {
	_, err := c.jobs.Apply(ctx, Change{
		Job: j.ID, Version: j.Version, Event: EventPhaseFailed, Wait: WaitNone, Reason: reason, Dispatch: nil,
		PlanNotBefore: time.Time{}, Executor: "", Delivery: nil, MergedAt: time.Time{}, Approval: nil,
		End: &OperationEnd{
			Claim: claim, Kind: EndFail, Failure: durable.Failure{Message: reason, Final: true, RetryAfter: 0},
		},
	}, fence, nil)

	switch {
	case errors.Is(err, durable.ErrClaimLost):
		// Another claimer holds the operation now, and ends the phase.
		return nil
	case errors.Is(err, ErrConflict) || errors.Is(err, namespace.ErrFenced):
		return fmt.Errorf("%w: %w", errRetryClaim, err)
	case err != nil:
		return c.giveBack(ctx, claim, claimError(err))
	default:
		return nil
	}
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

// claimEntries returns the audit entries of the claim op of the current
// phase of j: the entry of runner:claim for a phase that changes resources,
// and none for a phase that changes nothing. A takeover is a claim too. The
// entry commits with the event of the claim. The end of a phase, a release and
// a renewal write none.
func claimEntries(op *durable.Operation, j *Job) []audit.Entry {
	if j.Current >= len(j.Phases) || !j.Phases[j.Current].Kind.Changes() {
		return nil
	}

	unit := j.Spec.Unit

	return []audit.Entry{{
		At: time.Now().UTC(), Namespace: unit.Namespace(), Subject: op.Claim.Owner, IDPSubject: "", TokenID: "",
		Action: string(ActionRunnerClaim), Object: unit.String(), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: unit.UnitID(), PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: audit.PrincipalKindService, JobID: string(j.ID), Reason: "",
	}}
}

// claimError wraps the error of the durable store in the error of this
// port: the claim is lost or canceled, or the request is not valid.
func claimError(err error) error {
	switch {
	case errors.Is(err, ErrClaimLost) || errors.Is(err, ErrCanceled):
		return err
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
