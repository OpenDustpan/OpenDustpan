package job_test

import (
	"context"
	"testing"

	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// TestDestroyApprovalWithSettingOffAllowsOnlyPersons is the service table for
// the setting off. Only a person gets the authentication none. Each other kind,
// and each agent, gets job.ErrAuthStale, whatever credential it holds. The
// refusal comes before any change.
//
// Defense in depth: the handler and the store check the same kind. This table
// sends each kind to the service alone.
func TestDestroyApprovalWithSettingOffAllowsOnlyPersons(t *testing.T) {
	t.Parallel()

	off := freshstep.PolicyFunc(func(context.Context) bool { return false })

	// kindActor is a principal of kind with credential and token ID.
	kindActor := func(kind identity.Kind, credential identity.Credential, tokenID string) func(*rig) job.Actor {
		return func(*rig) job.Actor {
			a := actorOf("carol")
			a.Principal.Kind = kind
			a.Principal.Credential = credential
			a.Principal.TokenID = tokenID

			return a
		}
	}
	stale := job.ErrAuthStale

	tests := []struct {
		name    string
		actor   func(r *rig) job.Actor
		wantErr error
	}{
		{"person, no credential", kindActor(identity.KindPerson, "", ""), nil},
		{"person, access token", kindActor(identity.KindPerson, identity.CredentialAccessToken, "tok-1"), nil},
		{"service, server credential", kindActor(identity.KindService, identity.CredentialServer, ""), stale},
		{"service, access token", kindActor(identity.KindService, identity.CredentialAccessToken, "tok-2"), stale},
		{"workload, trust token", kindActor(identity.KindWorkload, identity.CredentialTrustToken, "tok-3"), stale},
		{"workload, job token", kindActor(identity.KindWorkload, identity.CredentialJobToken, "tok-4"), stale},
		{"agent kind, no credential", kindActor(identity.KindAgent, "", ""), stale},
		{"person acting as an agent", func(*rig) job.Actor { return agentActor("carol", "dusty") }, stale},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := freshRig(t, off)
			r.seed(func(j *job.Job) { j.Spec.Operation = job.OperationDestroy })

			_, err := r.svc.Approve(t.Context(), tc.actor(r), approveID, job.VerdictApprove, r.binding(), "")

			wantAuth := ""
			if tc.wantErr == nil {
				wantAuth = "none"
			}

			checkFreshOutcome(t, r, err, tc.wantErr, wantAuth)
		})
	}
}
