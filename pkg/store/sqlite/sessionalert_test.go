package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// TestSessionAlertConformance runs the suite of the write of a session and an
// alert in one transaction.
func TestSessionAlertConformance(t *testing.T) {
	t.Parallel()

	sessiontest.RunAlertStore(t, func(t *testing.T) sessiontest.AlertHarness {
		t.Helper()

		st := open(t)

		return sessiontest.AlertHarness{Store: st, Alerts: st, Outbox: st.Notifications()}
	})
}
