package unmanagedtest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/unmanaged/unmanagedtest"
)

// TestSweepStoreConformance runs the sweep store cases on the memory adapter.
func TestSweepStoreConformance(t *testing.T) {
	t.Parallel()

	unmanagedtest.RunConformance(t, unmanagedtest.NewHarness())
}
