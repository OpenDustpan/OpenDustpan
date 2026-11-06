package sessiontest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/session"
)

// AlertHarness is what RunAlertStore needs: a store that meets Store, the
// same store as a session.AlertStore, and the outbox of the same database, to
// read back what the write stored.
type AlertHarness struct {
	Store  Store
	Alerts session.AlertStore
	Outbox notify.Outbox
}

// AlertFactory returns a fresh, empty harness for one subtest.
type AlertFactory func(t *testing.T) AlertHarness

// RunAlertStore executes the conformance suite of session.AlertStore against
// stores produced by newHarness.
func RunAlertStore(t *testing.T, newHarness AlertFactory) {
	t.Helper()

	if newHarness == nil {
		t.Fatal("sessiontest.RunAlertStore needs a store factory")
	}

	cases := []struct {
		test func(t *testing.T, h AlertHarness)
		name string
	}{
		{name: "the session and the alert commit together", test: testAlertCommitsTogether},
		{name: "a refused session stores no alert", test: testAlertRolledBackWithSession},
		{name: "an alert that fails validation stores no session", test: testAlertInvalid},
		{name: "an alert of another scope stores no session", test: testAlertOtherScope},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newHarness(t))
		})
	}
}

// alertOf returns a valid alert with the dedupe key key.
func alertOf(id, key string) notify.Notification {
	return notify.Notification{
		ID: notify.IDPrefix + id, Type: notify.BreakGlassUsed, Scope: audit.ServerScope,
		Object: notify.NotificationsObject(audit.ServerScope), ObjectID: "", Subject: "", Agent: "",
		Severity: notify.SeverityCritical, Summary: "a break-glass account signed in", Links: nil, Attributes: nil,
		Recipients: []notify.Recipient{{Principal: "", Group: "", Admins: true}}, DedupeKey: key, At: at(5),
		Sequence: 0,
	}
}

// storedAlerts returns the notifications of the server scope.
func storedAlerts(t *testing.T, h AlertHarness) []notify.Notification {
	t.Helper()

	got, err := h.Outbox.After(t.Context(), audit.ServerScope, 0, 100)
	if err != nil {
		t.Fatalf("read the outbox: %v", err)
	}

	return got
}

// testAlertCommitsTogether checks that one write stores the session, the
// audit entry and the alert.
func testAlertCommitsTogether(t *testing.T, h AlertHarness) {
	t.Helper()

	mustPerson(t, h.Store, "prn-1")

	ss := newSession("ses-1", "prn-1", at(5))
	n := alertOf("a1", "break-glass.used/ses-1")

	if err := h.Alerts.CreateSessionWithAlert(t.Context(), ss, digest(1), entry(ss.ID, ss.CreatedAt), n); err != nil {
		t.Fatalf("create with alert = %v", err)
	}

	if got := mustGet(t, h.Store); got.ID != "ses-1" {
		t.Errorf("session = %q, want ses-1", got.ID)
	}

	got := storedAlerts(t, h)
	if len(got) != 1 || got[0].ID != n.ID || got[0].Type != notify.BreakGlassUsed || got[0].DedupeKey != n.DedupeKey {
		t.Fatalf("outbox = %+v, want the alert %q", got, n.ID)
	}
}

// testAlertRolledBackWithSession checks that a session that the store refuses
// leaves no alert: both commit, or neither.
func testAlertRolledBackWithSession(t *testing.T, h AlertHarness) {
	t.Helper()

	mustPerson(t, h.Store, "prn-1")

	first := newSession("ses-1", "prn-1", at(5))
	mustSession(t, h.Store, first, digest(1))

	second := newSession("ses-2", "prn-1", at(6))
	err := h.Alerts.CreateSessionWithAlert(
		t.Context(), second, digest(1), entry(second.ID, second.CreatedAt), alertOf("a2", "break-glass.used/ses-2"),
	)
	wantErr(t, "a taken digest", err, session.ErrAlreadyExists)

	if got := storedAlerts(t, h); len(got) != 0 {
		t.Errorf("outbox = %+v, want no alert after a refused session", got)
	}

	// A person who does not exist gets no session and no alert either.
	ghost := newSession("ses-3", "prn-none", at(7))
	err = h.Alerts.CreateSessionWithAlert(
		t.Context(), ghost, digest(3), entry(ghost.ID, ghost.CreatedAt), alertOf("a3", "break-glass.used/ses-3"),
	)
	wantErr(t, "an unknown principal", err, session.ErrPrincipalNotFound)

	if got := storedAlerts(t, h); len(got) != 0 {
		t.Errorf("outbox = %+v, want no alert for an unknown principal", got)
	}
}

// testAlertInvalid checks that an alert that fails validation stores no
// session.
func testAlertInvalid(t *testing.T, h AlertHarness) {
	t.Helper()

	mustPerson(t, h.Store, "prn-1")

	ss := newSession("ses-1", "prn-1", at(5))
	n := alertOf("a1", "break-glass.used/ses-1")
	n.Summary = ""

	err := h.Alerts.CreateSessionWithAlert(t.Context(), ss, digest(1), entry(ss.ID, ss.CreatedAt), n)
	if !errors.Is(err, notify.ErrInvalid) {
		t.Fatalf("create with an invalid alert = %v, want %v", err, notify.ErrInvalid)
	}

	if _, err := h.Store.GetSession(t.Context(), ss.ID); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("get after an invalid alert = %v, want %v", err, session.ErrNotFound)
	}
}

// testAlertOtherScope checks that an alert of a namespace is refused: the
// outbox of a session is the one of the installation.
func testAlertOtherScope(t *testing.T, h AlertHarness) {
	t.Helper()

	mustPerson(t, h.Store, "prn-1")

	ss := newSession("ses-1", "prn-1", at(5))
	n := alertOf("a1", "break-glass.used/ses-1")
	n.Scope = "other"

	err := h.Alerts.CreateSessionWithAlert(t.Context(), ss, digest(1), entry(ss.ID, ss.CreatedAt), n)
	if !errors.Is(err, notify.ErrInvalid) {
		t.Fatalf("create with an alert of another scope = %v, want %v", err, notify.ErrInvalid)
	}

	if _, err := h.Store.GetSession(t.Context(), ss.ID); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("get after an alert of another scope = %v, want %v", err, session.ErrNotFound)
	}
}
