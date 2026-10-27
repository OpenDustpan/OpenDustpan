package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

func TestInstanceConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunInstances(t, func(t *testing.T) installation.InstanceStore {
		t.Helper()

		return open(t, base)
	})
}
