package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/inventory/inventorytest"
)

func TestInventoryHistoryConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	inventorytest.RunHistory(t, func(t *testing.T) inventorytest.Fixture {
		t.Helper()

		return open(t, base)
	})
}
