package idempotencytest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
)

// TestMemoryStore runs the suite against the reference store in memory.
func TestMemoryStore(t *testing.T) {
	t.Parallel()

	idempotencytest.Run(t, func(t *testing.T) idempotency.Store {
		t.Helper()

		return idempotencytest.NewMemory()
	})
}
