package jobs_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/job"
)

// TestGetJobShowsNoneConfirmationAsOpenOnlyWhileTheSettingIsOff proves that the
// answer of a job does not show a confirmation gate open for a confirmation with
// the authentication none while the setting is on. The handler holds no policy,
// so its guard requires the step (fail closed), which is the setting on. A
// confirmation with a fresh MFA step fills the gate in every case. Without the
// reads of the setting in the answer (a constant false), the none row shows one
// filled slot.
func TestGetJobShowsNoneConfirmationAsOpenOnlyWhileTheSettingIsOff(t *testing.T) {
	t.Parallel()

	at := time.Unix(1, 0).UTC()

	tests := []struct {
		name       string
		auth       job.ConfirmAuth
		authAt     time.Time
		wantFilled int
	}{
		{"none does not fill the gate while the setting is on", job.ConfirmAuthNone, time.Time{}, 0},
		{"fresh MFA fills the gate", job.ConfirmAuthMFA, at.Add(-time.Minute), 1},
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

			handler := newHandler(t, newAuthorizer(holdsBoth()...), newFakeJobs(stored))
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
