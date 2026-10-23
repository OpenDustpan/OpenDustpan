package runnerapi_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// errUnknownManagement is an error that no case of the management API knows.
var errUnknownManagement = errors.New("driver: connection reset")

// TestEveryManagementAnswerHasACode fails for an error of the runner
// management API that answers without a code of the catalog, with the wrong
// status, or with an argument that the code does not declare.
func TestEveryManagementAnswerHasACode(t *testing.T) {
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
		{"unknown pool", runner.ErrNotFound, http.StatusNotFound, errcode.RunnerNotFound},
		{"unknown namespace", namespace.ErrNotFound, http.StatusNotFound, errcode.RunnerNotFound},
		{"bad body", runnerapi.ErrBadBody, http.StatusBadRequest, errcode.RequestBodyInvalid},
		{"invalid", runner.ErrInvalid, http.StatusBadRequest, errcode.RunnerInvalid},
		{"pool exists", runner.ErrAlreadyExists, http.StatusConflict, errcode.RunnerPoolExists},
		{"immutable", runner.ErrImmutable, http.StatusConflict, errcode.RunnerImmutable},
		{"pool in use", runner.ErrPoolInUse, http.StatusConflict, errcode.RunnerPoolInUse},
		{"token used", runner.ErrTokenUsed, http.StatusConflict, errcode.RunnerTokenUsed},
		{"limit", runner.ErrLimit, http.StatusConflict, errcode.RunnerLimit},
		{"retired", runner.ErrRetired, http.StatusConflict, errcode.RunnerRetired},
		{"fenced", namespace.ErrFenced, http.StatusConflict, errcode.NamespaceFenced},
		{"not home", namespace.ErrNotHome, http.StatusMisdirectedRequest, errcode.NamespaceNotHome},
		{"maintenance", maintenance.ErrInMaintenance, http.StatusServiceUnavailable, errcode.MaintenanceInMaintenance},
		{"unknown error", errUnknownManagement, http.StatusInternalServerError, errcode.ServerInternal},
	}

	for _, tc := range tests {
		for _, err := range []error{tc.err, fmt.Errorf("create the pool: %w", tc.err)} {
			t.Run(tc.name+"/"+err.Error(), func(t *testing.T) {
				t.Parallel()

				rec := httptest.NewRecorder()
				runnerapi.WriteManagementError(t.Context(), rec, err)

				errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
					errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
			})
		}
	}
}
