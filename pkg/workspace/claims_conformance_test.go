package workspace_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// TestMemoryClaimStoreConformance runs the claim conformance suite against
// the in-memory claim store.
func TestMemoryClaimStoreConformance(t *testing.T) {
	t.Parallel()

	workspacetest.RunClaimStore(t, func(t *testing.T) workspacetest.ClaimBackend {
		t.Helper()

		return workspacetest.NewMemoryClaimBackend()
	})
}
