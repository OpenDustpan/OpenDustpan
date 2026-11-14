package sqlite_test

import (
	"path/filepath"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/session/sessiontest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

var _ session.TicketStore = (*sqlite.Store)(nil)

// TestTicketStoreConformance runs the suite of the records of used CAS
// tickets. One file allows one store, so each Open gives that store. A reopen
// closes it and opens a new one on the same file.
func TestTicketStoreConformance(t *testing.T) {
	t.Parallel()

	sessiontest.RunTicketStore(t, func(t *testing.T) sessiontest.TicketStoreHarness {
		t.Helper()

		path := filepath.Join(t.TempDir(), "dustpan.db")

		copyTemplate(t, path)

		var (
			mu      sync.Mutex
			current *sqlite.Store
		)

		return sessiontest.TicketStoreHarness{
			Open: func(t *testing.T) session.TicketStore {
				t.Helper()

				mu.Lock()
				defer mu.Unlock()

				if current == nil {
					current = openPath(t, path)
				}

				return current
			},
			Reopen: func(t *testing.T, old session.TicketStore) session.TicketStore {
				t.Helper()

				mu.Lock()
				defer mu.Unlock()

				if old != session.TicketStore(current) {
					t.Fatal("reopen: store is not the open store")
				}

				if err := current.Close(); err != nil {
					t.Fatalf("close store: %v", err)
				}

				current = openPath(t, path)

				return current
			},
		}
	})
}
