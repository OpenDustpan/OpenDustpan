package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/session/sessiontest"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

var _ session.TicketStore = (*postgres.Store)(nil)

// TestTicketStoreConformance runs the suite of the records of used CAS
// tickets. Each handle is its own pool on one schema, as two servers open one
// database. A reopen closes the pool and opens a new one.
func TestTicketStoreConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	sessiontest.RunTicketStore(t, func(t *testing.T) sessiontest.TicketStoreHarness {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		return sessiontest.TicketStoreHarness{
			Open: func(t *testing.T) session.TicketStore {
				t.Helper()

				return openURL(t, schemaURL)
			},
			Reopen: func(t *testing.T, old session.TicketStore) session.TicketStore {
				t.Helper()

				closer, ok := old.(*postgres.Store)
				if !ok {
					t.Fatalf("reopen: store is %T, want *postgres.Store", old)
				}

				closer.Close()

				return openURL(t, schemaURL)
			},
		}
	})
}
