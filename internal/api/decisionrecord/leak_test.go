package decisionrecord_test

import (
	"testing"

	"go.uber.org/goleak"

	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
)

// TestMain fails the package when a test leaves a goroutine running.
func TestMain(m *testing.M) {
	// No described operation lacks a success-path test: both routes are
	// served, so the allowlist is empty.
	goleak.VerifyTestMain(apidesctest.RequireObserved(m, nil))
}
