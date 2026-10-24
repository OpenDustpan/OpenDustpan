package jobs_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errUnknown is an error that no case of the adapter knows.
var errUnknown = errors.New("driver: connection reset")

// TestEveryErrorAnswerHasACode fails for an error of the jobs API that
// answers without a code of the catalog, with the wrong status, or with an
// argument that the code does not declare.
func TestEveryErrorAnswerHasACode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"rate limited", identity.ErrRateLimited, http.StatusTooManyRequests, errcode.AuthRateLimited},
		{"unauthenticated", identity.ErrUnauthenticated, http.StatusUnauthorized, errcode.AuthUnauthenticated},
		{"csrf", sessionauth.ErrCSRF, http.StatusForbidden, errcode.SessionCSRF},
		{"other site", sessionauth.ErrOtherSite, http.StatusForbidden, errcode.SessionOtherSite},
		{"denied", identity.ErrDenied, http.StatusForbidden, errcode.AccessDenied},
		{"unknown namespace", namespace.ErrNotFound, http.StatusNotFound, errcode.JobNotFound},
		{"unknown job", job.ErrNotFound, http.StatusNotFound, errcode.JobNotFound},
		{"not home", namespace.ErrNotHome, http.StatusMisdirectedRequest, errcode.NamespaceNotHome},
		{"maintenance", maintenance.ErrInMaintenance, http.StatusServiceUnavailable, errcode.MaintenanceInMaintenance},
		{"bad key", idempotency.ErrInvalidKey, http.StatusBadRequest, errcode.RequestIdempotencyKeyInvalid},
		{"key conflict", job.ErrKeyConflict, http.StatusUnprocessableEntity, errcode.RequestIdempotencyKeyReused},
		{"fenced", namespace.ErrFenced, http.StatusConflict, errcode.NamespaceFenced},
		{"invalid", job.ErrInvalid, http.StatusBadRequest, errcode.JobInvalid},
		{"unsupported", job.ErrUnsupported, http.StatusUnprocessableEntity, errcode.JobUnsupported},
		{"policy", job.ErrPolicyDenied, http.StatusForbidden, errcode.JobPolicyDenied},
		{"gate", job.ErrGateEnforced, http.StatusForbidden, errcode.JobGateEnforced},
		{"agent", job.ErrAgentDenied, http.StatusForbidden, errcode.JobAgentDenied},
		{"frozen", job.ErrFrozen, http.StatusConflict, errcode.JobFrozen},
		{"limit", job.ErrLimit, http.StatusTooManyRequests, errcode.JobLimit},
		{"unknown error", errUnknown, http.StatusInternalServerError, errcode.ServerInternal},
	}

	for _, tc := range tests {
		for _, err := range []error{tc.err, fmt.Errorf("read the job: %w", tc.err)} {
			t.Run(tc.name+"/"+err.Error(), func(t *testing.T) {
				t.Parallel()

				rec := httptest.NewRecorder()
				jobsapi.WriteError(t.Context(), rec, err)

				errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
					errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
			})
		}
	}
}
