package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/mfa/mfatest"
)

// TestPasskeyConformance runs the passkey store suite.
func TestPasskeyConformance(t *testing.T) {
	t.Parallel()

	mfatest.RunPasskeys(t, func(t *testing.T) mfatest.PasskeyStore {
		t.Helper()

		return open(t)
	})
}
