package job

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
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
//  4. The fresh MFA step of the approval of a destroy.
//  5. An approval by an agent, by a subject that may not fill a slot, and by
//     the starter when no approval gate allows it.
//  6. The binding to the plan of the phase before, when there is one.
//
// A rejection needs checks 1 to 3 only. A rejection by an agent is allowed,
// and a rejection never needs the plan or a fresh authentication: it only
// stops the change.
//
// The store records the verdict, and the event that follows from it, in one
// transaction with the audit entry. A second approval by one subject is
// recorded and counts once (Gate.Status). Group membership is checked by ID.
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
		Gate: GateApproval, Subject: a.Principal.Subject, Agent: string(a.Agent), Verdict: v, Comment: comment,
		Plan: nil, At: now, Auth: "", AuthAt: time.Time{},
	}

	if v == VerdictApprove {
		// The binding of the caller. checkPlan checks it, and drops it when no
		// plan phase comes before.
		ap.Plan = &plan

		err = s.checkApproval(ctx, &j, a, gates, &ap)
		if err != nil {
			return Job{}, err
		}
	}

	return s.record(ctx, &j, a, &ap, mfaOf(&j, v))
}

// checkApproval runs the checks that only an approval needs. ap.Plan holds the
// binding of the caller, and stays as the binding to record.
func (s *service) checkApproval(ctx context.Context, j *Job, a Actor, gates []Gate, ap *Approval) error {
	// Defense in depth: the HTTP handler refuses a destroy without session
	// proof too. This check stays so that another caller cannot skip it.
	if j.Spec.Operation == OperationDestroy {
		fresh, err := newDestroyFresh(a, ap.At)
		if err != nil {
			return err
		}

		ap.Auth, ap.AuthAt = fresh.auth, fresh.at
	}

	err := s.checkApprover(ctx, j, a, gates)
	if err != nil {
		return err
	}

	err = s.checkPlan(j, ap)
	if err != nil {
		return err
	}

	return nil
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
		if ident.IsHiddenRune(r) {
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
// does not count, by the starter when no approval gate allows it, by a subject
// that approved the phase already, and by a subject outside the groups of a
// gate.
func (s *service) checkApprover(ctx context.Context, j *Job, a Actor, gates []Gate) error {
	if a.Agent != "" {
		return fmt.Errorf("%w: agent %q cannot approve; only a person can", ErrAgentDenied, clip(string(a.Agent)))
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

	// An approval counts once, so a repeat only grows the record of the phase.
	if approvedAlready(&j.Phases[j.Current], a.Principal.Subject) {
		return fmt.Errorf("%w: wait for the other approvers", ErrAlreadyApproved)
	}

	return s.checkGroups(ctx, j, a.Principal.Subject, gates)
}

// approvedAlready reports whether subject has an approval on the phase.
func approvedAlready(p *Phase, subject string) bool {
	return slices.ContainsFunc(p.Approvals, func(ap Approval) bool {
		return ap.Subject == subject && ap.Verdict == VerdictApprove && typeOf(ap.Gate) == GateApproval
	})
}

// checkGroups refuses an approval by a subject that is not an enabled member of
// the groups of each gate that names groups. The gate names its groups by ID,
// so a group that someone renamed still matches, and a group that someone made
// again with the old name does not. It is a *DeniedError on job:approve for a
// non-member, for a failure of the reader, for no reader, and for a gate whose
// names have no IDs (fail closed). A gate with no groups needs no reader.
//
// A rejection does not come here: any holder of job:approve can reject, because
// a rejection only stops the change.
func (s *service) checkGroups(ctx context.Context, j *Job, subject string, gates []Gate) error {
	for i := range gates {
		g := &gates[i]
		if len(g.Groups) == 0 && len(g.GroupIDs) == 0 {
			continue
		}

		denied := func(reason string, cause error) error {
			err := fmt.Errorf("%w: %s", identity.ErrDenied, reason)
			if cause != nil {
				err = fmt.Errorf("%w: %s: %w", identity.ErrDenied, reason, cause)
			}

			return &DeniedError{Action: ActionJobApprove, Object: j.Spec.Unit, Err: err}
		}

		switch {
		case len(g.GroupIDs) != len(g.Groups) || len(g.GroupIDs) == 0:
			return denied("the gate names groups with no group IDs", nil)
		case s.groups == nil:
			return denied("this server cannot read group membership", nil)
		}

		member, err := s.groups.MemberOfAny(ctx, subject, g.GroupIDs)

		switch {
		case err != nil:
			return denied("the group membership cannot be read", err)
		case !member:
			return denied("the subject is not a member of a group of the gate", nil)
		}
	}

	return nil
}

// checkPlan checks the binding of the approver to the stored plan of the plan
// phase before the current phase. ap.Plan holds the binding of the caller. With
// no plan phase before, the phase has no plan to bind, and the binding is
// dropped (ap.Plan becomes nil). The state version of the plan is the one the
// plan read: a state change after the plan is caught by the store at the
// dispatch of the apply phase, which reads the current state version.
func (s *service) checkPlan(j *Job, ap *Approval) error {
	if j.Current == 0 || j.Phases[j.Current-1].Kind != PhasePlan {
		ap.Plan = nil

		return nil
	}

	stored := j.Phases[j.Current-1].Plan

	current := ""
	if stored != nil {
		current = stored.StateVersion
	}

	err := CheckPlan(stored, ap.Plan, current, ap.At.Add(-s.maxPlanAge))
	if err != nil {
		return fmt.Errorf("approve job %s: %w", j.ID, err)
	}

	return nil
}

// destroyFresh is the checked session proof of a destroy approval.
type destroyFresh struct {
	auth ConfirmAuth
	at   time.Time
}

// newDestroyFresh accepts only a person's console session. A token or an
// agent never gives a fresh step, even when its caller supplies an MFA time.
func newDestroyFresh(a Actor, now time.Time) (destroyFresh, error) {
	if a.Agent != "" || a.Principal.Kind != identity.KindPerson ||
		a.Principal.Credential != identity.CredentialSession || a.Principal.TokenID != "" ||
		a.ConfirmAuth != ConfirmAuthMFA || a.AuthAt.IsZero() || a.AuthAt.After(now) ||
		now.Sub(a.AuthAt) > ConfirmFreshness {
		return destroyFresh{}, fmt.Errorf("%w: the approval of a destroy needs an MFA step in the last %s",
			ErrAuthStale, ConfirmFreshness)
	}

	return destroyFresh{auth: ConfirmAuthMFA, at: a.AuthAt}, nil
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

	e, err := s.approvalEntry(j, a, ap, auth, c.Event)
	if err != nil {
		return Job{}, fmt.Errorf("record approval: %w", err)
	}

	fence, err := s.fences.Fence(ctx, j.Spec.Unit.Namespace())
	if err != nil {
		return Job{}, fmt.Errorf("fence of %s: %w", j.Spec.Unit.Namespace(), err)
	}

	stored, err := s.store.Apply(ctx, c, fence, []audit.Entry{e})
	if err != nil {
		return Job{}, fmt.Errorf("record approval: %w", err)
	}

	return stored, nil
}

// approvalEntry returns the audit entry of a verdict: who, on which unit, the
// job, the verdict, the phase, the event that followed, and the agent block
// and the authentication when there are any. The comment is the reason. An
// agent reference that the block cannot hold fails here, before any write.
func (s *service) approvalEntry(
	j *Job, a Actor, ap *Approval, auth ConfirmAuth, ev Event,
) (audit.Entry, error) {
	changes := []audit.Change{
		{Key: "job", Old: nil, New: string(j.ID), Layer: "", TakesEffect: ""},
		{Key: "verdict", Old: nil, New: string(ap.Verdict), Layer: "", TakesEffect: ""},
		{Key: "phase", Old: nil, New: string(j.Phases[j.Current].Kind), Layer: "", TakesEffect: ""},
		{Key: "operation", Old: nil, New: string(j.Spec.Operation), Layer: "", TakesEffect: ""},
		{Key: "event", Old: nil, New: string(ev), Layer: "", TakesEffect: ""},
	}

	// Defense in depth: the Validate check of the audit store refuses the
	// same values at the append. This check stays so that a malformed agent
	// reference fails the verdict before the fence and before any write, and
	// does not rely on the store.
	origin, err := audit.NewOrigin(audit.OriginArgs{
		Run: agentRunOf(a.Agent, audit.ChannelAPI), Session: agentSessionOf(a.Agent, audit.ChannelAPI),
		Tool: "", ArgsDigest: "", BreakGlass: false, SecretStage: "", DelegationID: "",
	})
	if err != nil {
		return audit.Entry{}, err
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
		Origin: origin,
	}, nil
}
