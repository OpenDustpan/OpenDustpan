package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/profile/profiletest"
)

func TestProfileConformance(t *testing.T) {
	t.Parallel()

	profiletest.Run(t, func(t *testing.T) profiletest.Store {
		t.Helper()

		s := open(t)

		wrapper := idempotencytest.TestWrapper(t, 1)

		return profiletest.Store{
			Profiles: s.Profiles(), Namespaces: s, Audit: s, Records: s.Idempotency(wrapper),
		}
	})
}
