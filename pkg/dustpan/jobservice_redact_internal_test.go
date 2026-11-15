package dustpan

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// TestJobServiceRedactFailsClosedWithoutArtifactStore checks the service as the
// server wires it. No adapter of job.ArtifactStore exists, so each redaction
// answers job.ErrUnsupported and the store gets no write. Each case is hostile
// in one way: the service must refuse it before it reads a job or an
// authorization, as if every other layer had failed.
func TestJobServiceRedactFailsClosedWithoutArtifactStore(t *testing.T) {
	t.Parallel()

	st := openCollectionStore(t)
	created := time.Unix(0, 0).UTC()
	createCollectionNamespace(t, st, created)

	starter := newCheckerStarter(t, checkerBackend{store: st, jobs: st.Jobs(), runners: st.Runners()},
		func() time.Time { return created })

	stranger := job.Actor{
		Principal: identity.Principal{
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "stranger", DisplayName: "",
			Admin: false, TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "",
			JobID: "",
		},
		Agent: "", Mode: "", ConfirmAuth: "", AuthAt: time.Time{},
	}

	tests := []struct {
		name  string
		actor job.Actor
		id    job.ID
		req   job.RedactRequest
	}{
		{name: "administrator, unknown job", actor: driftActor(), id: "job-unknown", req: redactOf("v1")},
		{name: "caller with no grant", actor: stranger, id: "job-unknown", req: redactOf("v1")},
		{name: "empty request", actor: driftActor(), id: "", req: redactOf("")},
		{name: "path-like version", actor: driftActor(), id: "job-unknown", req: redactOf("../../v1")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := starter.RedactLog(t.Context(), tt.actor, tt.id, tt.req)
			if !errors.Is(err, job.ErrUnsupported) {
				t.Fatalf("RedactLog = %v; want %v", err, job.ErrUnsupported)
			}
		})
	}
}

// redactOf returns a redaction request for the log version v.
func redactOf(v string) job.RedactRequest {
	return job.RedactRequest{Version: v, Substitutions: nil, Reason: "leak"}
}
