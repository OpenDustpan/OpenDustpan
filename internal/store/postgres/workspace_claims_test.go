package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// TestClaimStoreConformance runs the claim conformance suite against
// PostgreSQL: the workspace row lock orders the parallel registrations.
func TestClaimStoreConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	workspacetest.RunClaimStore(t, func(t *testing.T) workspacetest.ClaimBackend {
		t.Helper()

		return open(t, base)
	})
}
