package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// TestClaimStoreConformance runs the claim conformance suite against SQLite.
func TestClaimStoreConformance(t *testing.T) {
	t.Parallel()

	workspacetest.RunClaimStore(t, func(t *testing.T) workspacetest.ClaimBackend {
		t.Helper()

		return open(t)
	})
}
