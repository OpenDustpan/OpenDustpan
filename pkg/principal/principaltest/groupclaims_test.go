package principaltest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/principal/principaltest"
)

// TestGroupClaimsSuite runs the suite of the group claims port with no
// implementation, so that each case shows as skipped until one runs it.
func TestGroupClaimsSuite(t *testing.T) {
	t.Parallel()

	principaltest.RunGroupClaims(t, nil)
}
