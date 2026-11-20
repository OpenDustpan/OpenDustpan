package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/localuser/localusertest"
)

// TestLocalUserConformance runs the local user store suite.
func TestLocalUserConformance(t *testing.T) {
	t.Parallel()

	localusertest.Run(t, func(t *testing.T) localusertest.Store {
		t.Helper()

		return open(t)
	})
}
