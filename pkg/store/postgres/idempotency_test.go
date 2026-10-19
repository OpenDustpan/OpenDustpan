package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
)

func TestIdempotencyConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	idempotencytest.Run(t, func(t *testing.T) idempotency.Store {
		t.Helper()

		return open(t, base).Idempotency()
	})
}
