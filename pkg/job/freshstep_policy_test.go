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

// freshRig returns a rig whose service holds policy (nil: no policy) and whose
// authorizer lets every subject read and approve on the unit.
func freshRig(t *testing.T, policy freshstep.Policy) *rig {
	t.Helper()

	return newRig(t, func(cfg *job.ServiceConfig, r *rig) {
		cfg.Approvers = openPolicy()
		cfg.FreshPolicy = policy

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
// no step, and a policy that is nil counts as on. Without the policy check in
// checkApproval, the off rows fail (the approval is refused) or the on rows
// fail (the approval passes).
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

	tests := []struct {
		name     string
		policy   freshstep.Policy
		actor    func(r *rig) job.Actor
		wantErr  error
		wantAuth string
	}{
		{"off, token, no step", off, func(*rig) job.Actor { return tokenActor() }, nil, "none"},
		{"off, no credential, no step", off, func(*rig) job.Actor { return actorOf("carol") }, nil, "none"},
		{"off, session with a step", off, sessionActor, nil, "none"},
		{"off, agent", off, func(*rig) job.Actor { return agentActor("carol", "dusty") }, job.ErrAgentDenied, ""},
		{"on, token, no step", on, func(*rig) job.Actor { return tokenActor() }, job.ErrAuthStale, ""},
		{"on, no step", on, func(*rig) job.Actor { return actorOf("carol") }, job.ErrAuthStale, ""},
		{"on, agent", on, func(*rig) job.Actor { return agentActor("carol", "dusty") }, job.ErrAuthStale, ""},
		{"on, session with a step", on, sessionActor, nil, "mfa"},
		{"nil policy, token, no step", nil, func(*rig) job.Actor { return tokenActor() }, job.ErrAuthStale, ""},
		{"nil policy, session with a step", nil, sessionActor, nil, "mfa"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := freshRig(t, tc.policy)
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
