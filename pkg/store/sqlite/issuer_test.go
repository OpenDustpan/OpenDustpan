package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/profile/profiletest"
)

func TestIssuerConformance(t *testing.T) {
	t.Parallel()

	profiletest.RunIssuer(t, func(t *testing.T) profiletest.IssuerStore {
		t.Helper()

		s := open(t)

		return profiletest.IssuerStore{Issuer: s.Issuer(), Namespaces: s}
	})
}
