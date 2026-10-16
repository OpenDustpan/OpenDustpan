package storetest_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/authgate/authgatetest"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestSignInFailuresConformance runs the suite against the in-memory
// counter, so the suite itself is tested before a SQL store runs it.
func TestSignInFailuresConformance(t *testing.T) {
	t.Parallel()

	storetest.RunSignInFailures(t, func(*testing.T) authgate.FailureCounter {
		return authgatetest.NewMemory()
	})
}
