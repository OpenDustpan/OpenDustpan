package decisionrecord_test

import (
	"testing"

	"go.uber.org/goleak"

	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
)

// unobservedOperations lists the described operations whose success path no
// test of this package serves. It is empty: the tests serve both routes.
var unobservedOperations = []string{}

// TestMain fails the package when a test leaves a goroutine running.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(apidesctest.RequireObserved(m, unobservedOperations))
}
