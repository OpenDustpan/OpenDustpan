package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/notify/notifytest"
)

func TestNotificationConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	notifytest.Run(t, func(t *testing.T) notifytest.Harness {
		t.Helper()

		s := open(t, base)
		st := s.Notifications()

		return notifytest.Harness{Outbox: st, Subscriptions: st, Deliveries: st, Domain: sinkDomain{s: s}}
	})
}
