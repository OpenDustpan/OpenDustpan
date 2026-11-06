package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/collection/collectiontest"
)

func TestCollectionConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	collectiontest.Run(t, func(t *testing.T) collectiontest.Store {
		t.Helper()

		return open(t, base)
	})
}

func TestPlacementConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	collectiontest.RunPlacement(t, func(t *testing.T) collectiontest.PlacementStore {
		t.Helper()

		return open(t, base)
	})
}
