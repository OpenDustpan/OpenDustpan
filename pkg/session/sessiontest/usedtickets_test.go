package sessiontest_test

import (
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// TestMemoryTickets runs the suite of session.UsedTickets on the in-memory
// store of the tests.
func TestMemoryTickets(t *testing.T) {
	t.Parallel()

	sessiontest.RunUsedTickets(t, func(*testing.T) sessiontest.TicketsFixture {
		var (
			mu  sync.Mutex
			now = time.UnixMilli(1_800_000_000_000)
		)

		clock := func() time.Time {
			mu.Lock()
			defer mu.Unlock()

			return now
		}

		return sessiontest.TicketsFixture{
			Tickets: sessiontest.NewMemoryTickets(clock),
			Advance: func(d time.Duration) {
				mu.Lock()
				defer mu.Unlock()

				now = now.Add(d)
			},
			Now: clock,
		}
	})
}
