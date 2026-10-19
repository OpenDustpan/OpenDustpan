package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/profile/profiletest"
)

func TestProfileConformance(t *testing.T) {
	t.Parallel()

	profiletest.Run(t, func(t *testing.T) profiletest.Store {
		t.Helper()

		s := open(t)

		return profiletest.Store{Profiles: s.Profiles(), Namespaces: s, Audit: s}
	})
}
