package dustpan

import (
	"context"
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
		{name: "valid request, administrator", actor: driftActor(), id: "job-unknown", req: validRedaction()},
		{name: "valid request, no grant", actor: stranger, id: "job-unknown", req: validRedaction()},
	}

	// The store gets no write from a refused redaction: no job exists in it. The
	// cleanup runs after all parallel cases.
	t.Cleanup(func() {
		var f job.Filter

		f.Namespace = "acme"
		f.Limit = 10

		page, err := st.Jobs().List(context.Background(), f)
		if err != nil {
			t.Errorf("List = %v", err)

			return
		}

		if len(page.Jobs) != 0 || len(page.Damaged) != 0 {
			t.Errorf("store holds %d jobs and %d damaged rows, want none", len(page.Jobs), len(page.Damaged))
		}
	})

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

// validRedaction returns a request that passes the boundary check: a log
// version, one substitution and a reason.
func validRedaction() job.RedactRequest {
	return job.RedactRequest{
		Version: "jlv-1", Substitutions: []job.Substitution{{Match: []byte("hunter2")}}, Reason: "leak",
	}
}

// redactOf returns a redaction request for the log version v.
func redactOf(v string) job.RedactRequest {
	return job.RedactRequest{Version: v, Substitutions: nil, Reason: "leak"}
}
