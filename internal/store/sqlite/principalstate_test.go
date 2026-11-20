package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// TestPrincipalStateConformance runs the suite of the one port of the
// session generation.
func TestPrincipalStateConformance(t *testing.T) {
	t.Parallel()

	sessiontest.RunPrincipalStateStore(t, func(t *testing.T) sessiontest.StateStore {
		t.Helper()

		return open(t)
	})
}
