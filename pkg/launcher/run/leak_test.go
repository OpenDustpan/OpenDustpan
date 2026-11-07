package run_test

import (
	"os"
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs a step program when the test binary starts as one, and
// else fails the package when a test leaves a goroutine running.
func TestMain(m *testing.M) {
	if mode := os.Getenv(helperVar); mode != "" {
		stepProgram(mode)

		return
	}

	goleak.VerifyTestMain(m)
}
