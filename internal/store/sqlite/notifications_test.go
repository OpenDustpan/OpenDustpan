package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/notifytest"
)

func TestNotificationConformance(t *testing.T) {
	t.Parallel()

	notifytest.Run(t, func(t *testing.T) notifytest.Harness {
		t.Helper()

		s := open(t)
		st := s.Notifications()

		return notifytest.Harness{Outbox: st, Subscriptions: st, Deliveries: st, Domain: sinkDomain{s: s}}
	})
}

// TestWiredSinkStoreConformance runs the suite with the sink store that the
// server uses, so there is one implementation of a sink write: the one that
// suspends the subscriptions of a changed sink.
func TestWiredSinkStoreConformance(t *testing.T) {
	t.Parallel()

	notifytest.Run(t, func(t *testing.T) notifytest.Harness {
		t.Helper()

		s := open(t)
		st := s.Notifications()

		wired, ok := s.NotificationSinks().(notify.Subscriptions)
		if !ok {
			t.Fatal("the wired sink store is not a notify.Subscriptions")
		}

		return notifytest.Harness{Outbox: st, Subscriptions: wired, Deliveries: st, Domain: sinkDomain{s: s}}
	})
}
