package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/preferences/preferencestest"
)

func TestPreferencesConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	preferencestest.Run(t, func(t *testing.T) preferencestest.Store {
		t.Helper()

		return open(t, base)
	})
}
