package objectcontext_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	objectcontextapi "opendustpan.org/opendustpan/internal/api/objectcontext"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// TestDenialWinsOverItsOwnRefusal proves that an error that wraps identity.ErrDenied
// and a refusal of this API answers the denial, in either order of the wrap. The
// API has no case for a denial: package problem answers it before the classifier.
func TestDenialWinsOverItsOwnRefusal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{"denial first", fmt.Errorf("%w: %w", identity.ErrDenied, objectcontext.ErrNotFound)},
		{"refusal first", fmt.Errorf("%w: %w", objectcontext.ErrNotFound, identity.ErrDenied)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			objectcontextapi.WriteError(t.Context(), rec, tc.err)

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusForbidden, rec.Body.Bytes(),
				errcodetest.Want{Code: errcode.AccessDenied, Args: nil, RequestBody: nil})

			if strings.Contains(rec.Body.String(), objectcontext.ErrNotFound.Error()) {
				t.Errorf("body holds the text of the refusal: %s", rec.Body)
			}
		})
	}
}
