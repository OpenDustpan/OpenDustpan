package job

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// The keys of the changes of a cancel entry.
const (
	changeKeyJob       = "job"
	changeKeyOperation = "operation"
	changeKeyAgent     = "agent"
)

// Cancel implements Service. Each check fails closed, and the order is the
// order of what a caller may learn:
//
//  1. The reason, at the boundary.
//  2. The job, then job:read (a denial is ErrNotFound, as Get does), then
//     job:cancel on the unit of the job (a denial, or a failure of the
//     authorizer, is a *DeniedError).
//  3. The state: CheckCancel gives ErrFinished and ErrDelivered.
//
// A job that is canceling already changes nothing: the call returns the job and
// writes no entry. Any other job gets EventCancel, which the store applies in
// one transaction with the audit entry and with the end of its durable
// operation.
func (s *service) Cancel(ctx context.Context, a Actor, id ID, reason string) (Job, error) {
	err := ctx.Err()
	if err != nil {
		return Job{}, fmt.Errorf("cancel job: %w", err)
	}

	err = audit.ValidateReason(reason)
	if err != nil {
		return Job{}, fmt.Errorf("%w: reason: %w", ErrInvalid, err)
	}

	j, err := s.authorizedFor(ctx, a, id, ActionJobCancel)
	if err != nil {
		return Job{}, err
	}

	err = CheckCancel(&j)
	if err != nil {
		return Job{}, err
	}

	// A second cancel changes nothing, so it leaves no second entry.
	if j.State == StateCanceling {
		return j, nil
	}

	fence, err := s.fences.Fence(ctx, j.Spec.Unit.Namespace())
	if err != nil {
		return Job{}, fmt.Errorf("fence of %s: %w", j.Spec.Unit.Namespace(), err)
	}

	c := Change{
		Job: j.ID, Version: j.Version, Event: EventCancel, Wait: WaitNone, Reason: reason, Dispatch: nil,
		PlanNotBefore: time.Time{}, Executor: "", Claim: nil, Delivery: nil, MergedAt: time.Time{}, Approval: nil,
		End: nil,
	}

	stored, err := s.store.Apply(ctx, c, fence, []audit.Entry{s.cancelEntry(&j, a, reason)})
	if err != nil {
		return Job{}, fmt.Errorf("cancel job: %w", err)
	}

	return stored, nil
}

// authorizedFor returns the job id for a principal that may read it and do
// action on its unit. A principal that may not read it gets ErrNotFound, so it
// learns nothing about the job. A principal that may read it but not do action
// gets a *DeniedError, and an error of the authorizer is a denial too (fail
// closed).
func (s *service) authorizedFor(ctx context.Context, a Actor, id ID, action identity.Action) (Job, error) {
	if !strings.HasPrefix(string(id), IDPrefix) {
		return Job{}, ErrNotFound
	}

	j, err := s.store.Get(ctx, id)
	if err != nil {
		return Job{}, fmt.Errorf("get job: %w", err)
	}

	err = s.authorizer.Authorize(ctx, a.Principal, ActionJobRead, j.Spec.Unit)

	switch {
	case err == nil:
	case errors.Is(err, identity.ErrDenied):
		return Job{}, ErrNotFound
	default:
		return Job{}, fmt.Errorf("authorize %s: %w", ActionJobRead, err)
	}

	err = s.authorizer.Authorize(ctx, a.Principal, action, j.Spec.Unit)

	switch {
	case err == nil:
		return j, nil
	case errors.Is(err, identity.ErrDenied):
		return Job{}, &DeniedError{Action: action, Object: j.Spec.Unit, Err: err}
	default:
		// The authorizer failed. A failure allows no one, and the caller gets
		// a denial that the audit log records.
		return Job{}, &DeniedError{
			Action: action, Object: j.Spec.Unit,
			Err: fmt.Errorf("%w: the authorizer failed: %w", identity.ErrDenied, err),
		}
	}
}

// cancelEntry returns the audit entry of a cancel: who, on which unit, the job,
// the state that the cancel found (the old value of the event), and the agent when there is one. The reason
// is the reason of the entry.
func (s *service) cancelEntry(j *Job, a Actor, reason string) audit.Entry {
	was := string(j.State)

	changes := []audit.Change{
		{Key: changeKeyJob, Old: nil, New: string(j.ID), Layer: "", TakesEffect: ""},
		{Key: "event", Old: &was, New: string(EventCancel), Layer: "", TakesEffect: ""},
		{Key: changeKeyOperation, Old: nil, New: string(j.Spec.Operation), Layer: "", TakesEffect: ""},
	}

	if a.Agent != "" {
		changes = append(changes, audit.Change{Key: changeKeyAgent, Old: nil, New: a.Agent, Layer: "", TakesEffect: ""})
	}

	return audit.Entry{
		At: s.now().UTC(), Namespace: j.Spec.Unit.Namespace(), Subject: a.Principal.Subject, IDPSubject: "",
		TokenID: a.Principal.TokenID, Action: string(ActionJobCancel), Object: j.Spec.Unit.String(), Version: "",
		ValuePath: "", ValueCount: nil, Changes: changes, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: j.Spec.Unit.UnitID(),
		PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: string(a.Principal.Kind), JobID: "",
		Reason: reason,
		Origin: nil,
	}
}
