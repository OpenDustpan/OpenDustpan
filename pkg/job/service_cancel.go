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
	if errors.Is(err, ErrDamaged) {
		return s.cancelDamaged(ctx, a, id, reason)
	}

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

// cancelDamaged cancels the job id, whose stored body does not decode, so that
// an operator can clear it. It checks job:read and job:cancel on the unit of
// the row, which it finds from the unit ID column of the row and not from the
// body, as Cancel does for a job that decodes. A row whose unit it cannot find
// is ErrNotFound, and no caller learns more. The store ends no durable
// operation for such a row: the job stays stopped and holds no body to read.
// It returns a Job that holds only what the row proves: the ID, the unit, the
// state canceled.
func (s *service) cancelDamaged(ctx context.Context, a Actor, id ID, reason string) (Job, error) {
	damage, unit, err := s.damagedUnit(ctx, id)
	if err != nil {
		return Job{}, err
	}

	err = s.authorizer.Authorize(ctx, a.Principal, ActionJobRead, unit)

	switch {
	case err == nil:
	case errors.Is(err, identity.ErrDenied):
		return Job{}, ErrNotFound
	default:
		return Job{}, fmt.Errorf("authorize %s: %w", ActionJobRead, err)
	}

	err = s.authorizer.Authorize(ctx, a.Principal, ActionJobCancel, unit)
	if err != nil {
		// Fail closed: a failure of the authorizer allows no one.
		return Job{}, &DeniedError{
			Action: ActionJobCancel, Object: unit, Err: fmt.Errorf("%w: %w", identity.ErrDenied, err),
		}
	}

	fence, err := s.fences.Fence(ctx, unit.Namespace())
	if err != nil {
		return Job{}, fmt.Errorf("fence of %s: %w", unit.Namespace(), err)
	}

	var j Job

	j.ID, j.Spec.Unit = id, unit

	entry := s.cancelEntry(&j, a, reason)
	entry.Changes = append(entry.Changes, audit.Change{
		Key: "damaged", Old: nil, New: damage, Layer: "", TakesEffect: "",
	})

	err = s.store.CancelDamaged(ctx, id, fence, []audit.Entry{entry})
	if errors.Is(err, ErrInvalid) {
		return Job{}, errRepaired
	}

	if err != nil {
		return Job{}, fmt.Errorf("cancel damaged job: %w", err)
	}

	j.State, j.Wait = StateCanceled, WaitNone

	return j, nil
}

// authorizedFor returns the job id for a principal that may read it and do
// action on its unit. A principal that may not read it gets ErrNotFound, so it
// learns nothing about the job. A principal that may read it but not do action
// gets a *DeniedError, and an error of the authorizer is a denial too (fail
// closed).
func (s *service) authorizedFor(ctx context.Context, a Actor, id ID, action identity.Action) (Job, error) {
	return s.authorizedBy(ctx, a, id, func(*Job) (identity.Action, error) { return action, nil })
}

// authorizedBy is authorizedFor for an action that depends on the job: pick
// gives the action after the caller may read the job. An error of pick is
// returned as it is, so a caller that may not read the job never sees it.
func (s *service) authorizedBy(
	ctx context.Context, a Actor, id ID, pick func(*Job) (identity.Action, error),
) (Job, error) {
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

	action, err := pick(&j)
	if err != nil {
		return Job{}, err
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
		changes = append(
			changes,
			audit.Change{Key: changeKeyAgent, Old: nil, New: string(a.Agent), Layer: "", TakesEffect: ""},
		)
	}

	return s.changeEntry(j, a, ActionJobCancel, changes, reason)
}

// changeEntry returns the audit entry of a change that the principal of a made
// to the unit of j with action. It holds the changes and the reason, and no
// request data.
func (s *service) changeEntry(
	j *Job, a Actor, action identity.Action, changes []audit.Change, reason string,
) audit.Entry {
	return audit.Entry{
		At: s.now().UTC(), Namespace: j.Spec.Unit.Namespace(), Subject: a.Principal.Subject, IDPSubject: "",
		TokenID: a.Principal.TokenID, Action: string(action), Object: j.Spec.Unit.String(), Version: "",
		ValuePath: "", ValueCount: nil, Changes: changes, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: j.Spec.Unit.UnitID(),
		PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: string(a.Principal.Kind), JobID: "",
		Reason: reason,
		Origin: nil,
	}
}

// damagedUnit reads the damaged row id and finds the unit that the row names.
// It returns the reason that the row is damaged and the unit.
// It checks that the unit is the one that the row names, in the namespace of
// the row.
func (s *service) damagedUnit(ctx context.Context, id ID) (string, identity.Object, error) {
	row, ns, err := s.store.GetDamaged(ctx, id)

	switch {
	case errors.Is(err, ErrInvalid):
		// The row was repaired after Get reported it as damaged. The caller
		// sends the request again and cancels the job through its body.
		return "", identity.Object{}, errRepaired
	case err != nil:
		return "", identity.Object{}, fmt.Errorf("get damaged job: %w", err)
	}

	unit, err := s.units.UnitByID(ctx, ns, row.UnitID)
	if err != nil {
		return "", identity.Object{}, fmt.Errorf("get unit of damaged job: %w", err)
	}

	// Defense in depth: the resolver looks the unit up by its ID, and the
	// store looks the row up by its ID alone. This check stays so that a
	// resolver or a store that answers for another namespace or another unit
	// cannot move the checks of the caller to a unit that the row does not name. It
	// does not rely on the checks of the resolver, of the store or of the
	// route of the API.
	if unit.Namespace() != ns || unit.UnitID() == "" || unit.UnitID() != row.UnitID {
		return "", identity.Object{}, ErrNotFound
	}

	return row.Reason, unit, nil
}

// errRepaired is the answer when the row of a job was repaired between the
// read that reported it as damaged and the cancel. It is an ErrConflict.
var errRepaired = fmt.Errorf(
	"%w: the stored job was repaired while it was canceled; send the request again",
	ErrConflict,
)
