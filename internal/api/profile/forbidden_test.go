package profile_test

import (
	"testing"

	apiprofile "opendustpan.org/opendustpan/internal/api/profile"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// TestForbiddenCodes proves that each cause of a 403 keeps its message and
// status and adds its machine code, on the list route.
func TestForbiddenCodes(t *testing.T) {
	t.Parallel()

	for _, cause := range errcodetest.Causes(t) {
		t.Run(cause.Name, func(t *testing.T) {
			t.Parallel()

			f := buildFixtureWith(t, noMiddleware,
				func(cfg *apiprofile.Config, _ *sqlite.Store, _ *namespace.Service) {
					cfg.Authorizer = errcodetest.Authorizer{Err: cause.Err}
				})

			got := f.do(t, get("admin", base))

			errcodetest.Check(t, got.code, []byte(got.body), cause)
		})
	}
}
