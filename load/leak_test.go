package main_test

import (
	"os"
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a test leaves a goroutine running.
func TestMain(m *testing.M) {
	// The tests of the server process start this test binary as a fake
	// server. See fake_server_test.go.
	if os.Getenv(fakeServerEnv) != "" {
		//nolint:forbidigo // The TestMain of a test binary that plays a fake server: the exit status is the contract.
		os.Exit(runFakeServer())
	}

	goleak.VerifyTestMain(m)
}
