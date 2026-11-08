package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

// ReasonNamespaceMissing is the reason of an operation whose job is in a
// namespace that does not exist. The claimer ends such an operation failed,
// so it never blocks a kind.
const ReasonNamespaceMissing = "the namespace of the job does not exist, so its phase cannot run"

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
// phase over. The event of the claim carries the claim (Change.Claim), so
// the store applies it only while the claim is the live claim of the
// operation: a claimer whose lease expired before its event gets
// ErrClaimLost from the store and leaves the operation to the holder after
// it.
//
// A claim that expires during a cancel does not leave the job canceling. The
// claimer asks for such an operation too (durable.ClaimRequest.TakeCanceled),
// and ends its phase with the rule of Release: the job ends canceled when no
// step that changes resources began, and failed when one began. The end of
// the operation commits with the event, as for a Release of a holder.
//
// Each claim meets the gates of the unit again, through the same GateGuard
// that the start of the job meets. A gate that someone saved after the start
// covers the job when a runner claims its phase. A claim that an enforced gate
// covers, or whose gates the guard cannot read, fails the phase in the write
// that ends the operation, with an audit entry of the refused claim. The
// runner gets no phase. See gateRefusal.
type DurableClaimer struct {
	jobs       Store
	operations durable.Store
	namespaces NamespaceReader
	gates      GateGuard
	now        func() time.Time
	log        *slog.Logger
}

var _ Claimer = (*DurableClaimer)(nil)

// ClaimerOption sets an option of a DurableClaimer.
type ClaimerOption func(*DurableClaimer)

// WithClaimerClock sets the time source of the claimer, for the time of its
// audit entries. The default is time.Now.
func WithClaimerClock(now func() time.Time) ClaimerOption {
	return func(c *DurableClaimer) { c.now = now }
}

// WithClaimerLogger sets the logger that records the end of a phase whose
// namespace does not exist. The default is a discard logger, so the claimer
// stays silent unless the caller passes a logger.
func WithClaimerLogger(logger *slog.Logger) ClaimerOption {
	return func(c *DurableClaimer) {
		if logger != nil {
			c.log = logger
		}
	}
}

// NewClaimer returns the claimer of the phases of jobs. namespaces gives the
// current fence of the namespace of a unit. gates tells which enforced gates
// cover the unit of a job at each claim. It is required: a claimer with a nil
// guard refuses every claim, so no phase runs past a gate that nobody checks.
// The logger of WithClaimerLogger records the end of a phase whose namespace
// does not exist.
func NewClaimer(
	jobs Store, operations durable.Store, namespaces NamespaceReader, gates GateGuard, opts ...ClaimerOption,
) *DurableClaimer {
	c := &DurableClaimer{
		jobs: jobs, operations: operations, namespaces: namespaces, gates: gates, now: time.Now,
		log: slog.New(slog.DiscardHandler),
	}

	for _, o := range opts {
		o(c)
	}

	return c
}

// Claim implements Claimer.
func (c *DurableClaimer) Claim(ctx context.Context, req ClaimRequest) (Assignment, error) {
	dreq := durable.ClaimRequest{
		Owner: req.Owner, Kinds: req.Kinds, MaxVersions: req.MaxVersions, Lease: req.Lease, TakeCanceled: true,
	}
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

	j, err := c.jobs.Get(ctx, pc.Job)
	if err != nil {
		return PhaseClaim{}, fmt.Errorf("read job %s: %w", pc.Job, err)
	}

	err = c.checkFence(ctx, &pc, &j)
	if err != nil {
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
		return releaseEnd(j, pc.Phase, pc.Claim), nil
	})
}

// releaseEnd returns the end of the phase of j that the holder of claim gives
// back, or that a claimer finds with a requested cancel: the rule of
// AfterLostClaim with EventRelease. A step that changes resources and began
// fails the phase. Else the event is EventRelease, which ends a canceling job
// canceled and returns a running job to its dispatch.
func releaseEnd(j *Job, phase int, claim durable.Claim) phaseEnd {
	ev, why := AfterLostClaim(j.Spec.Steps, j.Phases[phase].Steps, EventRelease)

	if ev == EventPhaseFailed {
		return phaseEnd{
			event: ev, reason: why, delivery: nil, entries: nil,
			operation: OperationEnd{
				Claim: claim, Kind: EndFail, Failure: durable.Failure{Message: why, Final: true, RetryAfter: 0},
			},
		}
	}

	return phaseEnd{
		event: ev, reason: why, delivery: nil, entries: nil,
		operation: OperationEnd{Claim: claim, Kind: EndRelease, Failure: noFailure()},
	}
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
				event: ev, reason: "", delivery: delivery, entries: nil,
				operation: OperationEnd{Claim: pc.Claim, Kind: EndComplete, Failure: noFailure()},
			}, nil
		}

		reason := o.Message
		if reason == "" {
			reason = "the executor reported that the phase failed"
		}

		return phaseEnd{
			event: ev, reason: reason, delivery: delivery, entries: nil,
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
	// entries are the audit entries that commit with the end, or nil.
	entries []audit.Entry
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
			Job:           pc.Job,
			Version:       j.Version,
			Event:         pe.event,
			Wait:          WaitNone,
			Reason:        pe.reason,
			Dispatch:      nil,
			PlanNotBefore: time.Time{},
			Executor:      "",
			Claim:         nil,
			Delivery:      pe.delivery,
			MergedAt:      time.Time{},
			Approval:      nil,
			End:           &pe.operation,
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
	var none Assignment

	j, err := c.jobs.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return none, false, c.endOperation(ctx, op.Claim, reasonNoPhase)
	}

	if err != nil {
		return none, false, c.giveBack(ctx, op.Claim, err)
	}

	if j.Current != phase || phase >= len(j.Phases) || j.Phases[phase].Operation != string(op.ID) {
		return none, false, c.endOperation(ctx, op.Claim, reasonNoPhase)
	}

	ev, why, ok := claimEvent(&j)
	if !ok {
		return c.endUnrunnable(ctx, op, &j)
	}

	fence, err := c.fence(ctx, &j)
	if err != nil {
		return none, false, c.giveBackOrEnd(ctx, &j, op.Claim, err)
	}

	if ev == EventPhaseFailed {
		pe := phaseEnd{
			event: ev, reason: why, delivery: nil, entries: nil,
			operation: OperationEnd{
				Claim: op.Claim, Kind: EndFail, Failure: durable.Failure{Message: why, Final: true, RetryAfter: 0},
			},
		}

		return none, false, c.endPhase(ctx, op.Claim, &j, fence, pe)
	}

	if pe, refused := c.gateRefusal(ctx, op, &j); refused {
		return none, false, c.endPhase(ctx, op.Claim, &j, fence, pe)
	}

	return c.applyClaim(ctx, op, &j, fence)
}

// The reasons of a claim that the gates refuse. Each is the reason of the
// failed phase and of the audit entry of the refused claim.
const (
	// reasonNoGateGuard is the reason of a claim on a claimer with no guard.
	reasonNoGateGuard = "the claim is refused: the server has no gate guard, so it cannot check the gates of the unit"
	// reasonGatesUnread is the reason of a claim whose gates the guard
	// cannot read.
	reasonGatesUnread = "the claim is refused: the gates of the unit cannot be read"
	// reasonGateCovers starts the reason of a claim that an enforced gate
	// covers. The text of the GateError follows it.
	reasonGateCovers = "the claim is refused: "
)

// gateRefusal checks the gates of the unit of j before the claim op runs its
// phase. It returns true and the end of the phase, with the audit entry of
// the refused claim, when an enforced gate at change.propose or state.write covers
// the unit, when the guard cannot answer, and when the claimer has no guard.
// The job token of the phase passes state.write with no evaluation, so this
// check is the only one between a gate saved after the start and the run of
// the phase. It fails closed: any answer but an empty list refuses.
//
// Defense in depth: the start of the job checks the same gates. This check
// stays so that a gate saved after the start, or a job that some other path
// stored, still stops at the claim, and it does not rely on the start check.
func (c *DurableClaimer) gateRefusal(ctx context.Context, op *durable.Operation, j *Job) (phaseEnd, bool) {
	reason, refused := c.gateReason(ctx, j)
	if !refused {
		return phaseEnd{
			event: "", reason: "", delivery: nil, entries: nil,
			operation: OperationEnd{Claim: op.Claim, Kind: "", Failure: noFailure()},
		}, false
	}

	return phaseEnd{
		event: EventPhaseFailed, reason: reason, delivery: nil,
		operation: OperationEnd{
			Claim: op.Claim, Kind: EndFail, Failure: durable.Failure{Message: reason, Final: true, RetryAfter: 0},
		},
		entries: []audit.Entry{c.claimEntry(op, j, audit.OutcomeDenied, reason)},
	}, true
}

// gateReason asks the guard for the enforced gates of the unit of j. It
// returns the reason of the refusal and true when the claim must not run.
func (c *DurableClaimer) gateReason(ctx context.Context, j *Job) (string, bool) {
	if c.gates == nil {
		return reasonNoGateGuard, true
	}

	enforced, err := c.gates.Enforced(ctx, j.Spec.Unit)
	if err != nil {
		// The text of the cause may hold detail of a driver, so the reason,
		// which the job and the audit entry show, is a fixed text.
		return reasonGatesUnread, true
	}

	if len(enforced) > 0 {
		return reasonGateCovers + (&GateError{Gates: enforced}).Error(), true
	}

	return "", false
}

// endUnrunnable ends the claimed operation op of the job j, which cannot take
// a claim. A canceling job got its cancel while the phase had no holder, for
// example when the lease of the last holder expired after the cancel. The
// claim then ends the phase with the rule of Release, so the job ends
// canceled and releases its unit, or fails when a change began. For any other
// job, for example one that expired, giving the operation back would make the
// next Claim take it again without end, so the claimer ends the operation.
func (c *DurableClaimer) endUnrunnable(ctx context.Context, op *durable.Operation, j *Job) (Assignment, bool, error) {
	var none Assignment

	if j.State != StateCanceling {
		return none, false, c.endOperation(ctx, op.Claim, reasonNotRunnable)
	}

	fence, err := c.fence(ctx, j)
	if err != nil {
		return none, false, c.giveBackOrEnd(ctx, j, op.Claim, err)
	}

	return none, false, c.endPhase(ctx, op.Claim, j, fence, releaseEnd(j, j.Current, op.Claim))
}

// applyClaim applies the event of the claim of op to j, bound to that claim.
func (c *DurableClaimer) applyClaim(
	ctx context.Context, op *durable.Operation, j *Job, fence namespace.Fence,
) (Assignment, bool, error) {
	var none Assignment

	claim := op.Claim

	claimed, err := c.jobs.Apply(ctx, Change{
		Job: j.ID, Version: j.Version, Event: EventClaim, Wait: WaitNone, Reason: "", Dispatch: nil,
		PlanNotBefore: time.Time{}, Executor: op.Claim.Owner, Claim: &claim, Delivery: nil, MergedAt: time.Time{},
		Approval: nil, End: nil,
	}, fence, c.claimEntries(op, j))

	switch {
	case errors.Is(err, durable.ErrClaimLost):
		// The lease of this claim expired before its event, and a later claim
		// holds the operation. That claim applies its own event.
		return none, false, nil
	case errors.Is(err, ErrConflict) || errors.Is(err, namespace.ErrFenced):
		return none, false, fmt.Errorf("%w: %w", errRetryClaim, err)
	case err != nil:
		return none, false, c.giveBack(ctx, op.Claim, err)
	default:
		return assignmentOf(op, fence, &claimed), true, nil
	}
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
		Claim:   PhaseClaim{Job: j.ID, Phase: j.Current, Claim: op.Claim, Fence: fence},
		Job:     *j,
		Plan:    nil,
		Profile: j.Spec.Profile,
	}

	if j.Current > 0 && j.Phases[j.Current].Kind == PhaseApply && j.Phases[j.Current-1].Kind == PhasePlan {
		a.Plan = j.Phases[j.Current-1].Plan
	}

	return a
}

// endPhase ends the claimed operation and the current phase of j with pe, as
// the rule of AfterLostClaim says, in one write bound to the claim. The claim
// runs nothing. It returns errRetryClaim when the job or its fence changed
// since the read.
func (c *DurableClaimer) endPhase(
	ctx context.Context, claim durable.Claim, j *Job, fence namespace.Fence, pe phaseEnd,
) error {
	_, err := c.jobs.Apply(ctx, Change{
		Job: j.ID, Version: j.Version, Event: pe.event, Wait: WaitNone, Reason: pe.reason, Dispatch: nil,
		PlanNotBefore: time.Time{}, Executor: "", Claim: nil, Delivery: nil, MergedAt: time.Time{}, Approval: nil,
		End: &pe.operation,
	}, fence, pe.entries)

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

// giveBackOrEnd gives back the claim of an operation whose namespace read
// failed, and ends it failed when the namespace does not exist. A namespace
// ID is random and is never given again, so the namespace of the job does
// not come back. Its name can come back, but a namespace that is created
// again under that name is another namespace, and the job must not run in
// it. Giving the operation back would make every poll claim it again and
// hold the kind of the operation.
//
// The end of the job record cannot carry this reason: every write of a job
// carries the fence of its namespace, and a missing namespace fails that
// check. So the failed operation is the record of the end, and the log line
// names it.
func (c *DurableClaimer) giveBackOrEnd(ctx context.Context, j *Job, claim durable.Claim, cause error) error {
	if !errors.Is(cause, namespace.ErrNotFound) {
		return c.giveBack(ctx, claim, cause)
	}

	c.log.WarnContext(ctx, "the phase ends failed: the namespace of the job does not exist",
		slog.String("job", string(j.ID)), slog.String("namespace", j.Spec.Unit.Namespace().String()),
		slog.String("operation", string(claim.Operation)), slog.String("reason", ReasonNamespaceMissing))

	return c.endOperation(ctx, claim, ReasonNamespaceMissing)
}

// giveBack gives back the claim of an operation that the claimer cannot run
// now, and returns cause. The give-back uses the context of endContext, so a
// canceled request does not leave the lease on the operation, and a database
// that hangs does not hold the poll for longer than endTimeout.
//
// It does not give back an operation with a requested cancel: a release
// would end the operation canceled and leave the job canceling with no
// operation. The store checks the cancel and writes in one transaction, so
// a cancel that arrives during the give-back cannot do that either. The claim
// then expires, and the next Claim takes the operation again.
func (c *DurableClaimer) giveBack(ctx context.Context, claim durable.Claim, cause error) error {
	ctx, cancel := endContext(ctx)
	defer cancel()

	err := c.operations.GiveBack(ctx, claim)
	if err != nil && !errors.Is(err, durable.ErrClaimLost) && !errors.Is(err, durable.ErrCanceled) {
		return errors.Join(cause, claimError(err))
	}

	return cause
}

// fence returns the current fence of the namespace of the unit of j. It
// returns an error that wraps namespace.ErrNotFound when the namespace that
// holds the name of the namespace of j is not the namespace that held the
// unit of j when the job started: a namespace that is created again under
// that name is another namespace, and the job must not run in it. A job with
// no namespace ID is refused the same way.
func (c *DurableClaimer) fence(ctx context.Context, j *Job) (namespace.Fence, error) {
	ns, err := c.namespaces.GetNamespace(ctx, j.Spec.Unit.Namespace())
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("read namespace %s: %w", j.Spec.Unit.Namespace(), err)
	}

	if j.NamespaceID == "" || ns.ID != j.NamespaceID {
		return namespace.Fence{}, fmt.Errorf("job %s: namespace %s has ID %s, not %s: %w",
			j.ID, ns.Name, ns.ID, j.NamespaceID, namespace.ErrNotFound)
	}

	return ns.Fence(), nil
}

// running checks the fence of pc, and returns the job of pc while it runs the
// phase of pc. It returns ErrClaimLost for a job that moved on.
func (c *DurableClaimer) running(ctx context.Context, pc *PhaseClaim) (Job, error) {
	j, err := c.jobs.Get(ctx, pc.Job)
	if err != nil {
		return Job{}, fmt.Errorf("read job %s: %w", pc.Job, err)
	}

	if err := c.checkFence(ctx, pc, &j); err != nil {
		return Job{}, err
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

// checkFence reads the namespace of pc and checks it against the fence of pc
// and the namespace ID of j. It returns namespace.ErrFenced when the fence of
// pc is not the current fence of its namespace, and ErrClaimLost when the job
// does not name the namespace that holds that name. The job store checks the
// fence again in the transaction of each event.
func (c *DurableClaimer) checkFence(ctx context.Context, pc *PhaseClaim, j *Job) error {
	ns, err := c.namespaces.GetNamespace(ctx, pc.Fence.Namespace)
	if err != nil {
		return fmt.Errorf("read namespace %s: %w", pc.Fence.Namespace, err)
	}

	if ns.FailoverVersion != pc.Fence.FailoverVersion {
		return fmt.Errorf("claim of job %s: %w", pc.Job, namespace.ErrFenced)
	}

	// Defense in depth: the claim-time check of fence compares the namespace
	// ID, and the store checks the fence of the namespace in the transaction
	// of each event. This check stays so that a renewal or an event cannot
	// use the claim of a job whose namespace a restore replaced under the
	// same name. It does not rely on the claim-time check or on the fence
	// check of the store.
	if j.NamespaceID == "" || ns.ID != j.NamespaceID {
		return fmt.Errorf("%w: %w: job %s: namespace %s has ID %s, not %s", ErrClaimLost,
			durable.ErrClaimLost, j.ID, ns.Name, ns.ID, j.NamespaceID)
	}

	return nil
}

// claimEntries returns the audit entries of the claim op of the current
// phase of j: the entry of runner:claim for a phase that changes resources,
// and none for a phase that changes nothing. A takeover is a claim too. The
// entry commits with the event of the claim. The end of a phase, a release and
// a renewal write none.
func (c *DurableClaimer) claimEntries(op *durable.Operation, j *Job) []audit.Entry {
	if j.Current >= len(j.Phases) || !j.Phases[j.Current].Kind.Changes() {
		return nil
	}

	return []audit.Entry{c.claimEntry(op, j, audit.OutcomeSuccess, "")}
}

// claimEntry returns the runner:claim entry of the claim op of the current
// phase of j, with its outcome and reason. A refused claim is an entry of
// every phase, also one that changes nothing.
func (c *DurableClaimer) claimEntry(op *durable.Operation, j *Job, outcome audit.Outcome, reason string) audit.Entry {
	unit := j.Spec.Unit

	return audit.Entry{
		At: c.now().UTC(), Namespace: unit.Namespace(), Subject: op.Claim.Owner, IDPSubject: "", TokenID: "",
		Action: string(ActionRunnerClaim), Object: unit.String(), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: outcome, RequestID: "", ObjectID: unit.UnitID(), PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: audit.PrincipalKindService, JobID: string(j.ID), Reason: reason,
	}
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
