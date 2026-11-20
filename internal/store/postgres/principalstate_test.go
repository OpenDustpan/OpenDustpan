package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// TestPrincipalStateConformance runs the suite of the one port of the
// session generation.
func TestPrincipalStateConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	sessiontest.RunPrincipalStateStore(t, func(t *testing.T) sessiontest.StateStore {
		t.Helper()

		return open(t, base)
	})
}
