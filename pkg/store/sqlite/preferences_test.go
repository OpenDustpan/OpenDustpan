package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/preferences/preferencestest"
)

func TestPreferencesConformance(t *testing.T) {
	t.Parallel()

	preferencestest.Run(t, func(t *testing.T) preferencestest.Store {
		t.Helper()

		return open(t)
	})
}
