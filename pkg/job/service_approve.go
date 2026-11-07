package job

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Approve implements Service. Each check fails closed, and the order is the
// order of what a caller may learn:
//
//  1. The verdict and the comment, at the boundary.
//  2. The job, then job:read (a denial is ErrNotFound, as Get does), then
//     job:approve on the unit of the job.
//  3. The state: a job that ended, a job that does not wait for approval,
//     and a phase with no approval gate that waits.
//  4. An approval by an agent, by a subject that may not fill a slot, and by
//     the starter when no approval gate allows it.
//  5. The binding to the plan of the phase before, when there is one.
//  6. The fresh MFA step of the approval of a destroy.
//
// A rejection needs checks 1 to 3 only. A rejection by an agent is allowed,
// and a rejection never needs the plan or a fresh authentication: it only
// stops the change.
//
// The store records the verdict, and the event that follows from it, in one
// transaction with the audit entry. A second approval by one subject is
// recorded and counts once (Gate.Status). This version does not check the
// groups of an approver.
//
//nolint:revive // argument-limit: the signature is the one of job.Service.
func (s *service) Approve(
	ctx context.Context, a Actor, id ID, v Verdict, plan PlanBinding, comment string,
) (Job, error) {
	err := ctx.Err()
	if err != nil {
		return Job{}, fmt.Errorf("approve job: %w", err)
	}

	err = checkVerdict(v, comment)
	if err != nil {
		return Job{}, err
	}

	j, err := s.authorizedJob(ctx, a, id)
	if err != nil {
		return Job{}, err
	}

	gates, err := waitingGates(&j, s.approvers)
	if err != nil {
		return Job{}, err
	}

	now := s.now().UTC()
	ap := Approval{
		Gate: GateApproval, Subject: a.Principal.Subject, Agent: a.Agent, Verdict: v, Comment: comment,
		Plan: nil, At: now, Auth: "", AuthAt: time.Time{},
	}

	if v == VerdictApprove {
		err = s.checkApproval(&j, a, plan, gates, &ap)
		if err != nil {
			return Job{}, err
		}
	}

	return s.record(ctx, &j, a, &ap, mfaOf(&j, v))
}

// checkApproval runs the checks that only an approval needs, and sets the
// binding to record as ap.Plan.
func (s *service) checkApproval(j *Job, a Actor, bound PlanBinding, gates []Gate, ap *Approval) error {
	err := s.checkApprover(j, a, gates)
	if err != nil {
		return err
	}

	err = s.checkPlan(j, bound, ap.At, ap)
	if err != nil {
		return err
	}

	return checkDestroy(j, a, ap.At)
}

// mfaOf returns the authentication kind that the audit entry records, which
// is "mfa" for the approval of a destroy, else empty.
func mfaOf(j *Job, v Verdict) ConfirmAuth {
	if v == VerdictApprove && j.Spec.Operation == OperationDestroy {
		return ConfirmAuthMFA
	}

	return ""
}

// checkVerdict is the boundary check: an allowlist of the two verdicts and
// the rules of the audit reason for the comment (length, valid UTF-8, no
// control or format character). It refuses a value and never changes it.
func checkVerdict(v Verdict, comment string) error {
	if v != VerdictApprove && v != VerdictReject {
		return fmt.Errorf("%w: the verdict is %q, send approve or reject", ErrInvalid, clip(string(v)))
	}

	err := audit.ValidateReason(comment)
	if err != nil {
		return fmt.Errorf("%w: comment: %w", ErrInvalid, err)
	}

	return nil
}

// maxEcho bounds the text of a caller value that an error repeats.
const maxEcho = 32

// keyPlan is the key of the plan in the changes of the audit entry of a
// verdict.
const keyPlan = "plan"

// clip cuts s for an error text, so that a hostile value never fills an
// error: it keeps at most maxEcho bytes and replaces hidden runes.
func clip(s string) string {
	suffix := ""
	if len(s) > maxEcho {
		s, suffix = s[:maxEcho], "..."
	}

	s = strings.ToValidUTF8(s, "?")

	return strings.Map(func(r rune) rune {
		if audit.IsHiddenRune(r) {
			return '?'
		}

		return r
	}, s) + suffix
}

// authorizedJob returns the job id for a principal that may read it and
// approve on its unit. A principal that may not read it gets ErrNotFound, so
// it learns nothing about the job. A principal that may read it but not
// approve gets a *DeniedError, and an error of the authorizer is a denial
// too (fail closed).
func (s *service) authorizedJob(ctx context.Context, a Actor, id ID) (Job, error) {
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

	err = s.authorizer.Authorize(ctx, a.Principal, ActionJobApprove, j.Spec.Unit)

	switch {
	case err == nil:
		return j, nil
	case errors.Is(err, identity.ErrDenied):
		return Job{}, &DeniedError{Action: ActionJobApprove, Object: j.Spec.Unit, Err: err}
	default:
		// The authorizer failed. A failure allows no one, and the caller gets
		// a denial that the audit log records.
		return Job{}, &DeniedError{
			Action: ActionJobApprove, Object: j.Spec.Unit,
			Err: fmt.Errorf("%w: the authorizer failed: %w", identity.ErrDenied, err),
		}
	}
}

// waitingGates returns the gates of type GateApproval of the current phase of
// j, when j waits for an approval on them. It returns ErrFinished for a job
// that ended, and ErrTransition for a job that does not wait, for a phase with
// no approval gate, and for a phase whose approval gates are open already (a
// confirmation gate may still wait: Confirm answers it).
func waitingGates(j *Job, policy gate.ApproverPolicy) ([]Gate, error) {
	if j.State.Terminal() {
		return nil, fmt.Errorf("%w: job %s is %s", ErrFinished, j.ID, j.State)
	}

	if j.State != StateAwaitingApproval || j.Current < 0 || j.Current >= len(j.Phases) {
		return nil, fmt.Errorf("%w: job %s is %s, not awaiting-approval", ErrTransition, j.ID, j.State)
	}

	phase := &j.Phases[j.Current]

	var gates []Gate

	for _, g := range phase.Gates {
		if typeOf(g.Type) == GateApproval {
			gates = append(gates, g)
		}
	}

	if len(gates) == 0 {
		return nil, fmt.Errorf("%w: phase %d of job %s has no approval gate", ErrTransition, j.Current, j.ID)
	}

	if GatesStatus(gates, phase.Approvals, j.StartedBy, policy) != GateWaiting {
		return nil, fmt.Errorf("%w: no approval gate of phase %d of job %s waits", ErrTransition, j.Current, j.ID)
	}

	return gates, nil
}

// checkApprover refuses an approval by an agent, by a subject that the policy
// does not count, and by the starter when no approval gate allows it.
func (s *service) checkApprover(j *Job, a Actor, gates []Gate) error {
	if a.Agent != "" {
		return fmt.Errorf("%w: agent %q cannot approve; only a person can", ErrAgentDenied, clip(a.Agent))
	}

	// Defense in depth: Gate.Status does not count such a subject either, and
	// the write paths of the rules refuse it. This check stays so that an
	// approval that fills no slot is refused, not recorded in silence.
	if !s.approvers.CountsAsSignature(a.Principal.Subject) {
		return &DeniedError{
			Action: ActionJobApprove, Object: j.Spec.Unit,
			Err: fmt.Errorf("%w: this subject cannot fill an approval slot", identity.ErrDenied),
		}
	}

	// A user who started the job through an agent is the starter too, so the
	// subject is the one test.
	if a.Principal.Subject == j.StartedBy && !slices.ContainsFunc(gates, func(g Gate) bool { return g.AllowSelf }) {
		return fmt.Errorf("%w: ask another approver", ErrSelfApproval)
	}

	return nil
}

// checkPlan checks the binding of the approver to the stored plan of the plan
// phase before the current phase, and sets it as ap.Plan. With no plan phase
// before, the phase has no plan to bind, and ap.Plan stays nil. The state
// version of the plan is the one the plan read: a state change after the plan
// is caught by the store at the dispatch of the apply phase, which reads the
// current state version.
func (s *service) checkPlan(j *Job, bound PlanBinding, now time.Time, ap *Approval) error {
	if j.Current == 0 || j.Phases[j.Current-1].Kind != PhasePlan {
		return nil
	}

	stored := j.Phases[j.Current-1].Plan

	current := ""
	if stored != nil {
		current = stored.StateVersion
	}

	err := CheckPlan(stored, &bound, current, now.Add(-s.maxPlanAge))
	if err != nil {
		return fmt.Errorf("approve job %s: %w", j.ID, err)
	}

	ap.Plan = &bound

	return nil
}

// checkDestroy refuses the approval of a destroy that has no fresh step of
// the built-in MFA. The server sets Actor.ConfirmAuth and Actor.AuthAt from
// the sign-in of the principal; no client gives them. The step must not be
// after now and at most ConfirmFreshness before it.
//
// Defense in depth: the route can check the step too. This check stays so
// that a new caller of the service cannot approve a destroy without it.
func checkDestroy(j *Job, a Actor, now time.Time) error {
	if j.Spec.Operation != OperationDestroy {
		return nil
	}

	if a.ConfirmAuth != ConfirmAuthMFA || a.AuthAt.IsZero() || a.AuthAt.After(now) ||
		now.Sub(a.AuthAt) > ConfirmFreshness {
		return fmt.Errorf("%w: the approval of a destroy needs an MFA step in the last %s",
			ErrAuthStale, ConfirmFreshness)
	}

	return nil
}

// record stores the verdict in one change with its audit entry. The event
// follows from the gates after the verdict: a rejection ends the job, the last
// needed approval opens it, and any other approval keeps it waiting.
func (s *service) record(ctx context.Context, j *Job, a Actor, ap *Approval, auth ConfirmAuth) (Job, error) {
	phase := &j.Phases[j.Current]
	approvals := append(slices.Clone(phase.Approvals), *ap)

	c := Change{
		Job: j.ID, Version: j.Version, Event: EventApprovalRecorded, Wait: WaitNone, Reason: "", Dispatch: nil,
		PlanNotBefore: time.Time{}, Executor: "", Claim: nil, Delivery: nil, MergedAt: time.Time{}, Approval: ap,
		End: nil,
	}

	switch GatesStatus(phase.Gates, approvals, j.StartedBy, s.approvers) {
	case GateRejected:
		c.Event, c.Reason = EventRejected, "rejected by "+ap.Subject
	case GateOpen:
		c.Event = EventApproved
	case GateWaiting:
		// The gates need more approvals: the event stays EventApprovalRecorded.
	default:
		// An unknown status keeps the job waiting, which is the closed choice.
		c.Event = EventApprovalRecorded
	}

	fence, err := s.fences.Fence(ctx, j.Spec.Unit.Namespace())
	if err != nil {
		return Job{}, fmt.Errorf("fence of %s: %w", j.Spec.Unit.Namespace(), err)
	}

	stored, err := s.store.Apply(ctx, c, fence, []audit.Entry{s.approvalEntry(j, a, ap, auth, c.Event)})
	if err != nil {
		return Job{}, fmt.Errorf("record approval: %w", err)
	}

	return stored, nil
}

// approvalEntry returns the audit entry of a verdict: who, on which unit, the
// job, the verdict, the phase, the event that followed, and the agent and the
// authentication when there are any. The comment is the reason.
func (s *service) approvalEntry(j *Job, a Actor, ap *Approval, auth ConfirmAuth, ev Event) audit.Entry {
	changes := []audit.Change{
		{Key: "job", Old: nil, New: string(j.ID), Layer: "", TakesEffect: ""},
		{Key: "verdict", Old: nil, New: string(ap.Verdict), Layer: "", TakesEffect: ""},
		{Key: "phase", Old: nil, New: string(j.Phases[j.Current].Kind), Layer: "", TakesEffect: ""},
		{Key: "operation", Old: nil, New: string(j.Spec.Operation), Layer: "", TakesEffect: ""},
		{Key: "event", Old: nil, New: string(ev), Layer: "", TakesEffect: ""},
	}

	if a.Agent != "" {
		changes = append(changes, audit.Change{Key: "agent", Old: nil, New: a.Agent, Layer: "", TakesEffect: ""})
	}

	if ap.Plan != nil {
		changes = append(changes, audit.Change{Key: keyPlan, Old: nil, New: ap.Plan.Plan, Layer: "", TakesEffect: ""})
	}

	if auth != "" {
		changes = append(changes, audit.Change{Key: "auth", Old: nil, New: string(auth), Layer: "", TakesEffect: ""})
	}

	return audit.Entry{
		At: ap.At, Namespace: j.Spec.Unit.Namespace(), Subject: a.Principal.Subject, IDPSubject: "",
		TokenID: a.Principal.TokenID, Action: string(ActionJobApprove), Object: j.Spec.Unit.String(), Version: "",
		ValuePath: "", ValueCount: nil, Changes: changes, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: j.Spec.Unit.UnitID(),
		PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: string(a.Principal.Kind), JobID: "",
		Reason: ap.Comment,
	}
}
