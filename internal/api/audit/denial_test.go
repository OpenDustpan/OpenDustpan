package audit_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	auditapi "opendustpan.org/opendustpan/internal/api/audit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestDenialWinsOverItsOwnRefusal proves that an error that wraps identity.ErrDenied
// and a refusal of this API answers the denial, in either order of the wrap. The
// API has no case for a denial: package problem answers it before the classifier.
func TestDenialWinsOverItsOwnRefusal(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		fmt.Errorf("%w: %w", identity.ErrDenied, audit.ErrEntryNotFound),
		fmt.Errorf("%w: %w", audit.ErrEntryNotFound, identity.ErrDenied),
	} {
		rec := httptest.NewRecorder()
		auditapi.WriteError(t.Context(), rec, err)

		errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusForbidden, rec.Body.Bytes(),
			errcodetest.Want{Code: errcode.AccessDenied, Args: nil, RequestBody: nil})

		if strings.Contains(rec.Body.String(), audit.ErrEntryNotFound.Error()) {
			t.Errorf("body holds the text of the refusal: %s", rec.Body)
		}
	}
}
