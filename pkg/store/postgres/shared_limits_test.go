package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestSharedLimits runs the limits that hold across instances on two store
// handles that share one database, as two server instances do.
func TestSharedLimits(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the shared limits", envURL)
	}

	storetest.RunSharedLimits(t, func(t *testing.T, sender *storetest.CountingSender) storetest.SharedLimits {
		t.Helper()

		schemaURL := isolatedURL(t, base)
		first, second := openHandle(t, schemaURL), openHandle(t, schemaURL)
		breakable := storetest.NewBreakableCounter(second)

		const shop = namespace.Name("shop")

		return storetest.SharedLimits{
			Stores: [2]storetest.WorkspaceStore{first, second},
			Services: [2]*sinks.Service{
				storetest.NewSharedSinkService(t, first, authgate.FailureCounter(first), sender, shop, true),
				storetest.NewSharedSinkService(t, second, breakable, sender, shop, false),
			},
			Namespace:     shop,
			SecondCounter: breakable,
		}
	})
}
