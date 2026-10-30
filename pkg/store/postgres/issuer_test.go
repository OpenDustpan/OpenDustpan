package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/profile/profiletest"
)

func TestIssuerConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	profiletest.RunIssuer(t, func(t *testing.T) profiletest.IssuerStore {
		t.Helper()

		s := open(t, base)

		return profiletest.IssuerStore{Issuer: s.Issuer(), Namespaces: s}
	})
}
