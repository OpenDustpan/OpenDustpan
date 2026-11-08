package job_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/secret"
)

// devInstallation is the policy of a dev installation: every signature counts.
//
//nolint:gochecknoglobals // A frozen policy shared by the tests of this file.
var devInstallation = gate.ApproverPolicy{Production: false, IsDev: nil}

func TestGateStatus(t *testing.T) {
	t.Parallel()

	approve := func(subject string) job.Approval {
		return job.Approval{
			Gate: job.GateApproval, Subject: subject, Agent: "", Verdict: job.VerdictApprove, Comment: "",
			Plan: nil, At: time.Time{}, Auth: "", AuthAt: time.Time{},
		}
	}
	reject := func(subject string) job.Approval {
		a := approve(subject)
		a.Verdict = job.VerdictReject

		return a
	}

	tests := []struct {
		name      string
		need      int
		allowSelf bool
		approvals []job.Approval
		want      job.GateStatus
	}{
		{"no gate", 0, false, nil, job.GateOpen},
		{"one of one", 1, false, []job.Approval{approve("bo")}, job.GateOpen},
		{"none of one", 1, false, nil, job.GateWaiting},
		{"starter does not count", 1, false, []job.Approval{approve("al")}, job.GateWaiting},
		{"starter counts when allowed", 1, true, []job.Approval{approve("al")}, job.GateOpen},
		{"a subject counts once", 2, false, []job.Approval{approve("bo"), approve("bo")}, job.GateWaiting},
		{"two of two", 2, false, []job.Approval{approve("bo"), approve("cy")}, job.GateOpen},
		{"one rejection rejects", 1, false, []job.Approval{approve("bo"), reject("cy")}, job.GateRejected},
		{"rejection by the starter rejects", 1, false, []job.Approval{reject("al")}, job.GateRejected},
		{"rejection with no gate rejects", 0, false, []job.Approval{reject("bo")}, job.GateRejected},
		{
			"an approval through an agent does not count",
			1, false,
			[]job.Approval{viaAgent(approve("bo"))},
			job.GateWaiting,
		},
		{
			"a rejection through an agent rejects",
			1, false,
			[]job.Approval{viaAgent(reject("bo"))},
			job.GateRejected,
		},
		{
			"an empty gate type is an approval",
			1, false,
			[]job.Approval{withGate(approve("bo"), "")},
			job.GateOpen,
		},
		{
			"a confirmation is not an approval",
			1, true,
			[]job.Approval{confirmation("al", time.Minute)},
			job.GateWaiting,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := job.Gate{
				Type:      job.GateApproval,
				Approvals: tt.need,
				Groups:    nil,
				GroupIDs:  nil,
				AllowSelf: tt.allowSelf,
				Timeout:   0,
				BindPlan:  false,
			}
			if got := g.Status(tt.approvals, "al", devInstallation); got != tt.want {
				t.Fatalf("Status() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestConfirmationGateStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		approvals []job.Approval
		want      job.GateStatus
	}{
		{"no confirmation", nil, job.GateWaiting},
		{"the requester with a fresh MFA step", []job.Approval{confirmation("al", time.Minute)}, job.GateOpen},
		{
			"the requester at the end of the window",
			[]job.Approval{confirmation("al", job.ConfirmFreshness)},
			job.GateOpen,
		},
		{
			"the requester with an old MFA step",
			[]job.Approval{confirmation("al", job.ConfirmFreshness+time.Second)},
			job.GateWaiting,
		},
		{"the requester with no MFA step", []job.Approval{noAuth(confirmation("al", 0))}, job.GateWaiting},
		{
			"the requester with a fresh SSO sign-in",
			[]job.Approval{withAuth(confirmation("al", time.Minute), job.ConfirmAuthSSO)},
			job.GateOpen,
		},
		{
			"the requester with an old SSO sign-in",
			[]job.Approval{withAuth(confirmation("al", job.ConfirmFreshness+time.Second), job.ConfirmAuthSSO)},
			job.GateWaiting,
		},
		{
			"a fresh time with no kind of authentication",
			[]job.Approval{withAuth(confirmation("al", time.Minute), "")},
			job.GateWaiting,
		},
		{
			"an MFA step after the confirmation",
			[]job.Approval{confirmation("al", -time.Minute)},
			job.GateWaiting,
		},
		{"another person", []job.Approval{confirmation("bo", time.Minute)}, job.GateWaiting},
		{
			"the agent for the requester",
			[]job.Approval{viaAgent(confirmation("al", time.Minute))},
			job.GateWaiting,
		},
		{
			"an approval of the policy gate by another person",
			[]job.Approval{withGate(confirmation("bo", time.Minute), job.GateApproval)},
			job.GateWaiting,
		},
		{
			"a rejection by the requester rejects",
			[]job.Approval{rejected(confirmation("al", time.Minute))},
			job.GateRejected,
		},
		{
			"a rejection by the agent rejects",
			[]job.Approval{viaAgent(rejected(confirmation("al", time.Minute)))},
			job.GateRejected,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// AllowSelf has no effect on a confirmation gate.
			for _, allowSelf := range []bool{false, true} {
				g := job.ConfirmationGate(false)
				g.AllowSelf = allowSelf

				if got := g.Status(tt.approvals, "al", devInstallation); got != tt.want {
					t.Fatalf("Status(allowSelf %t) = %s, want %s", allowSelf, got, tt.want)
				}
			}
		})
	}
}

func TestGatesStatus(t *testing.T) {
	t.Parallel()

	policy := job.Gate{
		Type: job.GateApproval, Approvals: 1, Groups: nil, GroupIDs: nil, AllowSelf: false, Timeout: 0, BindPlan: false,
	}
	both := []job.Gate{policy, job.ConfirmationGate(false)}
	approved := withGate(confirmation("bo", 0), job.GateApproval)

	tests := []struct {
		name      string
		gates     []job.Gate
		approvals []job.Approval
		want      job.GateStatus
	}{
		{"no gate", nil, nil, job.GateOpen},
		{"both waiting", both, nil, job.GateWaiting},
		{"only the approval", both, []job.Approval{approved}, job.GateWaiting},
		{"only the confirmation", both, []job.Approval{confirmation("al", time.Minute)}, job.GateWaiting},
		{"both open", both, []job.Approval{approved, confirmation("al", time.Minute)}, job.GateOpen},
		{
			"a rejection of one rejects",
			both,
			[]job.Approval{approved, rejected(confirmation("al", time.Minute))},
			job.GateRejected,
		},
		{
			"the confirmation does not pass the policy gate for the starter",
			[]job.Gate{policy},
			[]job.Approval{confirmation("al", time.Minute)},
			job.GateWaiting,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := job.GatesStatus(tt.gates, tt.approvals, "al", devInstallation); got != tt.want {
				t.Fatalf("GatesStatus() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestPhaseGates(t *testing.T) {
	t.Parallel()

	policy := job.Gate{Type: "", Approvals: 2, Groups: nil, GroupIDs: nil, AllowSelf: false, Timeout: 0, BindPlan: true}
	none := job.Decision{Allow: true, Reason: "", Gates: nil, PolicyVersion: ""}
	gated := job.Decision{
		Allow: true, Reason: "", Gates: map[job.PhaseKind]job.Gate{job.PhaseApply: policy}, PolicyVersion: "v1",
	}
	planApply := []job.PhaseKind{job.PhasePlan, job.PhaseApply}

	tests := []struct {
		name    string
		kinds   []job.PhaseKind
		d       job.Decision
		confirm bool
		want    []string
	}{
		{"no policy, no agent", planApply, none, false, []string{"", ""}},
		{"policy, no agent", planApply, gated, false, []string{"", "approval/bind"}},
		{"no policy, agent in approve-first", planApply, none, true, []string{"", "confirmation/bind"}},
		{"policy and agent", planApply, gated, true, []string{"", "approval/bind confirmation/bind"}},
		{"refresh has no plan to bind", []job.PhaseKind{job.PhaseApply}, none, true, []string{"confirmation"}},
		{"write-back", []job.PhaseKind{job.PhaseDeliver}, none, true, []string{"confirmation"}},
		{"plan only", []job.PhaseKind{job.PhasePlan}, none, true, []string{""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := job.PolicyGates(tt.kinds, tt.d)
			if tt.confirm {
				got = job.WithConfirmations(tt.kinds, got)
			}

			if s := describeGates(got); !slices.Equal(s, tt.want) {
				t.Fatalf("gates = %q, want %q", s, tt.want)
			}
		})
	}
}

// describeGates returns, for each phase, the types of its gates in order,
// with "/bind" for a gate that binds to the plan.
func describeGates(phases [][]job.Gate) []string {
	out := make([]string, 0, len(phases))

	for _, gates := range phases {
		parts := make([]string, 0, len(gates))

		for _, g := range gates {
			p := string(g.Type)
			if g.BindPlan {
				p += "/bind"
			}

			parts = append(parts, p)
		}

		out = append(out, strings.Join(parts, " "))
	}

	return out
}

func TestCheckAgent(t *testing.T) {
	t.Parallel()

	unit := identity.NamespaceObject("acme")
	all := job.DirectSwitches{Server: true, Namespace: true, User: true}
	writeBack := func(d job.Delivery) job.Spec {
		s := newSpec(unit, job.OperationApply, job.ExecutorWriteBack)
		s.Delivery = d

		return s
	}

	tests := []struct {
		name    string
		spec    job.Spec
		agent   string
		mode    job.AgentMode
		sw      job.DirectSwitches
		confirm bool
		err     error
	}{
		{
			"a person needs no confirmation", newSpec(unit, job.OperationApply, job.ExecutorRunner), "", "",
			job.DirectSwitches{Server: false, Namespace: false, User: false},
			false, nil,
		},
		{
			"an agent plan needs none", newSpec(unit, job.OperationPlan, job.ExecutorRunner), "dusty",
			job.AgentModeApproveFirst,
			job.DirectSwitches{Server: false, Namespace: false, User: false},
			false, nil,
		},
		{
			"approve-first confirms an apply", newSpec(unit, job.OperationApply, job.ExecutorRunner), "dusty",
			job.AgentModeApproveFirst, all, true, nil,
		},
		{
			"an empty mode is approve-first", writeBack(job.DeliveryDirectPush), "dusty", "", all, true, nil,
		},
		{
			"review-request mode with a review request", writeBack(job.DeliveryReviewRequest), "dusty",
			job.AgentModeReviewRequest,
			job.DirectSwitches{Server: false, Namespace: false, User: false},
			false, nil,
		},
		{
			"review-request mode with the default delivery", writeBack(""), "dusty", job.AgentModeReviewRequest,
			job.DirectSwitches{Server: false, Namespace: false, User: false},
			false, nil,
		},
		{
			"review-request mode refuses a direct push", writeBack(job.DeliveryDirectPush), "dusty",
			job.AgentModeReviewRequest, all, false, job.ErrAgentDenied,
		},
		{
			"review-request mode refuses a runner apply", newSpec(unit, job.OperationApply, job.ExecutorRunner),
			"dusty", job.AgentModeReviewRequest, all, false, job.ErrAgentDenied,
		},
		{
			"direct mode with all three switches", writeBack(job.DeliveryDirectPush), "dusty", job.AgentModeDirect,
			all, false, nil,
		},
		{
			"direct mode without the server switch", writeBack(job.DeliveryDirectPush), "dusty",
			job.AgentModeDirect,
			job.DirectSwitches{Server: false, Namespace: true, User: true},
			false,
			job.ErrAgentDenied,
		},
		{
			"direct mode without the namespace switch", newSpec(unit, job.OperationDestroy, job.ExecutorRunner),
			"dusty", job.AgentModeDirect,
			job.DirectSwitches{Server: true, Namespace: false, User: true},
			false,
			job.ErrAgentDenied,
		},
		{
			"direct mode without the user switch", writeBack(job.DeliveryReviewRequest), "dusty",
			job.AgentModeDirect,
			job.DirectSwitches{Server: true, Namespace: true, User: false},
			false,
			job.ErrAgentDenied,
		},
		{
			"an unknown mode", writeBack(job.DeliveryReviewRequest), "dusty", "yolo", all, false, job.ErrInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := job.Actor{Principal: identity.Principal{
				Subject: "al", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
				TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
				Kind:       "",
				Credential: "",
				JobID:      "",
			}, Agent: tt.agent, Mode: tt.mode, ConfirmAuth: job.ConfirmAuthMFA, AuthAt: time.Time{}}

			confirm, err := job.CheckAgent(&tt.spec, a, tt.sw)
			if (tt.err == nil && err != nil) || (tt.err != nil && !errors.Is(err, tt.err)) {
				t.Fatalf("CheckAgent() error = %v, want %v", err, tt.err)
			}

			if confirm != tt.confirm {
				t.Fatalf("CheckAgent() confirm = %t, want %t", confirm, tt.confirm)
			}
		})
	}
}

// TestCheckAgentFreshAuth checks the rules of the fresh authentication
// and of the runner operations in each mode.
func TestCheckAgentFreshAuth(t *testing.T) {
	t.Parallel()

	unit := identity.NamespaceObject("acme")
	all := job.DirectSwitches{Server: true, Namespace: true, User: true}

	tests := []struct {
		name    string
		op      job.Operation
		mode    job.AgentMode
		auth    job.ConfirmAuth
		confirm bool
		err     error
	}{
		{
			"approve-first with MFA confirms",
			job.OperationApply,
			job.AgentModeApproveFirst,
			job.ConfirmAuthMFA,
			true,
			nil,
		},
		{
			"approve-first with SSO confirms",
			job.OperationApply,
			job.AgentModeApproveFirst,
			job.ConfirmAuthSSO,
			true,
			nil,
		},
		{
			"approve-first with no fresh authentication is refused", job.OperationApply, job.AgentModeApproveFirst,
			"", false, job.ErrAgentDenied,
		},
		{
			"an empty mode with no fresh authentication is refused",
			job.OperationDestroy,
			"",
			"",
			false,
			job.ErrAgentDenied,
		},
		{
			"a plan with no fresh authentication in approve-first",
			job.OperationPlan,
			job.AgentModeApproveFirst,
			"",
			false,
			nil,
		},
		{"a plan in review-request mode", job.OperationPlan, job.AgentModeReviewRequest, "", false, nil},
		{"a plan in direct mode", job.OperationPlan, job.AgentModeDirect, "", false, nil},
		{
			"review-request mode refuses a runner destroy", job.OperationDestroy, job.AgentModeReviewRequest,
			job.ConfirmAuthMFA, false, job.ErrAgentDenied,
		},
		{
			"review-request mode refuses a runner refresh", job.OperationRefresh, job.AgentModeReviewRequest,
			job.ConfirmAuthMFA, false, job.ErrAgentDenied,
		},
		{"direct mode needs no fresh authentication", job.OperationApply, job.AgentModeDirect, "", false, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := newSpec(unit, tt.op, job.ExecutorRunner)
			a := job.Actor{Principal: identity.Principal{
				Subject: "al", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
				TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
				Kind:       "",
				Credential: "",
				JobID:      "",
			}, Agent: "dusty", Mode: tt.mode, ConfirmAuth: tt.auth, AuthAt: time.Time{}}

			confirm, err := job.CheckAgent(&spec, a, all)
			if (tt.err == nil && err != nil) || (tt.err != nil && !errors.Is(err, tt.err)) {
				t.Fatalf("CheckAgent() error = %v, want %v", err, tt.err)
			}

			if confirm != tt.confirm {
				t.Fatalf("CheckAgent() confirm = %t, want %t", confirm, tt.confirm)
			}
		})
	}
}

// confirmation returns a confirmation by subject at a fixed time, with
// an MFA step authAge before it.
func confirmation(subject string, authAge time.Duration) job.Approval {
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

	return job.Approval{
		Gate: job.GateConfirmation, Subject: subject, Agent: "", Verdict: job.VerdictApprove, Comment: "",
		Plan: nil, At: at, Auth: job.ConfirmAuthMFA, AuthAt: at.Add(-authAge),
	}
}

// noAuth returns a with no fresh authentication.
func noAuth(a job.Approval) job.Approval {
	a.Auth = ""
	a.AuthAt = time.Time{}

	return a
}

// withAuth returns a with the fresh authentication of the kind c.
func withAuth(a job.Approval, c job.ConfirmAuth) job.Approval {
	a.Auth = c

	return a
}

// rejected returns a with the verdict reject.
func rejected(a job.Approval) job.Approval {
	a.Verdict = job.VerdictReject

	return a
}

// withGate returns a for the gate type g.
func withGate(a job.Approval, g job.GateType) job.Approval {
	a.Gate = g

	return a
}

// viaAgent returns a with the agent dusty.
func viaAgent(a job.Approval) job.Approval {
	a.Agent = "dusty"

	return a
}

func TestCheckPlan(t *testing.T) {
	t.Parallel()

	made := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	plan := storedPlan("sv-7", made, "d1", "a1")
	bound := plan.Binding()
	other := bound
	other.ApplySHA256 = "a2"
	redacted := storedPlan("sv-7", made, "d1", "a1")
	redacted.RedactedAt = made.Add(time.Minute)

	tests := []struct {
		name      string
		plan      *job.PlanRef
		bound     *job.PlanBinding
		current   string
		notBefore time.Time
		want      error
	}{
		{"no plan after a plan phase", nil, nil, "sv-9", made, job.ErrPlanStale},
		{"fresh and bound", plan, &bound, "sv-7", made.Add(-time.Hour), nil},
		{"fresh with no gate", plan, nil, "sv-7", made.Add(-time.Hour), nil},
		{"another plan to apply", plan, &other, "sv-7", made.Add(-time.Hour), job.ErrPlanChanged},
		{"state changed", plan, &bound, "sv-8", made.Add(-time.Hour), job.ErrPlanStale},
		{"too old", plan, &bound, "sv-7", made.Add(time.Second), job.ErrPlanStale},
		{"redacted", redacted, nil, "sv-7", made.Add(-time.Hour), job.ErrPlanStale},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := job.CheckPlan(tt.plan, tt.bound, tt.current, tt.notBefore)
			if (tt.want == nil && err != nil) || (tt.want != nil && !errors.Is(err, tt.want)) {
				t.Fatalf("CheckPlan() = %v, want %v", err, tt.want)
			}
		})
	}

	want := job.PlanBinding{Plan: plan.ID, DisplaySHA256: "d1", ApplySHA256: "a1", StateVersion: "sv-7"}
	if bound != want {
		t.Fatalf("Binding() = %+v, want %+v", bound, want)
	}

	var none *job.PlanRef
	if b := none.Binding(); b != (job.PlanBinding{Plan: "", DisplaySHA256: "", ApplySHA256: "", StateVersion: ""}) {
		t.Fatalf("Binding() of no plan = %+v, want the zero binding", b)
	}
}

func TestCheckPlanPartsStoredValues(t *testing.T) {
	t.Parallel()

	const md5Hex = "0123456789abcdef0123456789abcdef"

	hash := strings.Repeat("a", 64)
	part := func(size int64, md5 string) []job.PlanPart {
		return []job.PlanPart{{
			Kind: job.OutputPlanDisplay, Size: 5, SHA256: hash, Secret: false, StoredSize: size, StoredMD5: md5,
		}}
	}

	tests := []struct {
		name  string
		parts []job.PlanPart
		ok    bool
	}{
		{"a part with stored values", part(40, md5Hex), true},
		{"a part from an earlier version, with none", part(0, ""), true},
		{"an MD5 with no size", part(0, md5Hex), false},
		{"a size with no MD5", part(40, ""), false},
		{"a negative stored size", part(-1, md5Hex), false},
		{"an MD5 in upper case", part(40, strings.ToUpper(md5Hex)), false},
		{"a short MD5", part(40, md5Hex[:30]), false},
		{"an MD5 with text that is no hex", part(40, strings.Repeat("g", 32)), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := job.CheckPlanParts(tt.parts)
			if tt.ok != (err == nil) || (err != nil && !errors.Is(err, job.ErrInvalid)) {
				t.Fatalf("CheckPlanParts = %v, want ok %v", err, tt.ok)
			}
		})
	}
}

// TestCheckPlanPartsSecret proves the secret rule of a plan part: only the
// part of kind OutputPlanApply is secret.
func TestCheckPlanPartsSecret(t *testing.T) {
	t.Parallel()

	hash := strings.Repeat("a", 64)

	part := func(kind job.OutputKind, isSecret bool) job.PlanPart {
		return job.PlanPart{Kind: kind, Size: 1, SHA256: hash, Secret: isSecret, StoredSize: 0, StoredMD5: ""}
	}

	tests := []struct {
		name  string
		parts []job.PlanPart
		// bad names the kind of the part that the secret rule refuses, and
		// is empty when the parts pass.
		bad job.OutputKind
	}{
		{"an apply part", []job.PlanPart{part(job.OutputPlanApply, true)}, ""},
		{"a display part", []job.PlanPart{part(job.OutputPlanDisplay, false)}, ""},
		{
			"a display part and an apply part",
			[]job.PlanPart{part(job.OutputPlanDisplay, false), part(job.OutputPlanApply, true)},
			"",
		},
		{"an apply part that is not secret", []job.PlanPart{part(job.OutputPlanApply, false)}, job.OutputPlanApply},
		{"a display part that is secret", []job.PlanPart{part(job.OutputPlanDisplay, true)}, job.OutputPlanDisplay},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := job.CheckPlanParts(tt.parts)
			if tt.bad == "" {
				if err != nil {
					t.Fatalf("CheckPlanParts = %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, job.ErrInvalid) {
				t.Fatalf("CheckPlanParts = %v, want ErrInvalid", err)
			}

			if !strings.Contains(err.Error(), string(tt.bad)) {
				t.Fatalf("CheckPlanParts = %v, want a message that names the part kind %q", err, tt.bad)
			}
		})
	}
}

// storedPlan returns a plan that read the state version sv at made, with
// the hashes display and apply of its two parts.
func storedPlan(sv string, made time.Time, display, apply string) *job.PlanRef {
	return &job.PlanRef{
		ID: "pln-" + display + apply, ItemKey: secret.ItemKeyID("ik-" + display + apply), StateVersion: sv,
		Parts: []job.PlanPart{
			{Kind: job.OutputPlanDisplay, Size: 10, SHA256: display, Secret: false, StoredSize: 0, StoredMD5: ""},
			{Kind: job.OutputPlanApply, Size: 20, SHA256: apply, Secret: true, StoredSize: 0, StoredMD5: ""},
		},
		CreatedAt: made, ExpiresAt: history.ExpiresAt(made, history.DefaultRetention), RedactedAt: time.Time{},
	}
}

func TestClosedBy(t *testing.T) {
	t.Parallel()

	unit := unitWithID(t, "network", "unt-1")
	merged := time.Unix(200, 0)
	push := delivery(job.DeliveryDirectPush, time.Time{})
	open := delivery(job.DeliveryReviewRequest, time.Time{})
	review := delivery(job.DeliveryReviewRequest, merged)
	at := func(sec int64, final bool) job.StateVersion {
		return job.StateVersion{
			Unit: unit, VersionID: "sv-1", Commit: "", Job: "", At: time.Unix(sec, 0), Final: final,
		}
	}

	// delivery() delivers at 50.
	tests := []struct {
		name  string
		state job.State
		d     *job.DeliveryResult
		v     job.StateVersion
		want  bool
	}{
		{"direct push, version after", job.StateAwaitingState, push, at(51, true), true},
		{"direct push, version at the delivery", job.StateAwaitingState, push, at(50, true), true},
		{"direct push, version before", job.StateAwaitingState, push, at(49, true), false},
		{"intermediate version", job.StateAwaitingState, push, at(51, false), false},
		{"review request, no merge", job.StateAwaitingState, open, at(300, true), false},
		{"review request, version after the merge", job.StateAwaitingState, review, at(201, true), true},
		{"review request, version at the merge", job.StateAwaitingState, review, at(200, true), true},
		{"review request, version before the merge", job.StateAwaitingState, review, at(150, true), false},
		{"not awaiting state", job.StateRunning, push, at(51, true), false},
		{"no delivery", job.StateAwaitingState, nil, at(51, true), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			j := jobIn(tt.state, tt.d)
			j.Spec.Unit = unit

			if got := j.ClosedBy(tt.v); got != tt.want {
				t.Fatalf("ClosedBy() = %t, want %t", got, tt.want)
			}
		})
	}

	other := jobIn(job.StateAwaitingState, push)
	other.Spec.Unit = unitWithID(t, "dns", "unt-2")

	if other.ClosedBy(at(51, true)) {
		t.Fatal("ClosedBy() = true for a version of another unit")
	}
}

// unitWithID returns the OpenTofu unit name of the namespace acme with the
// stable unit ID id.
func unitWithID(t *testing.T, name, id string) identity.Object {
	t.Helper()

	o, err := identity.UnitObject("opentofu", "acme", name)
	if err != nil {
		t.Fatalf("UnitObject = %v", err)
	}

	return o.WithUnitID(id)
}

func TestFreeze(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 12, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(14 * 24 * time.Hour)
	valid := job.Freeze{
		ID: "frz-1", Namespace: "acme", Reason: "holiday freeze", StartsAt: start, EndsAt: end,
		CreatedBy: "al", EndedBy: "",
	}

	active := []struct {
		name string
		f    job.Freeze
		at   time.Time
		want bool
	}{
		{"before the start", valid, start.Add(-time.Second), false},
		{"at the start", valid, start, true},
		{"inside", valid, start.Add(time.Hour), true},
		{"at the end", valid, end, false},
		{"open ended", job.Freeze{
			ID: "frz-2", Namespace: "acme", Reason: "incident", StartsAt: start, EndsAt: time.Time{},
			CreatedBy: "al", EndedBy: "",
		}, end.Add(1000 * time.Hour), true},
	}

	for _, tt := range active {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.f.ActiveAt(tt.at); got != tt.want {
				t.Fatalf("ActiveAt() = %t, want %t", got, tt.want)
			}
		})
	}

	invalid := map[string]func(f job.Freeze) job.Freeze{
		"no namespace":     func(f job.Freeze) job.Freeze { f.Namespace = ""; return f },
		"no reason":        func(f job.Freeze) job.Freeze { f.Reason = ""; return f },
		"no start":         func(f job.Freeze) job.Freeze { f.StartsAt = time.Time{}; return f },
		"end at start":     func(f job.Freeze) job.Freeze { f.EndsAt = f.StartsAt; return f },
		"end before start": func(f job.Freeze) job.Freeze { f.EndsAt = f.StartsAt.Add(-time.Hour); return f },
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}

	for name, change := range invalid {
		if err := change(valid).Validate(); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("%s: Validate() = %v, want ErrInvalid", name, err)
		}
	}
}

func TestAgentModeValid(t *testing.T) {
	t.Parallel()

	for mode, want := range map[job.AgentMode]bool{
		job.AgentModeApproveFirst: true, job.AgentModeReviewRequest: true, job.AgentModeDirect: true,
		"": false, "approve": false, "Direct": false,
	} {
		if got := mode.Valid(); got != want {
			t.Errorf("AgentMode(%q).Valid() = %t, want %t", mode, got, want)
		}
	}
}
