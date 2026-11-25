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
//  3. A rejection by a person that is not the starter: job:cancel on the unit.
//  4. The state: a job that ended, a job that does not wait for approval,
//     and a phase with no approval gate that waits.
//  5. The fresh MFA step of the approval of a destroy.
//  6. An approval by an agent, by a subject that may not fill a slot, and by
//     the starter when no approval gate allows it.
//  7. The binding to the plan of the phase before, when there is one.
//
// A rejection by the starter needs checks 1, 2 and 4 only. A rejection by an
// agent is allowed, and a rejection never needs the plan or a fresh
// authentication: it only stops the change.
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

	if v == VerdictReject {
		err = s.checkRejector(ctx, &j, a)
		if err != nil {
			return Job{}, err
		}
	}

	// The setting is read once, so that the gates and the audit entry of this
	// verdict answer from one value.
	freshRequired := s.fresh.Required(ctx)

	gates, err := waitingGates(&j, s.approvers, freshRequired)
	if err != nil {
		return Job{}, err
	}

	now := s.now().UTC()
	ap := Approval{
		Gate: GateApproval, For: nil, Subject: a.Principal.Subject, Agent: string(a.Agent), Verdict: v,
		Comment: comment, Plan: nil, At: now, Auth: "", AuthAt: time.Time{},
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

	return s.record(ctx, &j, a, &ap, recordAs{action: ActionJobApprove, auth: ap.Auth, freshRequired: freshRequired})
}

// checkApproval runs the checks that only an approval needs. ap.Plan holds the
// binding of the caller, and stays as the binding to record.
func (s *service) checkApproval(ctx context.Context, j *Job, a Actor, gates []Gate, ap *Approval) error {
	if j.Spec.Operation == OperationDestroy {
		err := s.checkDestroyAuth(ctx, a, ap)
		if err != nil {
			return err
		}
	}

	err := s.checkApprover(ctx, j, a, gates, ap)
	if err != nil {
		return err
	}

	err = s.checkPlan(j, ap)
	if err != nil {
		return err
	}

	return nil
}

// checkDestroyAuth checks the fresh step of the approval of a destroy, and sets
// ap.Auth and ap.AuthAt to what the audit entry records. With the setting
// freshAuthEnabled off, the approval needs no step and records the
// authentication none. checkApprover still refuses an agent then.
//
// Defense in depth: the HTTP handler and the job store check the proof of a
// destroy too, with the same policy. This check stays so that another caller
// cannot skip it. It does not rely on them. A zero guard requires the step, so
// a service without a guard fails closed.
func (s *service) checkDestroyAuth(ctx context.Context, a Actor, ap *Approval) error {
	// The guard returns nil only when the policy requires no step. Any error
	// from it means that the step is required: the session proof below decides.
	if err := s.fresh.Require(ctx, a.Principal.Subject); err == nil {
		// Defense in depth: the HTTP handler and the job store also allow only a
		// person here. This check stays so that a service, a workload or an agent
		// never gets the authentication none, whatever credential it holds.
		if a.Agent != "" || a.Principal.Kind != identity.KindPerson {
			return fmt.Errorf("%w: only a person can approve a destroy when the setting is off", ErrAuthStale)
		}

		ap.Auth, ap.AuthAt = ConfirmAuthNone, time.Time{}

		return nil
	}

	fresh, err := newDestroyFresh(a, ap.At)
	if err != nil {
		return err
	}

	ap.Auth, ap.AuthAt = fresh.auth, fresh.at

	return nil
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
	return s.authorizedFor(ctx, a, id, ActionJobApprove)
}

// waitingGates returns the gates of type GateApproval of the current phase of
// j, when j waits for an approval on them. It returns ErrFinished for a job
// that ended, and ErrTransition for a job that does not wait, for a phase with
// no approval gate, and for a phase whose approval gates are open already (a
// confirmation gate may still wait: Confirm answers it).
func waitingGates(j *Job, policy gate.ApproverPolicy, freshRequired bool) ([]Gate, error) {
	if j.State.Terminal() {
		return nil, fmt.Errorf("%w: job %s is %s", ErrFinished, j.ID, j.State)
	}

	if j.State != StateAwaitingApproval || j.Current < 0 || j.Current >= len(j.Phases) {
		return nil, fmt.Errorf("%w: job %s is %s, not awaiting-approval", ErrTransition, j.ID, j.State)
	}

	phase := &j.Phases[j.Current]

	var gates []Gate

	for i := range phase.Gates {
		g := &phase.Gates[i]
		if typeOf(g.Type) == GateApproval {
			gates = append(gates, *g)
		}
	}

	if len(gates) == 0 {
		return nil, fmt.Errorf("%w: phase %d of job %s has no approval gate", ErrTransition, j.Current, j.ID)
	}

	status := GatesStatus(gates, phase.Approvals, j.StartedBy, policy, freshRequired)
	if status != GateWaiting {
		return nil, fmt.Errorf("%w: no approval gate of phase %d of job %s waits", ErrTransition, j.Current, j.ID)
	}

	return gates, nil
}

// checkApprover refuses an approval by an agent, by a subject that the policy
// does not count, by the starter when no approval gate allows it, by a subject
// that approved the phase already, and by a subject outside the groups of
// every gate. It sets ap.For to the names of the gates that the approval can
// fill.
func (s *service) checkApprover(ctx context.Context, j *Job, a Actor, gates []Gate, ap *Approval) error {
	if a.Agent != "" {
		return fmt.Errorf("%w: agent %q cannot approve; only a person can", ErrAgentDenied, clip(string(a.Agent)))
	}

	// Defense in depth: the matching does not count such a subject either, and
	// the write paths of the rules refuse it. This check stays so that an
	// approval that fills no slot is refused, not recorded in silence.
	if !s.approvers.CountsAsSignature(a.Principal.Subject) {
		return &DeniedError{
			Action: ActionJobApprove, Object: j.Spec.Unit,
			Err: fmt.Errorf("%w: this subject cannot fill an approval slot", identity.ErrDenied),
		}
	}

	// The subject is the one test for the starter: a user who started the job
	// through an agent is the starter by subject. Without a gate that allows
	// it, the starter cannot approve.
	starter := isStarter(j, a)
	if starter && !slices.ContainsFunc(gates, func(g Gate) bool { return g.AllowSelf }) {
		return fmt.Errorf("%w: ask another approver", ErrSelfApproval)
	}

	// An approval counts once, so a repeat only grows the record of the phase.
	if approvedAlready(&j.Phases[j.Current], a.Principal.Subject) {
		return fmt.Errorf("%w: wait for the other approvers", ErrAlreadyApproved)
	}

	holding, err := s.checkGroups(ctx, j, a.Principal.Subject, gates)
	if err != nil {
		return err
	}

	// The starter fills only a gate that allows it.
	if starter {
		holding = slices.DeleteFunc(holding, func(name string) bool {
			i := slices.IndexFunc(gates, func(g Gate) bool { return g.Name == name })

			return i < 0 || !gates[i].AllowSelf
		})

		if len(holding) == 0 {
			return fmt.Errorf("%w: ask another approver", ErrSelfApproval)
		}
	}

	ap.For = holding

	return nil
}

// approvedAlready reports whether subject has an approval on the phase.
func approvedAlready(p *Phase, subject string) bool {
	return slices.ContainsFunc(p.Approvals, func(ap Approval) bool {
		return ap.Subject == subject && ap.Verdict == VerdictApprove && typeOf(ap.Gate) == GateApproval
	})
}

// isStarter reports whether a is the principal that started j. A user who
// started the job through an agent is the starter by subject.
func isStarter(j *Job, a Actor) bool {
	return a.Principal.Subject == j.StartedBy
}

// checkRejector refuses a rejection by a person that is not the starter when
// that person cannot cancel the job. The starter may reject with job:approve
// only. A failure of the authorizer is a denial (fail closed).
//
// Defense in depth: the HTTP handler also authorizes job:cancel for this
// case. This check stays so that a caller of the service cannot skip it.
func (s *service) checkRejector(ctx context.Context, j *Job, a Actor) error {
	if isStarter(j, a) {
		return nil
	}

	err := s.authorizer.Authorize(ctx, a.Principal, ActionJobCancel, j.Spec.Unit)

	switch {
	case err == nil:
		return nil
	case errors.Is(err, identity.ErrDenied):
		return &DeniedError{Action: ActionJobCancel, Object: j.Spec.Unit, Err: err}
	default:
		// The authorizer failed. A failure allows no one, and the caller gets
		// a denial that the audit log records.
		return &DeniedError{
			Action: ActionJobCancel, Object: j.Spec.Unit,
			Err: fmt.Errorf("%w: %w: %w", identity.ErrDenied, ErrAuthorizerFailed, err),
		}
	}
}

// checkGroups returns the names of the gates that hold subject: the gates
// with no groups, and the gates whose groups have subject as an enabled
// member. The gate names its groups by ID, so a group that someone renamed
// still matches, and a group that someone made again with the old name does
// not. It is a *DeniedError on job:approve for a subject that no gate holds,
// for a failure of the reader, for no reader, and for a gate whose names have
// no IDs (fail closed). The text of a refusal that holds no gate names the
// requirements of the phase. A gate with no groups needs no reader.
//
// A rejection does not come here. Any holder of job:approve can start a
// rejection; Approve checks job:cancel for a rejection by a person that is
// not the starter.
func (s *service) checkGroups(ctx context.Context, j *Job, subject string, gates []Gate) ([]string, error) {
	denied := func(reason string, cause error) error {
		err := fmt.Errorf("%w: %s", identity.ErrDenied, reason)
		if cause != nil {
			err = fmt.Errorf("%w: %s: %w", identity.ErrDenied, reason, cause)
		}

		return &DeniedError{Action: ActionJobApprove, Object: j.Spec.Unit, Err: err}
	}

	var holding []string

	for i := range gates {
		g := &gates[i]
		if len(g.Groups) == 0 && len(g.GroupIDs) == 0 {
			holding = append(holding, g.Name)

			continue
		}

		switch {
		case len(g.GroupIDs) != len(g.Groups) || len(g.GroupIDs) == 0:
			return nil, denied("the gate names groups with no group IDs", nil)
		case s.groups == nil:
			return nil, denied("this server cannot read group membership", nil)
		}

		member, err := s.groups.MemberOfAny(ctx, subject, g.GroupIDs)

		switch {
		case err != nil:
			return nil, denied("the group membership cannot be read", err)
		case member:
			holding = append(holding, g.Name)
		default:
			// Not a member: the gate does not hold the subject.
		}
	}

	if len(holding) == 0 {
		return nil, &DeniedError{
			Action: ActionJobApprove, Object: j.Spec.Unit,
			Err: fmt.Errorf("%w: %w: the subject is not a member of a group of any requirement: %s",
				identity.ErrDenied, ErrNoRequirement, requirementNames(gates)),
		}
	}

	return holding, nil
}

// requirementNames returns the names of gates for an error text, clipped.
func requirementNames(gates []Gate) string {
	names := make([]string, 0, len(gates))

	for i := range gates {
		name := gates[i].Name
		if name == "" {
			name = "default"
		}

		names = append(names, clip(name))
	}

	return strings.Join(names, ", ")
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

// recordAs is what the audit entry of a verdict names: the action that allowed
// it, and the fresh authentication when there is one.
type recordAs struct {
	action identity.Action
	auth   ConfirmAuth
	// freshRequired is the answer of the fresh policy that the verdict read.
	// The gates of the verdict count with it.
	freshRequired bool
}

// record stores the verdict in one change with its audit entry. The event
// follows from the gates after the verdict: a rejection ends the job, the last
// needed approval opens it, and any other approval keeps it waiting.
func (s *service) record(ctx context.Context, j *Job, a Actor, ap *Approval, as recordAs) (Job, error) {
	phase := &j.Phases[j.Current]
	approvals := append(slices.Clone(phase.Approvals), *ap)

	c := Change{
		Job: j.ID, Version: j.Version, Event: EventApprovalRecorded, Wait: WaitNone, Reason: "", Dispatch: nil,
		PlanNotBefore: time.Time{}, Executor: "", Claim: nil, Delivery: nil, MergedAt: time.Time{}, Approval: ap,
		End: nil,
	}

	switch GatesStatus(phase.Gates, approvals, j.StartedBy, s.approvers, as.freshRequired) {
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

	e, err := s.approvalEntry(j, a, ap, as, c.Event)
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
	j *Job, a Actor, ap *Approval, as recordAs, ev Event,
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

	if len(ap.For) > 0 {
		changes = append(changes, audit.Change{
			Key: "requirements", Old: nil, New: strings.Join(ap.For, ","), Layer: "", TakesEffect: "",
		})
	}

	if ap.Plan != nil {
		changes = append(changes, audit.Change{Key: keyPlan, Old: nil, New: ap.Plan.Plan, Layer: "", TakesEffect: ""})
	}

	if as.auth != "" {
		changes = append(changes, audit.Change{Key: "auth", Old: nil, New: string(as.auth), Layer: "", TakesEffect: ""})
	}

	return audit.Entry{
		At: ap.At, Namespace: j.Spec.Unit.Namespace(), Subject: a.Principal.Subject, IDPSubject: "",
		TokenID: a.Principal.TokenID, Action: string(as.action), Object: j.Spec.Unit.String(), Version: "",
		ValuePath: "", ValueCount: nil, Changes: changes, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: j.Spec.Unit.UnitID(),
		PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: string(a.Principal.Kind), JobID: "",
		Reason: ap.Comment,
		Origin: origin,
	}, nil
}
