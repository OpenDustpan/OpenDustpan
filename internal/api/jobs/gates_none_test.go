package jobs_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// newPolicyHandler is newHandler with the fresh-step policy of the handler.
func newPolicyHandler(t *testing.T, jobs job.Store, policy freshstep.Policy) testHandler {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: fakeNamespaces{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := jobsapi.NewHandler(jobsapi.Config{
		Namespaces: namespaces, Jobs: jobs, Units: nil, Artifacts: nil, Starter: nil,
		Workers: nil, Pools: nil, Authenticator: tokenAuthenticator{}, Authorizer: newAuthorizer(holdsBoth()...),
		Audit: newTestAudit(), OnAuditFailure: nil, DenialLimiter: nil, Sessions: nil,
		Logger: slog.New(slog.DiscardHandler), Logs: nil, Cursors: nil,
		Approvers:   gate.ApproverPolicy{Production: false, IsDev: nil},
		FreshPolicy: policy,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return testHandler{Handler: handler, served: apidesctest.CheckStatus(t, handler)}
}

// TestGetJobShowsNoneConfirmationAsOpenOnlyWhileTheSettingIsOff proves that the
// answer of a job shows a confirmation gate open for a confirmation with the
// authentication none only while the setting is off. A handler with no policy
// requires the step (fail closed), the same as the setting on. A confirmation
// with a fresh MFA step fills the gate in every case. Without the reads of the
// setting in the answer (a constant false), the none rows with the setting on
// show one filled slot.
func TestGetJobShowsNoneConfirmationAsOpenOnlyWhileTheSettingIsOff(t *testing.T) {
	t.Parallel()

	at := time.Unix(1, 0).UTC()
	off := freshstep.PolicyFunc(func(context.Context) bool { return false })
	on := freshstep.PolicyFunc(func(context.Context) bool { return true })

	tests := []struct {
		name       string
		policy     freshstep.Policy
		auth       job.ConfirmAuth
		authAt     time.Time
		wantFilled int
	}{
		{"none fills the gate while the setting is off", off, job.ConfirmAuthNone, time.Time{}, 1},
		{"none does not fill the gate while the setting is on", on, job.ConfirmAuthNone, time.Time{}, 0},
		{"none does not fill the gate with no policy", nil, job.ConfirmAuthNone, time.Time{}, 0},
		{"fresh MFA fills the gate while the setting is off", off, job.ConfirmAuthMFA, at.Add(-time.Minute), 1},
		{"fresh MFA fills the gate while the setting is on", on, job.ConfirmAuthMFA, at.Add(-time.Minute), 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stored := runningJob(t, testJob)
			stored.Phases[0].Gates = []job.Gate{job.ConfirmationGate(false)}
			stored.Phases[0].Approvals = []job.Approval{{
				Gate: job.GateConfirmation, For: []string{""}, Subject: "ada", Agent: "", Verdict: job.VerdictApprove,
				Comment: "", Plan: nil, At: at, Auth: tc.auth, AuthAt: tc.authAt,
			}}

			handler := newPolicyHandler(t, newFakeJobs(stored), tc.policy)
			rec := serve(t, handler, request{method: http.MethodGet, path: jobPath("acme", testJob), token: testToken})

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d %s, want 200", rec.Code, rec.Body.String())
			}

			var doc gatesDoc
			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
				t.Fatalf("decode: %v", err)
			}

			gates := doc.Phases[0].Gates
			if len(gates) != 1 || gates[0].Filled != tc.wantFilled {
				t.Fatalf("gates = %+v, want one gate with %d filled", gates, tc.wantFilled)
			}
		})
	}
}
