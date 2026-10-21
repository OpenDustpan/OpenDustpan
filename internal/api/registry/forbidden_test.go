package registry_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// TestForbiddenCodes proves that each cause of a 403 keeps its message and
// status and adds its machine code, on the list route.
func TestForbiddenCodes(t *testing.T) {
	t.Parallel()

	for _, cause := range errcodetest.Causes(t) {
		t.Run(cause.Name, func(t *testing.T) {
			t.Parallel()

			f := newFixtureWithAuthorizer(t, errcodetest.Authorizer{Err: cause.Err})
			got := f.do(
				t,
				request{token: "admin", method: http.MethodGet, target: base + "/keys", body: nil, headers: nil},
			)

			errcodetest.Check(t, got.code, []byte(got.body), cause)
		})
	}
}
