package runnerapi_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// TestDenialWinsOverTheRefusals proves that a denial wins over every refusal
// of the management API, in either order of the wrap. The answer is the same
// 403 for a missing object and for an object with no right: it names no state.
func TestDenialWinsOverTheRefusals(t *testing.T) {
	t.Parallel()

	sentinels := []error{
		runnerapi.ErrNotFound, runner.ErrNotFound, namespace.ErrNotFound,
		runner.ErrInvalid, runnerapi.ErrBadBody,
		runner.ErrAlreadyExists, runner.ErrImmutable, runner.ErrPoolInUse, runner.ErrTokenUsed,
		runner.ErrLimit, runner.ErrRetired,
		namespace.ErrFenced, namespace.ErrNotHome, maintenance.ErrInMaintenance,
	}

	for _, sentinel := range sentinels {
		for _, err := range []error{
			fmt.Errorf("%w: %w", identity.ErrDenied, sentinel),
			fmt.Errorf("%w: %w", sentinel, identity.ErrDenied),
		} {
			t.Run(sentinel.Error(), func(t *testing.T) {
				t.Parallel()

				rec := httptest.NewRecorder()
				runnerapi.WriteManagementError(t.Context(), rec, err)

				checkDenied(t, rec, sentinel)
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
