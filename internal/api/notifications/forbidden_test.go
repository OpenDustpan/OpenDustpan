package notifications_test

import (
	"testing"

	apinotifications "opendustpan.org/opendustpan/internal/api/notifications"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// TestForbiddenCodes proves that each cause of a 403 keeps its message and
// status and adds its machine code, on the list route.
func TestForbiddenCodes(t *testing.T) {
	t.Parallel()

	for _, cause := range errcodetest.Causes(t) {
		t.Run(cause.Name, func(t *testing.T) {
			t.Parallel()

			f := newFixtureWithoutKeys(t)

			cfg := f.config
			cfg.Authorizer = errcodetest.Authorizer{Err: cause.Err}

			h, err := apinotifications.NewHandler(cfg)
			if err != nil {
				t.Fatalf("handler: %v", err)
			}

			f.handler = h

			got := f.do(t, get("admin", base))

			errcodetest.Check(t, got.code, []byte(got.body), cause)
		})
	}
}
