package job_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// confirmRig returns a rig whose authorizer lets every subject read the job and
// start an apply on the unit, and whose service holds policy.
func confirmRig(t *testing.T, policy *gate.ApproverPolicy) *rig {
	t.Helper()

	return newRig(t, func(cfg *job.ServiceConfig, r *rig) {
		cfg.Approvers = policy

		for _, action := range []identity.Action{job.ActionJobRead, job.ActionUnitRunApply} {
			r.authz.allow[string(action)+" "+r.unit.String()] = true
		}
	})
}

// seedConfirm stores a job whose apply phase waits for a confirmation of alice,
// who started it. mod changes it before it is stored.
func (r *rig) seedConfirm(mod func(*job.Job)) job.Job {
	return r.seed(func(j *job.Job) {
		j.Phases[1].Gates = []job.Gate{job.ConfirmationGate(true)}

		if mod != nil {
			mod(j)
		}
	})
}

// requester returns alice with a fresh MFA step of a minute ago.
func (r *rig) requester() job.Actor {
	a := actorOf("alice")
	a.ConfirmAuth = job.ConfirmAuthMFA
	a.AuthAt = r.now.Add(-time.Minute)

	return a
}

func TestServiceActionsConfirmRecordsTheVerdict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		verdict   job.Verdict
		actor     func(*rig) job.Actor
		wantEvent job.Event
	}{
		{"approval by the requester opens the gate", job.VerdictApprove, (*rig).requester, job.EventApproved},
		{
			"rejection by the requester ends the job", job.VerdictReject,
			func(*rig) job.Actor { return actorOf("alice") }, job.EventRejected,
		},
		{
			"rejection needs no step and no requester", job.VerdictReject,
			func(*rig) job.Actor { return actorOf("carol") }, job.EventRejected,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := confirmRig(t, openPolicy())
			seeded := r.seedConfirm(nil)

			_, err := r.svc.Confirm(t.Context(), tc.actor(r), approveID, tc.verdict, r.binding())
			if err != nil {
				t.Fatalf("Confirm = %v", err)
			}

			checkConfirmChange(t, r, seeded.Version, tc.wantEvent, tc.verdict)
		})
	}
}

func checkConfirmChange(t *testing.T, r *rig, version int64, event job.Event, verdict job.Verdict) {
	t.Helper()

	if len(r.store.applies) != 1 || len(r.store.applyEntries) != 1 || len(r.store.applyEntries[0]) != 1 {
		t.Fatalf("applies = %d, want one change with one entry", len(r.store.applies))
	}

	c := r.store.applies[0]
	if c.Event != event || c.Job != approveID || c.Version != version || c.Approval == nil {
		t.Fatalf("change = %+v, want %s of %s at %d with an approval", c, event, approveID, version)
	}

	if c.Approval.Gate != job.GateConfirmation || c.Approval.Verdict != verdict {
		t.Errorf("approval = %+v, want a %s of the confirmation gate", c.Approval, verdict)
	}

	checkConfirmEntry(t, r, &r.store.applyEntries[0][0])
}

func checkConfirmEntry(t *testing.T, r *rig, e *audit.Entry) {
	t.Helper()

	if e.Action != string(job.ActionUnitRunApply) || e.Outcome != audit.OutcomeSuccess || e.Object != r.unit.String() {
		t.Errorf("entry = %+v, want %s on the unit", *e, job.ActionUnitRunApply)
	}
}

func TestServiceActionsConfirmRecordsTheStepAndThePlan(t *testing.T) {
	t.Parallel()

	r := confirmRig(t, openPolicy())
	r.seedConfirm(nil)

	actor := r.requester()

	_, err := r.svc.Confirm(t.Context(), actor, approveID, job.VerdictApprove, r.binding())
	if err != nil {
		t.Fatalf("Confirm = %v", err)
	}

	ap := r.store.applies[0].Approval
	if ap.Auth != job.ConfirmAuthMFA || !ap.AuthAt.Equal(actor.AuthAt) || ap.Plan == nil || ap.Plan.Plan != "pln-1" {
		t.Errorf("approval = %+v, want the MFA step of the actor and the plan binding", ap)
	}
}

// confirmCase is one request that Confirm must refuse, with nothing written.
type confirmCase struct {
	name    string
	mod     func(*job.Job)
	prep    func(*rig)
	id      job.ID
	actor   func(*rig) job.Actor
	verdict job.Verdict
	bound   func(*rig) job.PlanBinding
	policy  *gate.ApproverPolicy
	want    error
}

// confirmRefuses returns a case of a confirmation by alice with a fresh step,
// of the seeded job, that fails with want. The options change the request.
func confirmRefuses(name string, want error, opts ...func(*confirmCase)) confirmCase {
	c := confirmCase{
		name: name, mod: nil, prep: nil, id: approveID, actor: (*rig).requester, verdict: job.VerdictApprove,
		bound: (*rig).binding, policy: openPolicy(), want: want,
	}

	for _, o := range opts {
		o(&c)
	}

	return c
}

func cMod(f func(*job.Job)) func(*confirmCase) { return func(c *confirmCase) { c.mod = f } }

func cPrep(f func(*rig)) func(*confirmCase) { return func(c *confirmCase) { c.prep = f } }

func cID(id job.ID) func(*confirmCase) { return func(c *confirmCase) { c.id = id } }

func cActor(f func(*rig) job.Actor) func(*confirmCase) { return func(c *confirmCase) { c.actor = f } }

func cVerdict(v job.Verdict) func(*confirmCase) { return func(c *confirmCase) { c.verdict = v } }

func cBound(f func(*rig) job.PlanBinding) func(*confirmCase) {
	return func(c *confirmCase) { c.bound = f }
}

func cPolicy(p *gate.ApproverPolicy) func(*confirmCase) { return func(c *confirmCase) { c.policy = p } }

// actorAt returns the requester with the changes of f.
func actorAt(f func(*rig, *job.Actor)) func(*rig) job.Actor {
	return func(r *rig) job.Actor {
		a := r.requester()
		f(r, &a)

		return a
	}
}

// confirmRefusals are the hostile requests, each against one layer alone.
func confirmRefusals() []confirmCase {
	confirmed := func(j *job.Job) {
		ap := approvedBy("alice")
		ap.Gate = job.GateConfirmation
		j.Phases[1].Approvals = []job.Approval{ap}
	}
	allowed := func(r *rig, action identity.Action) string { return string(action) + " " + r.unit.String() }

	return []confirmCase{
		confirmRefuses("unknown job", job.ErrNotFound, cID("job-missing")),
		confirmRefuses("id with no job prefix", job.ErrNotFound, cID("x-1")),
		confirmRefuses("no read action", job.ErrNotFound,
			cPrep(func(r *rig) { delete(r.authz.allow, allowed(r, job.ActionJobRead)) })),
		confirmRefuses("read but no start action", identity.ErrDenied,
			cPrep(func(r *rig) { delete(r.authz.allow, allowed(r, job.ActionUnitRunApply)) })),
		confirmRefuses("the authorizer fails on the start action", identity.ErrDenied,
			cPrep(func(r *rig) { r.authz.fail[allowed(r, job.ActionUnitRunApply)] = errNoRights })),
		confirmRefuses("job that ended", job.ErrFinished, cMod(func(j *job.Job) { j.State = job.StateSucceeded })),
		confirmRefuses("job that does not wait", job.ErrTransition,
			cMod(func(j *job.Job) { j.State = job.StateRunning })),
		confirmRefuses("phase with no confirmation gate", job.ErrTransition,
			cMod(func(j *job.Job) { j.Phases[1].Gates = []job.Gate{twoApprovals()} })),
		confirmRefuses("confirmation recorded already", job.ErrTransition, cMod(confirmed)),
		confirmRefuses("agent", job.ErrAgentDenied, cActor(actorAt(func(_ *rig, a *job.Actor) { a.Agent = "bot" }))),
		confirmRefuses("agent rejection", job.ErrAgentDenied, cVerdict(job.VerdictReject),
			cActor(func(*rig) job.Actor { return agentActor("alice", "bot") })),
		confirmRefuses("not the requester", job.ErrNotRequester,
			cActor(actorAt(func(_ *rig, a *job.Actor) { a.Principal.Subject = "carol" }))),
		confirmRefuses("no fresh step", job.ErrAuthStale, cActor(func(*rig) job.Actor { return actorOf("alice") })),
		confirmRefuses("step too old", job.ErrAuthStale,
			cActor(actorAt(func(r *rig, a *job.Actor) { a.AuthAt = r.now.Add(-job.ConfirmFreshness - time.Second) }))),
		confirmRefuses("step in the future", job.ErrAuthStale,
			cActor(actorAt(func(r *rig, a *job.Actor) { a.AuthAt = r.now.Add(time.Minute) }))),
		confirmRefuses("step of an unknown kind", job.ErrAuthStale,
			cActor(actorAt(func(_ *rig, a *job.Actor) { a.ConfirmAuth = "sms" }))),
		// The step time is recent, so only the kind of the step refuses none.
		confirmRefuses("step of the authentication none", job.ErrAuthStale,
			cActor(actorAt(func(_ *rig, a *job.Actor) { a.ConfirmAuth = job.ConfirmAuthNone }))),
	}
}

// confirmRefusalsOfTheRest are the hostile requests of the plan, the verdict and
// the policy.
func confirmRefusalsOfTheRest() []confirmCase {
	other := func(*rig) job.PlanBinding {
		return job.PlanBinding{Plan: "pln-9", DisplaySHA256: "", ApplySHA256: "", StateVersion: ""}
	}
	closed := &gate.ApproverPolicy{Production: true, IsDev: nil}

	return []confirmCase{
		confirmRefuses("plan that is not the stored plan", job.ErrPlanChanged, cBound(other)),
		confirmRefuses("no plan binding", job.ErrPlanChanged,
			cBound(func(*rig) job.PlanBinding { return noBinding() })),
		confirmRefuses("plan that is too old", job.ErrPlanStale,
			cMod(func(j *job.Job) { j.Phases[0].Plan.CreatedAt = j.Phases[0].Plan.CreatedAt.Add(-48 * time.Hour) })),
		confirmRefuses("unknown verdict", job.ErrInvalid, cVerdict("maybe")),
		confirmRefuses("empty verdict", job.ErrInvalid, cVerdict("")),
		confirmRefuses("subject that the policy does not count", identity.ErrDenied, cPolicy(closed)),
	}
}

// TestServiceActionsConfirmRefuses holds the hostile cases: each guard of
// Confirm refuses and writes nothing.
func TestServiceActionsConfirmRefuses(t *testing.T) {
	t.Parallel()

	for _, tc := range append(confirmRefusals(), confirmRefusalsOfTheRest()...) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := confirmRig(t, tc.policy)
			r.seedConfirm(tc.mod)

			if tc.prep != nil {
				tc.prep(r)
			}

			_, err := r.svc.Confirm(t.Context(), tc.actor(r), tc.id, tc.verdict, tc.bound(r))
			if !errors.Is(err, tc.want) {
				t.Fatalf("Confirm = %v, want %v", err, tc.want)
			}

			if len(r.store.applies) != 0 {
				t.Errorf("a refused confirmation wrote %d changes", len(r.store.applies))
			}
		})
	}
}

func TestServiceActionsConfirmFailsClosedOnAStoreFault(t *testing.T) {
	t.Parallel()

	r := confirmRig(t, openPolicy())
	r.seedConfirm(nil)
	r.store.applyErr = errDriver

	_, err := r.svc.Confirm(t.Context(), r.requester(), approveID, job.VerdictApprove, r.binding())
	if !errors.Is(err, errDriver) {
		t.Fatalf("Confirm = %v, want the store fault", err)
	}
}

func TestServiceActionsConfirmNeedsTheFence(t *testing.T) {
	t.Parallel()

	r := newRig(t, func(cfg *job.ServiceConfig, r *rig) {
		cfg.Approvers = openPolicy()
		cfg.Fences = svcFences{err: errDriver}

		for _, action := range []identity.Action{job.ActionJobRead, job.ActionUnitRunApply} {
			r.authz.allow[string(action)+" "+r.unit.String()] = true
		}
	})
	r.seedConfirm(nil)

	_, err := r.svc.Confirm(t.Context(), r.requester(), approveID, job.VerdictApprove, r.binding())
	if !errors.Is(err, errDriver) || len(r.store.applies) != 0 {
		t.Fatalf("Confirm = %v with %d changes, want the fence error and no change", err, len(r.store.applies))
	}
}
