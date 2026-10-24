package drift_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	driftapi "opendustpan.org/opendustpan/internal/api/drift"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errUnknown is an error that no case of the adapter knows.
var errUnknown = errors.New("driver: connection reset")

// TestEveryErrorAnswerHasACode fails for an error of the drift API that
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
		{"unknown namespace", namespace.ErrNotFound, http.StatusNotFound, errcode.UnitNotFound},
		{"unknown job", job.ErrNotFound, http.StatusNotFound, errcode.UnitNotFound},
		{"not home", namespace.ErrNotHome, http.StatusMisdirectedRequest, errcode.NamespaceNotHome},
		{"maintenance", maintenance.ErrInMaintenance, http.StatusServiceUnavailable, errcode.MaintenanceInMaintenance},
		{"fenced", namespace.ErrFenced, http.StatusConflict, errcode.NamespaceFenced},
		{"start conflict", job.ErrKeyConflict, http.StatusConflict, errcode.DriftStartConflict},
		{"invalid job", job.ErrInvalid, http.StatusBadRequest, errcode.JobInvalid},
		{"invalid check", drift.ErrInvalid, http.StatusBadRequest, errcode.JobInvalid},
		{"unsupported", job.ErrUnsupported, http.StatusUnprocessableEntity, errcode.JobUnsupported},
		{"policy", job.ErrPolicyDenied, http.StatusForbidden, errcode.JobPolicyDenied},
		{"gate", job.ErrGateEnforced, http.StatusForbidden, errcode.JobGateEnforced},
		{"agent", job.ErrAgentDenied, http.StatusForbidden, errcode.JobAgentDenied},
		{"frozen", job.ErrFrozen, http.StatusConflict, errcode.JobFrozen},
		{"limit", job.ErrLimit, http.StatusTooManyRequests, errcode.JobLimit},
		{
			"policy and denied", fmt.Errorf("%w: %w", job.ErrPolicyDenied, identity.ErrDenied), http.StatusForbidden,
			errcode.JobPolicyDenied,
		},
		{"unknown error", errUnknown, http.StatusInternalServerError, errcode.ServerInternal},
	}

	for _, tc := range tests {
		for _, err := range []error{tc.err, fmt.Errorf("start the check: %w", tc.err)} {
			t.Run(tc.name+"/"+err.Error(), func(t *testing.T) {
				t.Parallel()

				rec := httptest.NewRecorder()
				driftapi.WriteError(t.Context(), rec, err)

				errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
					errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
			})
		}
	}
}
