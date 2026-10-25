package drift_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/drift"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	coredrift "opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestDenialWinsOverTheRefusals proves that a denial wins over every refusal
// of a drift check, in either order of the wrap. The answer is the same
// 403 for a missing object and for an object with no right: it names no state.
func TestDenialWinsOverTheRefusals(t *testing.T) {
	t.Parallel()

	sentinels := []error{
		drift.ErrNotFound, namespace.ErrNotFound, job.ErrNotFound, drift.ErrBody,
		namespace.ErrFenced, job.ErrKeyConflict, job.ErrInvalid, coredrift.ErrInvalid,
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
				drift.WriteError(t.Context(), rec, err)

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
			drift.WriteError(t.Context(), rec, fmt.Errorf("%w: %w", identity.ErrDenied, tc.err))

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
		})
	}
}

// TestDenialIsThePolicyReason proves that the audit entry of a refused start
// that wraps identity.ErrDenied and a specific refusal names the policy, as the
// answer does. The entry never names the object's state either.
func TestDenialIsThePolicyReason(t *testing.T) {
	t.Parallel()

	causes := []error{
		job.ErrPolicyDenied, job.ErrGateEnforced, job.ErrAgentDenied, job.ErrFrozen, job.ErrLimit,
		job.ErrUnsupported, &job.GateError{Gates: nil},
	}

	for _, cause := range causes {
		for _, err := range []error{
			fmt.Errorf("%w: %w", identity.ErrDenied, cause),
			fmt.Errorf("%w: %w", cause, identity.ErrDenied),
		} {
			t.Run(cause.Error(), func(t *testing.T) {
				t.Parallel()

				reason, ok := drift.AuditReason(err)
				if !ok || reason != refusal.ReasonPolicy {
					t.Errorf("reason = %q, %t; want %q, true", reason, ok, refusal.ReasonPolicy)
				}
			})
		}
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
