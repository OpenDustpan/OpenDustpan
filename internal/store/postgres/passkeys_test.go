package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/mfa/mfatest"
)

// TestPasskeyConformance runs the passkey store suite.
func TestPasskeyConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	mfatest.RunPasskeys(t, func(t *testing.T) mfatest.PasskeyStore {
		t.Helper()

		return open(t, base)
	})
}
