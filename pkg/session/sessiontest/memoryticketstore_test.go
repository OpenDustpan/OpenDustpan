package sessiontest_test

import (
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// TestMemoryTicketStore runs the suite of session.TicketStore on the
// reference store.
func TestMemoryTicketStore(t *testing.T) {
	t.Parallel()

	sessiontest.RunTicketStore(t, func(*testing.T) sessiontest.TicketStoreHarness {
		s := sessiontest.NewMemoryTicketStore()

		return sessiontest.TicketStoreHarness{
			Open:   func(*testing.T) session.TicketStore { return s },
			Reopen: func(_ *testing.T, old session.TicketStore) session.TicketStore { return old },
		}
	})
}

// TestStoreTickets runs the suite of session.UsedTickets on the adapter over
// the reference store.
func TestStoreTickets(t *testing.T) {
	t.Parallel()

	sessiontest.RunUsedTickets(t, func(t *testing.T) sessiontest.TicketsFixture {
		t.Helper()

		var (
			mu  sync.Mutex
			now = time.UnixMilli(1_800_000_000_000)
		)

		clock := func() time.Time {
			mu.Lock()
			defer mu.Unlock()

			return now
		}

		tickets, err := session.NewStoreTickets(sessiontest.NewMemoryTicketStore(), clock)
		if err != nil {
			t.Fatalf("NewStoreTickets() = %v", err)
		}

		return sessiontest.TicketsFixture{
			Tickets: tickets,
			Advance: func(d time.Duration) {
				mu.Lock()
				defer mu.Unlock()

				now = now.Add(d)
			},
			Now: clock,
		}
	})
}
