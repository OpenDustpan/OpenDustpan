package storetest_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/authgate/authgatetest"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestSignInFailuresConformance runs the suite against the in-memory
// counter, so the suite itself is tested before a SQL store runs it.
func TestSignInFailuresConformance(t *testing.T) {
	t.Parallel()

	storetest.RunSignInFailures(t, func(*testing.T) storetest.SignInFailureHarness {
		store := authgatetest.NewMemory(nil)

		return storetest.SignInFailureHarness{
			// The memory counter reads only its own clock, the clock of
			// the store, so every handle is the same counter.
			Open: func(*testing.T, time.Duration) authgate.FailureCounter { return store },
		}
	})
}
