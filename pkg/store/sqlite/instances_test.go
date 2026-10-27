package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

func TestInstanceConformance(t *testing.T) {
	t.Parallel()

	storetest.RunInstances(t, func(t *testing.T) installation.InstanceStore {
		t.Helper()

		return open(t)
	})
}
