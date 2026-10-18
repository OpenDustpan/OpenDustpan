package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/inventory/inventorytest"
)

func TestInventoryHistoryConformance(t *testing.T) {
	t.Parallel()

	inventorytest.RunHistory(t, func(t *testing.T) inventorytest.Fixture {
		t.Helper()

		return open(t)
	})
}
