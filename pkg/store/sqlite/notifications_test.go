package sqlite_test

import (
	"testing"

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
