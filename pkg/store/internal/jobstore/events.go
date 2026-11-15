package jobstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
)

// change makes the fields of c on next, and checks the rules of its event.
// It keeps nothing on an error.
func change(ctx context.Context, tx Tx, next *job.Job, c *job.Change, now time.Time) error {
	if c.Approval != nil && next.Current < len(next.Phases) {
		next.Phases[next.Current].Approvals = append(next.Phases[next.Current].Approvals, *c.Approval)
	}

	//nolint:exhaustive // Every event that is not listed changes the state only.
	switch c.Event {
	case job.EventDispatch:
		return dispatch(ctx, tx, next, c, now)
	case job.EventClaim:
		return claim(ctx, tx, next, c, now)
	case job.EventReplan:
		return replan(next)
	case job.EventPhaseDone, job.EventLastPhaseDone, job.EventDelivered:
		return finish(next, c, now)
	case job.EventMerged:
		return merge(next, c)
	default:
		// Every other event changes the state only.
		return nil
	}
}

// dispatch checks the rules of a dispatch and starts its operation.
func dispatch(ctx context.Context, tx Tx, next *job.Job, c *job.Change, now time.Time) error {
	if c.Dispatch == nil {
		return fmt.Errorf("%w: a dispatch needs a start request", job.ErrInvalid)
	}

	return startDispatch(ctx, tx, next, c.Dispatch, dispatchTimes{notBefore: c.PlanNotBefore, now: now})
}

// claim records a claim or a takeover of the current phase. The claim of a
// plan phase also records the current state version of the unit.
func claim(ctx context.Context, tx Tx, next *job.Job, c *job.Change, now time.Time) error {
	if next.Current >= len(next.Phases) {
		return fmt.Errorf("%w: job %s has no phase to claim", job.ErrInvalid, next.ID)
	}

	p := &next.Phases[next.Current]

	if p.Kind == job.PhasePlan {
		v, err := tx.StateVersion(ctx, next.Spec.Unit.Namespace(), next.Spec.Unit.UnitID())
		if err != nil {
			return err
		}

		p.StateVersion = v
	}

	p.Claims++
	p.Executor = c.Executor

	if p.StartedAt.IsZero() {
		p.StartedAt = now
	}

	return nil
}

// replan moves the job back to the plan phase before the current phase.
func replan(next *job.Job) error {
	if next.Current == 0 || next.Phases[next.Current-1].Kind != job.PhasePlan {
		return fmt.Errorf("%w: no plan phase before phase %d", job.ErrInvalid, next.Current)
	}

	next.Phases[next.Current].Approvals = nil
	next.Current--
	next.Phases[next.Current].Plan = nil
	next.Phases[next.Current].StateVersion = ""
	next.Phases[next.Current].Operation = ""

	return nil
}

// finish ends the current phase, and keeps the delivery of a deliver phase.
func finish(next *job.Job, c *job.Change, now time.Time) error {
	if next.Current >= len(next.Phases) {
		return fmt.Errorf("%w: job %s has no phase to end", job.ErrInvalid, next.ID)
	}

	next.Phases[next.Current].FinishedAt = now
	next.Current++

	if c.Delivery != nil {
		d := *c.Delivery
		next.Delivery = &d
	}

	return nil
}

// merge records the merge of the review request.
func merge(next *job.Job, c *job.Change) error {
	if next.Delivery == nil || next.Delivery.Delivery != job.DeliveryReviewRequest || c.MergedAt.IsZero() {
		return fmt.Errorf("%w: a merge needs a review request and a time", job.ErrInvalid)
	}

	next.Delivery.MergedAt = c.MergedAt

	return nil
}

// afterChange starts or cancels the operations that the change of cur to
// next needs.
func afterChange(ctx context.Context, tx Tx, cur, next *job.Job, ev job.Event) error {
	if ev == job.EventCancel && cur.State.HoldsUnit() && cur.Current < len(cur.Phases) {
		id := durable.ID(cur.Phases[cur.Current].Operation)

		// A missing operation has nothing to cancel: a restore can leave a
		// job with no operation. The reconcile schedule ends such a job.
		err := tx.CancelOperation(ctx, id)
		if err != nil && !errors.Is(err, durable.ErrFinished) && !errors.Is(err, durable.ErrNotFound) {
			return fmt.Errorf("cancel %s: %w", id, err)
		}
	}

	if k, ok := followUpOf(next, ev); ok {
		return followUp(ctx, tx, next.ID, k)
	}

	return nil
}

// followUp starts the follow-up operation of kind k of the job id.
func followUp(ctx context.Context, tx CloseTx, id job.ID, k durable.Kind) error {
	req := durable.StartRequest{Key: job.FollowUpKey(id, k), Kind: k, Version: 1, Input: nil}

	if _, err := tx.StartOperation(ctx, &req); err != nil {
		return fmt.Errorf("start %s: %w", k, err)
	}

	return nil
}

// followUpOf returns the kind of the follow-up operation that the event ev
// to next starts, and false when it starts none.
func followUpOf(next *job.Job, ev job.Event) (durable.Kind, bool) {
	switch {
	case next.Delivery == nil:
		return "", false
	case ev == job.EventDelivered && next.Delivery.Delivery == job.DeliveryReviewRequest &&
		next.State == job.StateAwaitingState:
		return job.KindReviewWatch, true
	case ev == job.EventCancel || ev == job.EventRejected || ev == job.EventExpire:
		return job.KindAbandon, true
	default:
		return "", false
	}
}

// closeJob applies EventStateObserved with the closing version v to next,
// sets its verification to pending and starts its verify operation.
func closeJob(ctx context.Context, tx CloseTx, next *job.Job, v job.StateVersion) error {
	to, err := job.Next(next.State, job.EventStateObserved)
	if err != nil {
		return err
	}

	if err := followUp(ctx, tx, next.ID, job.KindVerify); err != nil {
		return err
	}

	closing := v
	next.State, next.Closed, next.Verification = to, &closing, job.VerificationPending

	return nil
}

// closeLate closes next on the last final state version of its unit, after
// EventMerged or EventDelivered, when that version closes it.
func closeLate(ctx context.Context, tx Tx, next *job.Job, ev job.Event) error {
	if ev != job.EventMerged && ev != job.EventDelivered {
		return nil
	}

	f, ok, err := tx.FinalVersion(ctx, next.Spec.Unit.Namespace(), next.Spec.Unit.UnitID())
	if err != nil || !ok {
		return err
	}

	v := job.StateVersion{
		Unit: next.Spec.Unit, VersionID: f.VersionID, Commit: f.Commit, Job: f.Job, At: f.At, Final: true,
	}
	if !next.ClosedBy(v) {
		return nil
	}

	return closeJob(ctx, tx, next, v)
}
