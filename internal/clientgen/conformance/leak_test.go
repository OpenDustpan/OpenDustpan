package conformance_test

import (
	"os"
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a test leaves a goroutine running. A process
// that has the fake adapter mode runs the fake adapter in place of the tests.
func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeModeEnv); mode != "" {
		runFakeAdapter(mode)

		return
	}

	goleak.VerifyTestMain(m)
}
