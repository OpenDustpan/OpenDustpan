package jobtest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/secret"
)

// approveCases returns the cases of the approvals of a job: how the store
// records a verdict with its event and its audit entry.
func approveCases() []suiteCase {
	return []suiteCase{
		{"approval: the first of two keeps the job waiting, the second queues it", testApprovalTwoApprovers},
		{
			"approval: several named gates keep their names and the approvals keep For, and the job opens after the third person",
			testApprovalSeveralGates,
		},
		{"approval: a rejection ends the job rejected", testApprovalRejected},
		{
			"approval: the event that records one approval is refused outside awaiting-approval",
			testApprovalRecordedElsewhere,
		},
		{"approval: a write on an older version is ErrConflict and records nothing", testApprovalConflict},
		{"approval: a dispatch refuses an approval bound to another plan", testApprovalOtherPlan},
		{"approval: a destroy needs fresh proof in the write transaction", testDestroyApprovalFresh},
		{"approval: a destroy records fresh proof and permits rejection without it", testDestroyApprovalProofStored},
	}
}

// needApprovals is the number of approvals of the gate of the cases.
const needApprovals = 2

// approverPolicy counts every subject, as a dev installation does.
func approverPolicy() gate.ApproverPolicy {
	return gate.ApproverPolicy{Production: false, IsDev: nil}
}

// awaitingApproval stores an apply job whose plan phase made a plan that
// read the current state version, and whose apply phase has one approval gate
// of two approvals that binds the plan. The job waits in awaiting-approval.
func awaitingApproval(t *testing.T, h Harness, f fixture) job.Job {
	t.Helper()

	return awaitingApprovalFor(t, h, f, job.OperationApply)
}

func awaitingApprovalFor(t *testing.T, h Harness, f fixture, op job.Operation) job.Job {
	t.Helper()

	return awaitingApprovalWith(t, h, f, op, []job.Gate{{
		Type: job.GateApproval, Name: "", Approvals: needApprovals, Groups: nil, GroupIDs: nil, AllowSelf: false,
		Timeout: 0, BindPlan: true,
	}})
}

// awaitingApprovalWith is awaitingApprovalFor with the gates of the apply
// phase.
func awaitingApprovalWith(t *testing.T, h Harness, f fixture, op job.Operation, gates []job.Gate) job.Job {
	t.Helper()

	// A harness without a state writer has no state version to bind.
	version := ""
	if h.WriteState != nil {
		version = writeState(t, h, f, false)
	}

	made := time.Now()

	j := newJob(f.unit, op, "")
	j.Current = 1
	j.Phases[0].StateVersion = version
	j.Phases[0].Plan = &job.PlanRef{
		ID: "pln-" + string(j.ID), ItemKey: secret.NewItemKeyID(), StateVersion: version,
		Parts: []job.PlanPart{
			{Kind: job.OutputPlanDisplay, Size: planSize, SHA256: "d1", Secret: false, StoredSize: 0, StoredMD5: ""},
			{Kind: job.OutputPlanApply, Size: planSize, SHA256: "a1", Secret: true, StoredSize: 0, StoredMD5: ""},
		},
		CreatedAt: made, ExpiresAt: history.ExpiresAt(made, history.DefaultRetention), RedactedAt: time.Time{},
	}
	j.Phases[1].Gates = gates

	stored := create(t, h, f, j, nil)

	return apply(t, h, f, change(&stored, job.EventNeedApproval))
}

func testDestroyApprovalFresh(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := awaitingApprovalFor(t, h, f, job.OperationDestroy)
	tests := []struct {
		name string
		edit func(*job.Approval)
	}{
		{"no proof", func(ap *job.Approval) { ap.Auth, ap.AuthAt = "", time.Time{} }},
		{"stale proof", func(ap *job.Approval) { ap.AuthAt = ap.At.Add(-time.Hour) }},
		{"future proof", func(ap *job.Approval) { ap.AuthAt = ap.At.Add(time.Hour) }},
		{"unknown proof", func(ap *job.Approval) { ap.Auth = "unknown" }},
		{"unknown verdict with MFA proof", func(ap *job.Approval) { ap.Verdict = "unknown" }},
		{"SSO proof", func(ap *job.Approval) { ap.Auth = job.ConfirmAuthSSO }},
		{"agent proof", func(ap *job.Approval) { ap.Agent = "agent" }},
		{"expired before write", func(ap *job.Approval) {
			ap.At = ap.At.Add(-time.Hour)
			ap.AuthAt = ap.At
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := verdict(&j, "cy", job.VerdictApprove, job.EventApprovalRecorded)
			c.Approval.Auth, c.Approval.AuthAt = job.ConfirmAuthMFA, c.Approval.At
			tc.edit(c.Approval)

			_, err := h.Jobs.Apply(t.Context(), c, f.fence, nil)
			if !errors.Is(err, job.ErrAuthStale) {
				t.Fatalf("Apply = %v, want ErrAuthStale", err)
			}

			got, err := h.Jobs.Get(t.Context(), j.ID)
			if err != nil || got.Version != j.Version || len(got.Phases[1].Approvals) != 0 {
				t.Fatal("the refused approval changed the job")
			}
		})
	}
}

func testDestroyApprovalProofStored(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := awaitingApprovalFor(t, h, f, job.OperationDestroy)
	c := verdict(&j, "cy", job.VerdictApprove, job.EventApprovalRecorded)
	c.Approval.Auth, c.Approval.AuthAt = job.ConfirmAuthMFA, c.Approval.At.Add(-time.Minute)
	stored := apply(t, h, f, c)
	ap := stored.Phases[1].Approvals[0]
	if ap.Auth != job.ConfirmAuthMFA || !ap.AuthAt.Equal(c.Approval.AuthAt) {
		t.Fatal("the store did not retain the approval proof")
	}

	rejected := apply(t, h, f, verdict(&stored, "di", job.VerdictReject, job.EventRejected))
	if rejected.State != job.StateRejected {
		t.Fatalf("state = %s, want rejected", rejected.State)
	}
}

// verdict returns the change that records the verdict v of subject on j, with
// the event ev. It binds to the plan of the phase before, as Approve does.
func verdict(j *job.Job, subject string, v job.Verdict, ev job.Event) job.Change {
	c := change(j, ev)
	c.Approval = &job.Approval{
		Gate: job.GateApproval, For: []string{""}, Subject: subject, Agent: "", Verdict: v, Comment: "ok", Plan: nil,
		At: time.Now().UTC(), Auth: "", AuthAt: time.Time{},
	}

	if v == job.VerdictApprove {
		b := j.Phases[j.Current-1].Plan.Binding()
		c.Approval.Plan = &b
	}

	if ev == job.EventRejected {
		c.Reason = "rejected by " + subject
	}

	return c
}

func testApprovalTwoApprovers(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := awaitingApproval(t, h, f)

	first := apply(t, h, f, verdict(&j, "cy", job.VerdictApprove, job.EventApprovalRecorded))
	checkApprovals(t, "first approval", &first, approvalsWant{
		state: job.StateAwaitingApproval, count: 1, status: job.GateWaiting,
	})

	if first.Version != j.Version+1 {
		t.Fatalf("version after the first approval = %d, want %d", first.Version, j.Version+1)
	}

	second := apply(t, h, f, verdict(&first, "di", job.VerdictApprove, job.EventApproved))
	checkApprovals(t, "second approver", &second, approvalsWant{
		state: job.StateQueued, count: needApprovals, status: job.GateOpen,
	})

	want := j.Phases[0].Plan.Binding()

	got := second.Phases[1].Approvals[1]
	if got.Subject != "di" || got.Comment != "ok" || got.Plan == nil || *got.Plan != want {
		t.Fatalf("stored approval = %+v, want the verdict of di with the plan binding", got)
	}
}

// approvalsWant is what a case expects of a job after a verdict: its state, the
// number of recorded approvals of the apply phase, and the status of its gates.
type approvalsWant struct {
	state  job.State
	count  int
	status job.GateStatus
}

// checkApprovals fails t when j is not as want says.
func checkApprovals(t *testing.T, name string, j *job.Job, want approvalsWant) {
	t.Helper()

	phase := &j.Phases[1]
	if j.State != want.state || len(phase.Approvals) != want.count {
		t.Fatalf("%s = %s, %d approvals; want %s, %d", name, j.State, len(phase.Approvals), want.state, want.count)
	}

	if s := job.GatesStatus(phase.Gates, phase.Approvals, j.StartedBy, approverPolicy()); s != want.status {
		t.Fatalf("status after the %s = %s, want %s", name, s, want.status)
	}
}

// severalGates returns the two named gates of the apply phase of the cases: a
// gate of two approvals and a gate of one.
func severalGates() []job.Gate {
	return []job.Gate{
		{
			Type: job.GateApproval, Name: "default/namespace/acme", Approvals: 2, Groups: []string{"platform"},
			GroupIDs: []string{"grp-a"}, AllowSelf: false, Timeout: 0, BindPlan: true,
		},
		{
			Type: job.GateApproval, Name: "default", Approvals: 1, Groups: nil, GroupIDs: nil, AllowSelf: false,
			Timeout: 0, BindPlan: true,
		},
	}
}

// verdictFor is verdict with the names of the gates that the approval fills.
func verdictFor(j *job.Job, subject string, ev job.Event, names ...string) job.Change {
	c := verdict(j, subject, job.VerdictApprove, ev)
	c.Approval.For = names

	return c
}

func testApprovalSeveralGates(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := awaitingApprovalWith(t, h, f, job.OperationApply, severalGates())

	if got := j.Phases[1].Gates; len(got) != 2 || got[0].Name != "default/namespace/acme" || got[1].Name != "default" ||
		got[0].Approvals != 2 || got[1].Approvals != 1 {
		t.Fatalf("stored gates = %+v, want the two named gates in order", got)
	}

	const ns, def = "default/namespace/acme", "default"

	first := apply(t, h, f, verdictFor(&j, "cy", job.EventApprovalRecorded, ns, def))
	checkApprovals(t, "first approval", &first, approvalsWant{
		state: job.StateAwaitingApproval, count: 1, status: job.GateWaiting,
	})

	second := apply(t, h, f, verdictFor(&first, "di", job.EventApprovalRecorded, ns))
	checkApprovals(t, "second approval", &second, approvalsWant{
		state: job.StateAwaitingApproval, count: 2, status: job.GateWaiting,
	})

	third := apply(t, h, f, verdictFor(&second, "ed", job.EventApproved, def))
	checkApprovals(t, "third approval", &third, approvalsWant{
		state: job.StateQueued, count: 3, status: job.GateOpen,
	})

	got, err := h.Jobs.Get(t.Context(), j.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	for i, want := range [][]string{{ns, def}, {ns}, {def}} {
		if !slices.Equal(got.Phases[1].Approvals[i].For, want) {
			t.Errorf("approval %d For = %v, want %v", i, got.Phases[1].Approvals[i].For, want)
		}
	}
}

func testApprovalRejected(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := awaitingApproval(t, h, f)

	got := apply(t, h, f, verdict(&j, "cy", job.VerdictReject, job.EventRejected))
	if got.State != job.StateRejected || got.Reason == "" || len(got.Phases[1].Approvals) != 1 ||
		got.Phases[1].Approvals[0].Verdict != job.VerdictReject {
		t.Fatalf("rejection = %s, reason %q, approvals %+v; want rejected with the verdict", got.State, got.Reason,
			got.Phases[1].Approvals)
	}
}

func testApprovalRecordedElsewhere(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	queued := create(t, h, f, newJob(f.unit, job.OperationApply, ""), nil)

	c := change(&queued, job.EventApprovalRecorded)
	c.Approval = &job.Approval{
		Gate: job.GateApproval, For: []string{""}, Subject: "cy", Agent: "", Verdict: job.VerdictApprove, Comment: "", Plan: nil,
		At: time.Now().UTC(), Auth: "", AuthAt: time.Time{},
	}

	_, err := h.Jobs.Apply(t.Context(), c, f.fence, nil)
	if !errors.Is(err, job.ErrTransition) {
		t.Fatalf("Apply(approval-recorded) on a queued job = %v, want ErrTransition", err)
	}

	got, err := h.Jobs.Get(t.Context(), queued.ID)
	if err != nil || got.Version != queued.Version || len(got.Phases[0].Approvals) != 0 {
		t.Fatalf("job after the refusal = version %d, %d approvals, %v; want nothing changed", got.Version,
			len(got.Phases[0].Approvals), err)
	}
}

func testApprovalConflict(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := awaitingApproval(t, h, f)

	apply(t, h, f, verdict(&j, "cy", job.VerdictApprove, job.EventApprovalRecorded))

	// A second approver read the job before the first approval was stored.
	_, err := h.Jobs.Apply(t.Context(), verdict(&j, "di", job.VerdictApprove, job.EventApproved), f.fence, nil)
	if !errors.Is(err, job.ErrConflict) {
		t.Fatalf("Apply on the older version = %v, want ErrConflict", err)
	}

	got, err := h.Jobs.Get(t.Context(), j.ID)
	if err != nil || got.State != job.StateAwaitingApproval || len(got.Phases[1].Approvals) != 1 {
		t.Fatalf("job after the conflict = %s, %d approvals, %v; want awaiting-approval, 1", got.State,
			len(got.Phases[1].Approvals), err)
	}
}

func testApprovalOtherPlan(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := awaitingApproval(t, h, f)

	// Both approvers saw another plan than the stored one.
	for i, subject := range []string{"cy", "di"} {
		ev := job.EventApprovalRecorded
		if i == 1 {
			ev = job.EventApproved
		}

		c := verdict(&j, subject, job.VerdictApprove, ev)
		c.Approval.Plan.ApplySHA256 = "other"
		j = apply(t, h, f, c)
	}

	if j.State != job.StateQueued {
		t.Fatalf("state = %s, want queued", j.State)
	}

	c := dispatch(&j)
	c.PlanNotBefore = time.Now().Add(-job.DefaultMaxPlanAge)

	_, err := h.Jobs.Apply(t.Context(), c, f.fence, nil)
	if !errors.Is(err, job.ErrPlanChanged) {
		t.Fatalf("dispatch with approvals of another plan = %v, want ErrPlanChanged", err)
	}
}
