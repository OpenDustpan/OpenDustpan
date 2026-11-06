package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/localuser/localusertest"
)

// TestLocalUserConformance runs the local user store suite.
func TestLocalUserConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	localusertest.Run(t, func(t *testing.T) localusertest.Store {
		t.Helper()

		return open(t, base)
	})
}
