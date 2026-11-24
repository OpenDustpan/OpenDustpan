package job

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

// Confirm implements Service. Each check fails closed, and the order is the
// order of what a caller may learn:
//
//  1. The verdict, at the boundary.
//  2. The job, then job:read (a denial is ErrNotFound, as Get does), then the
//     start action of the job on its unit (a denial, or a failure of the
//     authorizer, is a *DeniedError).
//  3. The state: a job that ended, a job that does not wait, and a phase with
//     no confirmation gate that waits.
//  4. An agent, which can never confirm.
//  5. The requester: only the person who started the job confirms it.
//  6. The fresh authentication of that person.
//  7. The binding to the plan of the phase before, when there is one.
//
// A rejection needs checks 1 to 4 only: it stops the change and needs no
// proof.
//
// The store records the verdict, and the event that follows from it, in one
// transaction with the audit entry.
func (s *service) Confirm(ctx context.Context, a Actor, id ID, v Verdict, plan PlanBinding) (Job, error) {
	err := ctx.Err()
	if err != nil {
		return Job{}, fmt.Errorf("confirm job: %w", err)
	}

	err = checkVerdict(v, "")
	if err != nil {
		return Job{}, err
	}

	j, err := s.authorizedBy(ctx, a, id, ConfirmAction)
	if err != nil {
		return Job{}, err
	}

	action, err := ConfirmAction(&j)
	if err != nil {
		return Job{}, err
	}

	err = checkConfirmable(&j)
	if err != nil {
		return Job{}, err
	}

	// Defense in depth: Gate.Status does not count the confirmation of an
	// agent either. This check stays so that such a confirmation is refused, not
	// recorded in silence.
	if a.Agent != "" {
		return Job{}, fmt.Errorf("%w: agent %q cannot confirm; only the person who asked can", ErrAgentDenied,
			clip(string(a.Agent)))
	}

	now := s.now().UTC()
	ap := Approval{
		Gate: GateConfirmation, For: nil, Subject: a.Principal.Subject, Agent: "", Verdict: v, Comment: "",
		Plan: nil, At: now, Auth: "", AuthAt: time.Time{},
	}

	if v == VerdictApprove {
		err = s.checkConfirmation(&j, a, &ap, plan)
		if err != nil {
			return Job{}, err
		}
	}

	return s.record(ctx, &j, a, &ap, recordAs{action: action, auth: ap.Auth})
}

// ConfirmAction returns the action that a confirmation of j needs on its unit:
// the action that starts a job like j.
func ConfirmAction(j *Job) (identity.Action, error) {
	return startActionFor(j.Spec.Executor, j.Spec.Operation, j.Spec.Purpose)
}

// checkConfirmable returns ErrFinished for a job that ended, and ErrTransition
// for a job that does not wait for approval, and for a phase with no
// confirmation gate that waits.
func checkConfirmable(j *Job) error {
	if j.State.Terminal() {
		return fmt.Errorf("%w: job %s is %s", ErrFinished, j.ID, j.State)
	}

	if j.State != StateAwaitingApproval || j.Current < 0 || j.Current >= len(j.Phases) {
		return fmt.Errorf("%w: job %s is %s, not awaiting-approval", ErrTransition, j.ID, j.State)
	}

	phase := &j.Phases[j.Current]

	for i := range phase.Gates {
		g := &phase.Gates[i]
		// A confirmation gate waits until one confirmation is recorded.
		if typeOf(g.Type) == GateConfirmation && !hasConfirmation(phase) {
			return nil
		}
	}

	return fmt.Errorf("%w: no confirmation gate of phase %d of job %s waits", ErrTransition, j.Current, j.ID)
}

// hasConfirmation reports whether a verdict of type GateConfirmation is in the
// approvals of p.
func hasConfirmation(p *Phase) bool {
	for i := range p.Approvals {
		if typeOf(p.Approvals[i].Gate) == GateConfirmation {
			return true
		}
	}

	return false
}

// checkConfirmation runs the checks that only a confirmation needs: the
// requester, the policy, the fresh authentication and the plan. It records the
// authentication and the binding in ap.
func (s *service) checkConfirmation(j *Job, a Actor, ap *Approval, plan PlanBinding) error {
	if a.Principal.Subject != j.StartedBy {
		return fmt.Errorf("%w: ask %s to confirm", ErrNotRequester, clip(j.StartedBy))
	}

	// Defense in depth: Gate.Status does not count such a subject either. This
	// check stays so that a confirmation that fills no slot is refused, not
	// recorded in silence.
	if !s.approvers.CountsAsSignature(a.Principal.Subject) {
		return &DeniedError{
			Action: ActionJobApprove, Object: j.Spec.Unit,
			Err: fmt.Errorf("%w: this subject cannot confirm", identity.ErrDenied),
		}
	}

	if !a.ConfirmAuth.Valid() || a.AuthAt.IsZero() || a.AuthAt.After(ap.At) || ap.At.Sub(a.AuthAt) > ConfirmFreshness {
		return fmt.Errorf("%w: the confirmation needs a step in the last %s", ErrAuthStale, ConfirmFreshness)
	}

	ap.Auth, ap.AuthAt = a.ConfirmAuth, a.AuthAt
	ap.Plan = &plan

	return s.checkPlan(j, ap)
}
