package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace/workspacehistorytest"
)

func TestWorkspaceBlameConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	workspacehistorytest.Run(t, func(t *testing.T) workspacehistorytest.Store {
		t.Helper()

		return open(t, base)
	})
}
