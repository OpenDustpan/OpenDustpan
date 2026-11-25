package job_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// freshRig returns a rig whose service holds the guard of policy (nil: no
// policy) and whose authorizer lets every subject read and approve on the unit.
func freshRig(t *testing.T, policy freshstep.Policy) *rig {
	t.Helper()

	guard, err := job.NewFreshGuard(policy)
	if err != nil {
		t.Fatalf("NewFreshGuard = %v, want nil", err)
	}

	return guardRig(t, guard)
}

// guardRig returns a rig whose service holds guard and whose authorizer lets
// every subject read and approve on the unit.
func guardRig(t *testing.T, guard freshstep.Guard) *rig {
	t.Helper()

	return newRig(t, func(cfg *job.ServiceConfig, r *rig) {
		cfg.Approvers = openPolicy()
		cfg.FreshGuard = guard

		for _, action := range []identity.Action{job.ActionJobRead, job.ActionJobApprove} {
			r.authz.allow[string(action)+" "+r.unit.String()] = true
		}
	})
}

// authKey is the key of the authentication in the changes of an audit entry.
const authKey = "auth"

// recordedAuth returns the value of the auth change of the one audit entry of
// the one Apply call, and whether the entry holds it.
func recordedAuth(t *testing.T, r *rig) (string, bool) {
	t.Helper()

	if len(r.store.applyEntries) != 1 || len(r.store.applyEntries[0]) != 1 {
		t.Fatalf("audit entries = %+v, want one entry of one change", r.store.applyEntries)
	}

	for _, c := range r.store.applyEntries[0][0].Changes {
		if c.Key == authKey {
			return c.New, true
		}
	}

	return "", false
}

// TestDestroyApprovalFollowsTheFreshPolicy is the table of the service layer.
// The off rows pass with no step and record auth none. The on rows refuse with
// no step, and a policy that is nil or a zero Guard counts as on. Without the
// guard check in checkDestroyAuth, the off rows fail (the approval is refused)
// or the on rows fail (the approval passes).
func TestDestroyApprovalFollowsTheFreshPolicy(t *testing.T) {
	t.Parallel()

	off := freshstep.PolicyFunc(func(context.Context) bool { return false })
	on := freshstep.PolicyFunc(func(context.Context) bool { return true })

	// tokenActor is a person with an access token and no step.
	tokenActor := func() job.Actor {
		a := actorWithToken("carol", "tok-1")
		a.Principal.Credential = identity.CredentialAccessToken

		return a
	}

	// sessionActor is a person with a session and a fresh MFA step.
	sessionActor := func(r *rig) job.Actor {
		a := actorOf("carol")
		a.Principal.Credential = identity.CredentialSession
		a.ConfirmAuth, a.AuthAt = job.ConfirmAuthMFA, r.now.Add(-time.Minute)

		return a
	}

	// agent is an agent that acts for a person.
	agent := func(*rig) job.Actor { return agentActor("carol", "dusty") }

	tests := []struct {
		name     string
		policy   freshstep.Policy
		zero     bool // the service holds the zero Guard, not the guard of policy
		actor    func(r *rig) job.Actor
		wantErr  error
		wantAuth string
	}{
		{"off, token, no step", off, false, func(*rig) job.Actor { return tokenActor() }, nil, "none"},
		{"off, no credential, no step", off, false, func(*rig) job.Actor { return actorOf("carol") }, nil, "none"},
		{"off, session with a step", off, false, sessionActor, nil, "none"},
		{"off, agent", off, false, agent, job.ErrAuthStale, ""},
		{"on, token, no step", on, false, func(*rig) job.Actor { return tokenActor() }, job.ErrAuthStale, ""},
		{"on, no step", on, false, func(*rig) job.Actor { return actorOf("carol") }, job.ErrAuthStale, ""},
		{"on, agent", on, false, agent, job.ErrAuthStale, ""},
		{"on, session with a step", on, false, sessionActor, nil, "mfa"},
		{"nil policy, token, no step", nil, false, func(*rig) job.Actor { return tokenActor() }, job.ErrAuthStale, ""},
		{"nil policy, session with a step", nil, false, sessionActor, nil, "mfa"},
		{"zero guard, token, no step", nil, true, func(*rig) job.Actor { return tokenActor() }, job.ErrAuthStale, ""},
		{"zero guard, no step", nil, true, func(*rig) job.Actor { return actorOf("carol") }, job.ErrAuthStale, ""},
		{"zero guard, session with a step", nil, true, sessionActor, nil, "mfa"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var r *rig
			if tc.zero {
				r = guardRig(t, freshstep.Guard{})
			} else {
				r = freshRig(t, tc.policy)
			}
			r.seed(func(j *job.Job) { j.Spec.Operation = job.OperationDestroy })

			_, err := r.svc.Approve(t.Context(), tc.actor(r), approveID, job.VerdictApprove, r.binding(), "")

			checkFreshOutcome(t, r, err, tc.wantErr, tc.wantAuth)
		})
	}
}

// checkFreshOutcome checks the answer of Approve: the refusal wantErr and no
// change, or no error, one change and the authentication wantAuth in the
// approval and in the audit entry.
func checkFreshOutcome(t *testing.T, r *rig, err, wantErr error, wantAuth string) {
	t.Helper()

	if wantErr != nil {
		if !errors.Is(err, wantErr) {
			t.Fatalf("Approve = %v, want %v", err, wantErr)
		}

		if len(r.store.applies) != 0 {
			t.Errorf("a refused approval wrote %d changes", len(r.store.applies))
		}

		return
	}

	if err != nil {
		t.Fatalf("Approve = %v, want nil", err)
	}

	if len(r.store.applies) != 1 {
		t.Fatalf("changes = %d, want 1", len(r.store.applies))
	}

	if got := string(r.store.applies[0].Approval.Auth); got != wantAuth {
		t.Errorf("approval auth = %q, want %q", got, wantAuth)
	}

	if got, ok := recordedAuth(t, r); !ok || got != wantAuth {
		t.Errorf("audit auth = %q (present %v), want %q", got, ok, wantAuth)
	}
}

// TestFreshPolicyDoesNotChangeOtherApprovals proves that the policy touches
// the approval of a destroy only: an approval of an apply and a rejection
// record no authentication, with the setting off too.
func TestFreshPolicyDoesNotChangeOtherApprovals(t *testing.T) {
	t.Parallel()

	off := freshstep.PolicyFunc(func(context.Context) bool { return false })

	tests := []struct {
		name    string
		op      job.Operation
		verdict job.Verdict
	}{
		{"apply approval", job.OperationApply, job.VerdictApprove},
		{"destroy rejection", job.OperationDestroy, job.VerdictReject},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := freshRig(t, off)
			r.seed(func(j *job.Job) { j.Spec.Operation = tc.op })

			// A rejection of a job that another principal started needs
			// job:cancel on the unit.
			if tc.verdict == job.VerdictReject {
				r.authz.allow[string(job.ActionJobCancel)+" "+r.unit.String()] = true
			}

			_, err := r.svc.Approve(t.Context(), actorOf("carol"), approveID, tc.verdict, r.binding(), "")
			if err != nil {
				t.Fatalf("Approve = %v, want nil", err)
			}

			if got, ok := recordedAuth(t, r); ok {
				t.Errorf("audit auth = %q, want none recorded", got)
			}
		})
	}
}

// TestNewFreshGuard proves that the guard refuses with ErrAuthStale when the
// policy requires a step, also for a nil policy, and passes when it does not.
func TestNewFreshGuard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		policy  freshstep.Policy
		wantErr error
	}{
		{"off", freshstep.PolicyFunc(func(context.Context) bool { return false }), nil},
		{"on", freshstep.PolicyFunc(func(context.Context) bool { return true }), job.ErrAuthStale},
		{"nil policy", nil, job.ErrAuthStale},
		{"nil policy func", freshstep.PolicyFunc(nil), job.ErrAuthStale},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g, err := job.NewFreshGuard(tc.policy)
			if err != nil {
				t.Fatalf("NewFreshGuard = %v, want nil", err)
			}

			err = g.Require(t.Context(), "carol")
			if tc.wantErr == nil && err != nil {
				t.Fatalf("Require = %v, want nil", err)
			}

			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("Require = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestConfirmAuthNoneIsNotAFreshStep proves that "none" is not a fresh
// authentication, so a confirmation gate never accepts it.
func TestConfirmAuthNoneIsNotAFreshStep(t *testing.T) {
	t.Parallel()

	if job.ConfirmAuthNone.Valid() {
		t.Fatal("ConfirmAuthNone.Valid() = true, want false")
	}

	if got := string(job.ConfirmAuthNone); got != "none" {
		t.Fatalf("ConfirmAuthNone = %q, want none", got)
	}
}

// TestConfirmationGateFollowsSetting proves that the confirmation gate counts
// an approval with auth none only while freshRequired is false. Each row runs
// Gate.Status, GatesStatus and GatesProgress, which must agree. Without the
// freshRequired check in freshAuth, the off row of none fails (the gate waits)
// and the on rows of none fail (the gate opens).
func TestConfirmationGateFollowsSetting(t *testing.T) {
	t.Parallel()

	g := job.ConfirmationGate(false)
	none := []job.Approval{withAuth(confirmation("al", 0), job.ConfirmAuthNone)}

	tests := []struct {
		name          string
		approvals     []job.Approval
		freshRequired bool
		want          job.GateStatus
	}{
		{"none with the setting off counts", none, false, job.GateOpen},
		{"none with the setting on does not count", none, true, job.GateWaiting},
		{
			"none recorded with the setting off and evaluated with it on does not count",
			none,
			true,
			job.GateWaiting,
		},
		{
			"a fresh MFA step counts with the setting off",
			[]job.Approval{confirmation("al", time.Minute)},
			false,
			job.GateOpen,
		},
		{
			"a fresh MFA step counts with the setting on",
			[]job.Approval{confirmation("al", time.Minute)},
			true,
			job.GateOpen,
		},
		{
			"an old MFA step counts with the setting off",
			[]job.Approval{confirmation("al", job.ConfirmFreshness+time.Second)},
			false,
			job.GateWaiting,
		},
		{
			"an old MFA step counts with the setting on",
			[]job.Approval{confirmation("al", job.ConfirmFreshness+time.Second)},
			true,
			job.GateWaiting,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := g.Status(tc.approvals, "al", *openPolicy(), tc.freshRequired); got != tc.want {
				t.Fatalf("Status() = %s, want %s", got, tc.want)
			}

			gates := []job.Gate{g}
			if got := job.GatesStatus(gates, tc.approvals, "al", *openPolicy(), tc.freshRequired); got != tc.want {
				t.Fatalf("GatesStatus() = %s, want %s", got, tc.want)
			}

			got, _ := job.GatesProgress(gates, tc.approvals, "al", *openPolicy(), tc.freshRequired)
			if got != tc.want {
				t.Fatalf("GatesProgress() = %s, want %s", got, tc.want)
			}
		})
	}
}
