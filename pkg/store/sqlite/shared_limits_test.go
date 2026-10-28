package sqlite_test

import (
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
	"opendustpan.org/opendustpan/pkg/secret"
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

		second := storetest.NewBreakableCounter(store)

		var services [2]*sinks.Service

		counters := [2]authgate.FailureCounter{store, second}

		const shop = namespace.Name("shop")

		for i := range services {
			namespaces, nsErr := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "test-region"})
			if nsErr != nil {
				t.Fatalf("namespace service: %v", nsErr)
			}

			if i == 0 {
				if _, createErr := namespaces.Create(t.Context(), shop); createErr != nil {
					t.Fatalf("create namespace: %v", createErr)
				}
			}

			wrapper, wrapErr := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
			if wrapErr != nil {
				t.Fatalf("wrapper: %v", wrapErr)
			}

			secrets, secErr := secret.NewService(secret.Config{
				Keys: store, Wrapper: wrapper, Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
			})
			if secErr != nil {
				t.Fatalf("secret service: %v", secErr)
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
			Stores:        [2]storetest.WorkspaceStore{store, store},
			Services:      services,
			Namespace:     shop,
			SecondCounter: second,
		}
	})
}
