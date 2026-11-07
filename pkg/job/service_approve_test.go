package job_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

const approveID = job.ID("job-approve")

var errApproveGrants = errors.New("grants store is down")

// noBinding returns the zero plan binding.
func noBinding() job.PlanBinding {
	var b job.PlanBinding

	return b
}

// bidiOverride is U+202E, built from its code point so that this file holds
// no bidirectional control character.
func bidiOverride() string { return string(rune(0x202e)) }

// openPolicy counts every subject but the internal ones.
func openPolicy() *gate.ApproverPolicy {
	return &gate.ApproverPolicy{Production: false, IsDev: nil}
}

// approveRig returns a rig whose service holds policy, and whose authorizer
// lets every subject read and approve on the unit. Tests that need a denial
// remove the entry or make the authorizer fail.
func approveRig(t *testing.T, policy *gate.ApproverPolicy) *rig {
	t.Helper()

	return newRig(t, func(cfg *job.ServiceConfig, r *rig) {
		cfg.Approvers = policy

		for _, action := range []identity.Action{job.ActionJobRead, job.ActionJobApprove} {
			r.authz.allow[string(action)+" "+r.unit.String()] = true
		}
	})
}

// approvePhase returns a phase of kind with the plan and the gates.
func approvePhase(kind job.PhaseKind, plan *job.PlanRef, gates []job.Gate) job.Phase {
	return job.Phase{
		Kind: kind, Operation: "", Claims: 0, Executor: "", Steps: nil, StateVersion: "", Plan: plan, Gates: gates,
		Approvals: nil, StartedAt: time.Time{}, FinishedAt: time.Time{},
	}
}

// partOf returns a plan part of kind with the hash.
func partOf(kind job.OutputKind, hash string) job.PlanPart {
	return job.PlanPart{Kind: kind, Size: 0, SHA256: hash, Secret: false, StoredSize: 0, StoredMD5: ""}
}

// twoApprovals is the gate of the seeded job: two approvals, bound to the plan.
func twoApprovals() job.Gate {
	return job.Gate{
		Type: job.GateApproval, Approvals: 2, Groups: nil, GroupIDs: nil, AllowSelf: false, Timeout: 0,
		BindPlan: true,
	}
}

// seed stores a job that waits for approval of its apply phase: a plan phase
// with a stored plan, and an apply phase with a gate of two approvals that
// binds the plan. alice started it. mod changes it before it is stored.
func (r *rig) seed(mod func(*job.Job)) job.Job {
	plan := r.storedPlan()

	j := job.Job{
		ID: approveID,
		Spec: job.Spec{
			Unit: r.unit, Operation: job.OperationApply, Purpose: "", Executor: job.ExecutorRunner,
			Location: emptyLocation(), Steps: nil, Pool: "pool-1", Profile: "", Edits: nil, Delivery: "",
			Defaulted: nil,
		},
		State: job.StateAwaitingApproval, Wait: job.WaitNone, Reason: "",
		Phases: []job.Phase{
			approvePhase(job.PhasePlan, &plan, nil),
			approvePhase(job.PhaseApply, nil, []job.Gate{twoApprovals()}),
		},
		Current: 1, StartedBy: "alice", Source: job.SourceAPI, Agent: "", AgentMode: "", BreakGlass: nil,
		Trigger: nil, Delivery: nil, Closed: nil, Verification: job.VerificationNone, IdempotencyKey: "",
		RequestHash: "", Version: 5, CreatedAt: time.Time{}, UpdatedAt: time.Time{}, StateChangedAt: time.Time{},
	}

	if mod != nil {
		mod(&j)
	}

	r.store.jobs[j.ID] = j

	return j
}

// storedPlan returns the stored plan of the seeded job, made an hour before now.
func (r *rig) storedPlan() job.PlanRef {
	return job.PlanRef{
		ID: "pln-1", ItemKey: "", StateVersion: "7", CreatedAt: r.now.Add(-time.Hour), ExpiresAt: time.Time{},
		RedactedAt: time.Time{},
		Parts: []job.PlanPart{
			partOf(job.OutputPlanDisplay, "aa"),
			partOf(job.OutputPlanApply, "bb"),
		},
	}
}

// binding is the binding of the seeded plan.
func (r *rig) binding() job.PlanBinding {
	p := r.storedPlan()

	return p.Binding()
}

func approvedBy(subject string) job.Approval {
	return job.Approval{
		Gate: job.GateApproval, Subject: subject, Agent: "", Verdict: job.VerdictApprove, Comment: "", Plan: nil,
		At: time.Time{}, Auth: "", AuthAt: time.Time{},
	}
}

func agentActor(subject, agent string) job.Actor {
	a := actorOf(subject)
	a.Agent = agent

	return a
}

func TestApproveRecordsTheVerdict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mod       func(*job.Job)
		actor     job.Actor
		verdict   job.Verdict
		wantEvent job.Event
	}{
		{
			"first of two approvals keeps the job waiting", nil, actorOf("carol"), job.VerdictApprove,
			job.EventApprovalRecorded,
		},
		{
			"second distinct approver opens the gate", func(j *job.Job) {
				j.Phases[1].Approvals = []job.Approval{approvedBy("dave")}
			}, actorOf("carol"), job.VerdictApprove, job.EventApproved,
		},
		{
			"the same approver twice counts once", func(j *job.Job) {
				j.Phases[1].Approvals = []job.Approval{approvedBy("carol")}
			}, actorOf("carol"), job.VerdictApprove, job.EventApprovalRecorded,
		},
		{"a rejection ends the job", nil, actorOf("carol"), job.VerdictReject, job.EventRejected},
		{"the starter may reject", nil, actorOf("alice"), job.VerdictReject, job.EventRejected},
		{"an agent may reject", nil, agentActor("carol", "dusty"), job.VerdictReject, job.EventRejected},
		{
			"the starter counts when the gate allows it", func(j *job.Job) {
				j.Phases[1].Gates[0].AllowSelf = true
				j.Phases[1].Approvals = []job.Approval{approvedBy("dave")}
			}, actorOf("alice"), job.VerdictApprove, job.EventApproved,
		},
		{
			"a job that an agent started needs another approver", func(j *job.Job) {
				j.Agent, j.AgentMode = "dusty", job.AgentModeApproveFirst
			}, actorOf("carol"), job.VerdictApprove, job.EventApprovalRecorded,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := approveRig(t, openPolicy())
			seeded := r.seed(tc.mod)

			_, err := r.svc.Approve(t.Context(), tc.actor, approveID, tc.verdict, r.binding(), "looks right")
			if err != nil {
				t.Fatalf("Approve = %v", err)
			}

			if len(r.store.applies) != 1 || len(r.store.applyEntries) != 1 || len(r.store.applyEntries[0]) != 1 {
				t.Fatalf("applies = %d, entries = %v, want one change with one entry", len(r.store.applies),
					r.store.applyEntries)
			}

			c := r.store.applies[0]
			if c.Event != tc.wantEvent || c.Job != approveID || c.Version != seeded.Version {
				t.Errorf("change = %s of %s at %d, want %s of %s at %d", c.Event, c.Job, c.Version, tc.wantEvent,
					approveID, seeded.Version)
			}

			checkApproval(t, &c, &tc.actor, tc.verdict, r.now)
			checkEntry(t, &r.store.applyEntries[0][0], &tc.actor, tc.verdict)
		})
	}
}

// checkApproval fails t when the approval of c is not the verdict of actor.
func checkApproval(t *testing.T, c *job.Change, actor *job.Actor, v job.Verdict, now time.Time) {
	t.Helper()

	ap := c.Approval
	if ap == nil {
		t.Fatal("change holds no approval")
	}

	if ap.Subject != actor.Principal.Subject || ap.Agent != actor.Agent || ap.Verdict != v ||
		ap.Comment != "looks right" || !ap.At.Equal(now) || ap.Gate != job.GateApproval {
		t.Errorf("approval = %+v, want the verdict %s of %s at %s", *ap, v, actor.Principal.Subject, now)
	}

	checkApprovalPlan(t, c, v)
}

// checkApprovalPlan fails t when an approval does not bind to the plan pln-1,
// or when a rejection has no reason.
func checkApprovalPlan(t *testing.T, c *job.Change, v job.Verdict) {
	t.Helper()

	if v == job.VerdictApprove && (c.Approval.Plan == nil || c.Approval.Plan.Plan != "pln-1") {
		t.Errorf("approval plan = %+v, want the binding of pln-1", c.Approval.Plan)
	}

	if v == job.VerdictReject && c.Reason == "" {
		t.Error("a rejection has no reason")
	}
}

// checkEntry fails t when e is not the audit entry of the verdict.
func checkEntry(t *testing.T, e *audit.Entry, actor *job.Actor, v job.Verdict) {
	t.Helper()

	if e.Action != string(job.ActionJobApprove) || e.Subject != actor.Principal.Subject ||
		e.Reason != "looks right" || e.Outcome != audit.OutcomeSuccess {
		t.Errorf("entry = %+v, want %s by %s with the comment", *e, job.ActionJobApprove, actor.Principal.Subject)
	}

	if !slices.ContainsFunc(e.Changes, func(c audit.Change) bool { return c.Key == "verdict" && c.New == string(v) }) {
		t.Errorf("entry changes = %+v, want the verdict %s", e.Changes, v)
	}
}

// refusalCase is one request that the service must refuse, with nothing
// written.
type refusalCase struct {
	name    string
	mod     func(*job.Job)
	actor   job.Actor
	verdict job.Verdict
	comment string
	bind    func(job.PlanBinding) job.PlanBinding
	policy  *gate.ApproverPolicy
	want    error
}

// refuse returns a case of an approval by carol of the seeded job that fails
// with want. The options change the request.
func refuse(name string, want error, opts ...func(*refusalCase)) refusalCase {
	c := refusalCase{
		name: name, mod: nil, actor: actorOf("carol"), verdict: job.VerdictApprove, comment: "", bind: nil,
		policy: nil, want: want,
	}

	for _, o := range opts {
		o(&c)
	}

	return c
}

func withMod(f func(*job.Job)) func(*refusalCase) { return func(c *refusalCase) { c.mod = f } }

func withActor(a job.Actor) func(*refusalCase) { return func(c *refusalCase) { c.actor = a } }

func withVerdict(v job.Verdict) func(*refusalCase) { return func(c *refusalCase) { c.verdict = v } }

func withComment(s string) func(*refusalCase) { return func(c *refusalCase) { c.comment = s } }

func withBind(f func(job.PlanBinding) job.PlanBinding) func(*refusalCase) {
	return func(c *refusalCase) { c.bind = f }
}

func withPolicy(p *gate.ApproverPolicy) func(*refusalCase) {
	return func(c *refusalCase) { c.policy = p }
}

// refusalCases are the hostile requests, each against one layer alone.
func refusalCases() []refusalCase {
	agedPlan := func(j *job.Job) { j.Phases[0].Plan.CreatedAt = j.Phases[0].Plan.CreatedAt.Add(-48 * time.Hour) }
	agentStarted := func(j *job.Job) { j.Agent, j.AgentMode = "dusty", job.AgentModeApproveFirst }
	devPolicy := &gate.ApproverPolicy{Production: true, IsDev: func(s string) bool { return s == "dev-user" }}

	return slices.Concat(
		refusalsOfTheBoundary(),
		[]refusalCase{
			refuse("principal that cannot read the job", job.ErrNotFound, withActor(actorOf("bob"))),
			refuse("job in queued", job.ErrTransition, withMod(func(j *job.Job) { j.State = job.StateQueued })),
			refuse("job that ended", job.ErrFinished, withMod(func(j *job.Job) { j.State = job.StateSucceeded })),
			refuse("rejection of a job that ended", job.ErrFinished, withVerdict(job.VerdictReject),
				withMod(func(j *job.Job) { j.State = job.StateCanceled })),
			refuse(
				"phase with no approval gate",
				job.ErrTransition,
				withMod(func(j *job.Job) { j.Phases[1].Gates = nil }),
			),
			refuse("phase with a confirmation gate only", job.ErrTransition, withMod(func(j *job.Job) {
				j.Phases[1].Gates = []job.Gate{job.ConfirmationGate(true)}
			})),
			refuse("approval gate that is open already", job.ErrTransition, withMod(func(j *job.Job) {
				j.Phases[1].Approvals = []job.Approval{approvedBy("dave"), approvedBy("erin")}
			})),
			refuse("approval by an agent", job.ErrAgentDenied, withActor(agentActor("carol", "dusty"))),
			refuse("approval by the starter", job.ErrSelfApproval, withActor(actorOf("alice"))),
			refuse("approval by the starter of a job that an agent started", job.ErrSelfApproval,
				withActor(actorOf("alice")), withMod(agentStarted)),
			refuse("subject that the policy does not count", identity.ErrDenied, withActor(actorOf("dev-user")),
				withPolicy(devPolicy)),
			refuse("plan of another ID", job.ErrPlanChanged,
				withBind(func(b job.PlanBinding) job.PlanBinding { b.Plan = "pln-2"; return b })),
			refuse("plan with another hash", job.ErrPlanChanged,
				withBind(func(b job.PlanBinding) job.PlanBinding { b.ApplySHA256 = "cc"; return b })),
			refuse(
				"no binding",
				job.ErrPlanChanged,
				withBind(func(job.PlanBinding) job.PlanBinding { return noBinding() }),
			),
			refuse("plan older than the maximum age", job.ErrPlanStale, withMod(agedPlan)),
			refuse(
				"plan phase with no stored plan",
				job.ErrPlanStale,
				withMod(func(j *job.Job) { j.Phases[0].Plan = nil }),
			),
			refuse("plan that was redacted", job.ErrPlanStale, withMod(func(j *job.Job) {
				j.Phases[0].Plan.RedactedAt = j.Phases[0].Plan.CreatedAt.Add(time.Minute)
			})),
			refuse("destroy with no MFA step", job.ErrAuthStale,
				withMod(func(j *job.Job) { j.Spec.Operation = job.OperationDestroy })),
		},
	)
}

// refusalsOfTheBoundary are the cases of the verdict and the comment.
func refusalsOfTheBoundary() []refusalCase {
	return []refusalCase{
		refuse("verdict with a command in it", job.ErrInvalid, withVerdict("approve; drop")),
		refuse("empty verdict", job.ErrInvalid, withVerdict("")),
		refuse("verdict in other case", job.ErrInvalid, withVerdict("Approve")),
		refuse("comment with a line break", job.ErrInvalid, withComment("fine\r\nSigned-off-by: root")),
		refuse("comment with a bidirectional override", job.ErrInvalid, withComment("fine"+bidiOverride())),
		refuse("comment over the limit", job.ErrInvalid, withComment(strings.Repeat("a", audit.MaxReasonLen+1))),
		refuse("comment that is not UTF-8", job.ErrInvalid, withComment("bad\xff")),
	}
}

func TestApproveRefusesAndWritesNothing(t *testing.T) {
	t.Parallel()

	for _, tc := range refusalCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policy := tc.policy
			if policy == nil {
				policy = openPolicy()
			}

			r := approveRig(t, policy)
			r.seed(tc.mod)

			bound := r.binding()
			if tc.bind != nil {
				bound = tc.bind(bound)
			}

			// The principal bob can read and approve nothing.
			r.authz.denied = "bob"

			_, err := r.svc.Approve(t.Context(), tc.actor, approveID, tc.verdict, bound, tc.comment)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Approve = %v, want %v", err, tc.want)
			}

			if len(r.store.applies) != 0 {
				t.Errorf("a refused approval wrote %d changes", len(r.store.applies))
			}
		})
	}
}

func TestApproveDestroyNeedsAFreshMFAStep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		auth job.ConfirmAuth
		age  time.Duration
		ok   bool
	}{
		{"MFA a minute ago", job.ConfirmAuthMFA, time.Minute, true},
		{"MFA at the limit", job.ConfirmAuthMFA, job.ConfirmFreshness, true},
		{"MFA just over the limit", job.ConfirmAuthMFA, job.ConfirmFreshness + time.Second, false},
		{"MFA a day ago", job.ConfirmAuthMFA, 24 * time.Hour, false},
		{"MFA in the future", job.ConfirmAuthMFA, -time.Minute, false},
		{"SSO a minute ago", job.ConfirmAuthSSO, time.Minute, false},
		{"unknown kind", job.ConfirmAuth("password"), time.Minute, false},
		{"no kind", "", time.Minute, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := approveRig(t, openPolicy())
			r.seed(func(j *job.Job) { j.Spec.Operation = job.OperationDestroy })

			actor := actorOf("carol")
			actor.ConfirmAuth, actor.AuthAt = tc.auth, r.now.Add(-tc.age)

			_, err := r.svc.Approve(t.Context(), actor, approveID, job.VerdictApprove, r.binding(), "")

			if tc.ok && err != nil {
				t.Fatalf("Approve = %v, want nil", err)
			}

			if !tc.ok && !errors.Is(err, job.ErrAuthStale) {
				t.Fatalf("Approve = %v, want ErrAuthStale", err)
			}

			wantChanges := 0
			if tc.ok {
				wantChanges = 1
			}

			if len(r.store.applies) != wantChanges {
				t.Errorf("changes = %d, want %d", len(r.store.applies), wantChanges)
			}
		})
	}
}

func TestApproveRejectionOfADestroyNeedsNoMFAStep(t *testing.T) {
	t.Parallel()

	r := approveRig(t, openPolicy())
	r.seed(func(j *job.Job) { j.Spec.Operation = job.OperationDestroy })

	_, err := r.svc.Approve(t.Context(), actorOf("carol"), approveID, job.VerdictReject, noBinding(), "")
	if err != nil {
		t.Fatalf("Approve = %v, want nil", err)
	}

	if len(r.store.applies) != 1 || r.store.applies[0].Event != job.EventRejected {
		t.Errorf("changes = %+v, want one rejection", r.store.applies)
	}
}

func TestApproveRefusesAPrincipalWithoutTheAction(t *testing.T) {
	t.Parallel()

	r := approveRig(t, openPolicy())
	delete(r.authz.allow, string(job.ActionJobApprove)+" "+r.unit.String())
	r.seed(nil)

	// The principal may read the job, so it knows that the job exists: the
	// answer is a denial of the action, not ErrNotFound.
	for _, v := range []job.Verdict{job.VerdictApprove, job.VerdictReject} {
		_, err := r.svc.Approve(t.Context(), actorOf("carol"), approveID, v, r.binding(), "")

		denied, ok := errors.AsType[*job.DeniedError](err)
		if !ok || !errors.Is(err, identity.ErrDenied) || denied.Action != job.ActionJobApprove {
			t.Errorf("Approve(%s) = %v, want a denial of job:approve", v, err)
		}
	}

	if len(r.store.applies) != 0 {
		t.Errorf("changes = %d, want none", len(r.store.applies))
	}
}

func TestApproveFailsClosedWhenTheAuthorizerFails(t *testing.T) {
	t.Parallel()

	r := approveRig(t, openPolicy())
	r.authz.fail[string(job.ActionJobApprove)+" "+r.unit.String()] = errApproveGrants
	r.seed(nil)

	_, err := r.svc.Approve(t.Context(), actorOf("carol"), approveID, job.VerdictApprove, r.binding(), "")

	denied, ok := errors.AsType[*job.DeniedError](err)
	if !ok || !errors.Is(err, identity.ErrDenied) || !errors.Is(err, errApproveGrants) {
		t.Fatalf("Approve = %v, want a denial that holds the failure", err)
	}

	if denied.Action != job.ActionJobApprove || denied.Object.String() != r.unit.String() {
		t.Errorf("denial = %s on %s, want job:approve on the unit", denied.Action, denied.Object)
	}

	if len(r.store.applies) != 0 {
		t.Errorf("changes = %d, want none", len(r.store.applies))
	}
}

func TestApproveWithNoPolicyNeverOpensTheGate(t *testing.T) {
	t.Parallel()

	r := approveRig(t, nil)
	r.seed(func(j *job.Job) { j.Phases[1].Approvals = []job.Approval{approvedBy("dave")} })

	_, err := r.svc.Approve(t.Context(), actorOf("carol"), approveID, job.VerdictApprove, r.binding(), "")
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Approve = %v, want a denial", err)
	}

	for _, c := range r.store.applies {
		if c.Event == job.EventApproved {
			t.Fatal("a service with no policy opened the gate")
		}
	}
}

func TestApproveRefusesAnUnknownOrForeignJobID(t *testing.T) {
	t.Parallel()

	r := approveRig(t, openPolicy())
	r.seed(nil)

	for _, id := range []job.ID{"", "x", "job-missing", "../job-approve"} {
		_, err := r.svc.Approve(t.Context(), actorOf("carol"), id, job.VerdictApprove, r.binding(), "")
		if !errors.Is(err, job.ErrNotFound) {
			t.Errorf("Approve(%q) = %v, want ErrNotFound", id, err)
		}
	}

	if len(r.store.applies) != 0 {
		t.Errorf("changes = %d, want none", len(r.store.applies))
	}
}

func TestApproveStopsOnACanceledContext(t *testing.T) {
	t.Parallel()

	r := approveRig(t, openPolicy())
	r.seed(nil)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := r.svc.Approve(ctx, actorOf("carol"), approveID, job.VerdictApprove, r.binding(), "")
	if !errors.Is(err, context.Canceled) || len(r.store.applies) != 0 {
		t.Fatalf("Approve = %v with %d changes, want context.Canceled and none", err, len(r.store.applies))
	}
}
