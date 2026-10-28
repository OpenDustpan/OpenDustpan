package sqlite_test

import (
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestSharedLimits runs the limits that hold across instances. SQLite allows
// one handle for each file, so both services use one handle: the limits count
// in the database and not in the service.
func TestSharedLimits(t *testing.T) {
	t.Parallel()

	storetest.RunSharedLimits(t, func(t *testing.T, sender *storetest.CountingSender) storetest.SharedLimits {
		t.Helper()

		store, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(func() {
			if closeErr := store.Close(); closeErr != nil {
				t.Errorf("close store: %v", closeErr)
			}
		})

		const shop = namespace.Name("shop")

		second := storetest.NewBreakableCounter(store)

		return storetest.SharedLimits{
			Stores: [2]storetest.WorkspaceStore{store, store},
			Services: [2]*sinks.Service{
				storetest.NewSharedSinkService(t, store, authgate.FailureCounter(store), sender, shop, true),
				storetest.NewSharedSinkService(t, store, second, sender, shop, false),
			},
			Namespace:     shop,
			SecondCounter: second,
		}
	})
}
