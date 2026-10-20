package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/manuallock/manuallocktest"
)

func TestManualLockConformance(t *testing.T) {
	t.Parallel()

	manuallocktest.Run(t, func(t *testing.T) manuallocktest.Store {
		t.Helper()

		return open(t)
	})
}
