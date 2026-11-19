package profiletest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/profile/profiletest"
)

// TestSessions runs the open session suite against the reference store in
// memory.
func TestSessions(t *testing.T) {
	t.Parallel()

	profiletest.RunSessions(t, func(t *testing.T) profiletest.SessionStore {
		t.Helper()

		return newMemSessions()
	})
}
