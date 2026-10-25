package triggers_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	triggersapi "opendustpan.org/opendustpan/internal/api/triggers"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// TestDenialWinsOverTheRefusals proves that a denial wins over every refusal
// of a trigger, in either order of the wrap, and before a cycle answer. The
// answer is the same 403 for a missing object and for an object with no right:
// it names no state.
func TestDenialWinsOverTheRefusals(t *testing.T) {
	t.Parallel()

	sentinels := []error{
		triggersapi.ErrNotFound, trigger.ErrNotFound, namespace.ErrNotFound, triggersapi.ErrBody,
		triggersapi.ErrKeyUnsupported, idempotency.ErrInvalidKey, trigger.ErrInvalid, trigger.ErrDisabled,
		job.ErrNotFound, job.ErrInvalid, job.ErrUnsupported, job.ErrPolicyDenied, job.ErrGateEnforced,
		job.ErrAgentDenied, job.ErrKeyConflict, job.ErrFrozen, job.ErrLimit,
		&manuallock.LockedError{
			Lock: manuallock.Lock{
				Since:  time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC),
				Target: manuallock.UnitTarget(collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "net"}),
				Holder: "prn-alice",
				Reason: "release freeze",
			},
			Unit: collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "net"},
			Noun: "workspace",
			Name: "acme/net",
		},
		cycleError(),
	}

	for _, sentinel := range sentinels {
		for _, err := range []error{
			fmt.Errorf("%w: %w", identity.ErrDenied, sentinel),
			fmt.Errorf("%w: %w", sentinel, identity.ErrDenied),
		} {
			t.Run(sentinel.Error(), func(t *testing.T) {
				t.Parallel()

				rec := httptest.NewRecorder()
				triggersapi.WriteError(t.Context(), rec, err)

				checkDenied(t, rec, sentinel)
			})
		}
	}
}

// TestSaverDenialWinsWithItsOwnCode proves that trigger.ErrSaverDenied keeps its own
// 403 code, with or without the wrap of identity.ErrDenied: it is a denial with
// a finer code.
func TestSaverDenialWinsWithItsOwnCode(t *testing.T) {
	t.Parallel()

	for _, err := range []error{trigger.ErrSaverDenied, fmt.Errorf("save: %w", trigger.ErrSaverDenied)} {
		rec := httptest.NewRecorder()
		triggersapi.WriteError(t.Context(), rec, err)

		errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusForbidden, rec.Body.Bytes(),
			errcodetest.Want{Code: errcode.TriggerSaverDenied, Args: nil, RequestBody: nil})
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
