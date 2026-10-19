package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace/workspacehistorytest"
)

func TestWorkspaceBlameConformance(t *testing.T) {
	t.Parallel()

	workspacehistorytest.Run(t, func(t *testing.T) workspacehistorytest.Store {
		t.Helper()

		return open(t)
	})
}
