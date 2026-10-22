package approverdirectorytest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/gate/approverdirectorytest"
)

// TestMemoryConformance runs the suite on the reference adapter.
func TestMemoryConformance(t *testing.T) {
	t.Parallel()

	approverdirectorytest.Run(t, approverdirectorytest.NewMemory)
}
