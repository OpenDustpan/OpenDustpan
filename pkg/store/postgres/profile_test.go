package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/profile/profiletest"
)

func TestProfileConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	profiletest.Run(t, func(t *testing.T) profiletest.Store {
		t.Helper()

		s := open(t, base)

		return profiletest.Store{Profiles: s.Profiles(), Namespaces: s, Audit: s, Records: s.Idempotency()}
	})
}
