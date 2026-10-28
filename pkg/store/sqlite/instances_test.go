package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// The store meets installation.MinimumChecker, so the start check of a restore
// and the readiness probe of the server never skip it.
var _ installation.MinimumChecker = (*sqlite.Store)(nil)

func TestInstanceConformance(t *testing.T) {
	t.Parallel()

	storetest.RunInstances(t, func(t *testing.T) installation.InstanceStore {
		t.Helper()

		return open(t)
	})
}
