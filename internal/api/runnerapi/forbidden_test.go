package runnerapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// TestForbiddenCodes proves that each cause of a 403 keeps its message and
// status and adds its machine code, on the list route of the management API.
func TestForbiddenCodes(t *testing.T) {
	t.Parallel()

	for _, cause := range errcodetest.Causes(t) {
		t.Run(cause.Name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodGet,
				"/api/runners/acme/pools",
				strings.NewReader(""),
			)
			req.Header.Set("Authorization", "token "+adminToken)

			rec := httptest.NewRecorder()
			handlerAuthorizing(t, errcodetest.ReadsThenRefuses{Err: cause.Err}).
				ServeHTTP(rec, req)

			errcodetest.Check(t, rec.Code, rec.Body.Bytes(), cause)
		})
	}
}
