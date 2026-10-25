package jobs_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestDenialWinsOverTheRefusals proves that a denial wins over every refusal
// of a job, in either order of the wrap. The answer is the same 403 for a
// missing object and for an object with no right: it names no state.
func TestDenialWinsOverTheRefusals(t *testing.T) {
	t.Parallel()

	sentinels := []error{
		jobsapi.ErrNotFound, job.ErrNotFound, namespace.ErrNotFound, jobsapi.ErrBody,
		idempotency.ErrInvalidKey, job.ErrKeyConflict, namespace.ErrFenced, job.ErrInvalid,
		job.ErrUnsupported, job.ErrPolicyDenied, job.ErrGateEnforced, job.ErrAgentDenied,
		job.ErrFrozen, job.ErrLimit,
	}

	for _, sentinel := range sentinels {
		for _, err := range []error{
			fmt.Errorf("%w: %w", identity.ErrDenied, sentinel),
			fmt.Errorf("%w: %w", sentinel, identity.ErrDenied),
		} {
			t.Run(sentinel.Error(), func(t *testing.T) {
				t.Parallel()

				rec := httptest.NewRecorder()
				jobsapi.WriteError(t.Context(), rec, err)

				checkDenied(t, rec, sentinel)
			})
		}
	}
}

// TestCredentialRefusalsKeepTheirAnswer proves that a refusal of the credential
// of a request keeps its answer when it also wraps identity.ErrDenied.
func TestCredentialRefusalsKeepTheirAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"rate limit", identity.ErrRateLimited, http.StatusTooManyRequests, errcode.AuthRateLimited},
		{"no sign-in", identity.ErrUnauthenticated, http.StatusUnauthorized, errcode.AuthUnauthenticated},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			jobsapi.WriteError(t.Context(), rec, fmt.Errorf("%w: %w", identity.ErrDenied, tc.err))

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
		})
	}
}

// checkDenied fails the test unless rec is the answer of a denial: 403, the
// denial code and the message "forbidden". The body holds no text of cause.
func checkDenied(t *testing.T, rec *httptest.ResponseRecorder, cause error) {
	t.Helper()

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusForbidden, rec.Body.Bytes(),
		errcodetest.Want{Code: errcode.Forbidden(identity.ErrDenied), Args: nil, RequestBody: nil})

	body := rec.Body.String()
	if !strings.Contains(body, `"message":"forbidden"`) {
		t.Errorf("message is not forbidden: %s", body)
	}

	if strings.Contains(body, cause.Error()) {
		t.Errorf("body holds the text of the refusal %q: %s", cause, body)
	}
}
