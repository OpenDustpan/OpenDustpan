package principaltest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/principal/principaltest"
)

// TestGroupClaimsSuite runs the suites of the group claims ports with no
// implementation, so that each case shows as skipped until one runs it.
func TestGroupClaimsSuite(t *testing.T) {
	t.Parallel()

	t.Run("GroupMapper", func(t *testing.T) {
		t.Parallel()
		principaltest.RunGroupMapper(t, nil)
	})
	t.Run("GroupClaims", func(t *testing.T) {
		t.Parallel()
		principaltest.RunGroupClaims(t, nil)
	})
}
