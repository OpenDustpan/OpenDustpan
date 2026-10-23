package jobs_test

import (
	"testing"

	"go.uber.org/goleak"

	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
)

// TestMain fails the package when a test leaves a goroutine running.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(apidesctest.RequireObserved(m, unobservedOperations))
}
