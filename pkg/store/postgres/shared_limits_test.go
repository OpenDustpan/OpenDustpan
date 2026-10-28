package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/postgres"
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
		stores := [2]*postgres.Store{openHandle(t, schemaURL), openHandle(t, schemaURL)}
		second := storetest.NewBreakableCounter(stores[1])
		counters := [2]authgate.FailureCounter{stores[0], second}

		const shop = namespace.Name("shop")

		var services [2]*sinks.Service

		for i, store := range stores {
			namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "test-region"})
			if err != nil {
				t.Fatalf("namespace service: %v", err)
			}

			if i == 0 {
				if _, createErr := namespaces.Create(t.Context(), shop); createErr != nil {
					t.Fatalf("create namespace: %v", createErr)
				}
			}

			wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
			if err != nil {
				t.Fatalf("wrapper: %v", err)
			}

			secrets, err := secret.NewService(secret.Config{
				Keys: store, Wrapper: wrapper, Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
			})
			if err != nil {
				t.Fatalf("secret service: %v", err)
			}

			services[i], err = sinks.NewService(sinks.Config{
				Store: store.NotificationSinks(), Sealer: notify.SinkSealer{Namespaces: secrets, Installation: nil},
				Namespaces: namespaces, Senders: []notify.Sender{sender}, Ports: egress.Ports{}, Counter: counters[i],
				Now: nil,
			})
			if err != nil {
				t.Fatalf("sink service: %v", err)
			}
		}

		return storetest.SharedLimits{
			Stores:        [2]storetest.WorkspaceStore{stores[0], stores[1]},
			Services:      services,
			Namespace:     shop,
			SecondCounter: second,
		}
	})
}
