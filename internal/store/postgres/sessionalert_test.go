package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// TestSessionAlertConformance runs the suite of the write of a session and an
// alert in one transaction.
func TestSessionAlertConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	sessiontest.RunAlertStore(t, func(t *testing.T) sessiontest.AlertHarness {
		t.Helper()

		st := open(t, base)

		return sessiontest.AlertHarness{Store: st, Alerts: st, Outbox: st.Notifications()}
	})
}
