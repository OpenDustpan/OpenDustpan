package localusersvc_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/blocklist"
	"opendustpan.org/opendustpan/pkg/notify"
)

// alerts returns the alerts of a lock that the store holds.
func (f *fixture) alerts(t *testing.T) []notify.Notification {
	t.Helper()

	got, err := f.store.Notifications().After(t.Context(), audit.ServerScope, 0, 1000)
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}

	var out []notify.Notification

	for i := range got {
		if got[i].Type == notify.SignInLocked {
			out = append(out, got[i])
		}
	}

	return out
}

// lockCase is a user name that the test locks, and the object that the records
// of its lock name.
type lockCase struct {
	name   string
	object string
}

// TestLockWritesItsRecords proves that the lock of a known name and the lock
// of an unknown name each write one audit entry and one alert to the
// administrators, and that no record holds the user name that the client
// sent.
func TestLockWritesItsRecords(t *testing.T) {
	t.Parallel()

	cases := []lockCase{
		{name: "alice", object: ""},
		{name: "nobody-with-this-name", object: "local-user:unknown"},
	}

	f := newFixture(t)
	f.mustCreate(t, "alice", goodPassword)

	for _, tc := range cases {
		f.fail(t, authgate.DefaultNameLimit, tc.name)
	}

	objects := map[string]bool{}

	entries := f.entriesWith(t, localusersvc.ActionNameLocked)
	if len(entries) != len(cases) {
		t.Fatalf("entries of a lock = %d, want %d", len(entries), len(cases))
	}

	for i := range entries {
		if entries[i].Outcome != audit.OutcomeSuccess {
			t.Fatalf("entry %d of a lock = %+v, want a success", i, entries[i])
		}

		objects[entries[i].Object] = true
	}

	if !objects["local-user:unknown"] {
		t.Fatalf("objects of the entries of a lock = %v, want local-user:unknown", objects)
	}

	alerts := f.alerts(t)
	if len(alerts) != len(cases) {
		t.Fatalf("alerts = %d, want %d", len(alerts), len(cases))
	}

	for i := range alerts {
		checkAlert(t, &alerts[i], cases)
	}
}

// checkAlert checks that a goes to the administrators only, names the scope,
// and holds none of the user names of cases.
func checkAlert(t *testing.T, a *notify.Notification, cases []lockCase) {
	t.Helper()

	if a.Severity != notify.SeverityWarning || a.Scope != audit.ServerScope ||
		a.Object != notify.NotificationsObject(audit.ServerScope) || len(a.Recipients) != 1 ||
		!a.Recipients[0].Admins || a.Attributes["scope"] != authgate.ScopePassword {
		t.Fatalf("alert = %+v", *a)
	}

	text := a.Summary + a.Attributes["object"] + a.DedupeKey

	for _, c := range cases {
		if strings.Contains(text, c.name) {
			t.Fatalf("alert holds the user name %q: %q", c.name, text)
		}
	}
}

// failingEvents is a counter that makes the record of a new lock fail, as an
// audit log that cannot take the entry does, or that fails the add as a whole.
type failingEvents struct {
	authgate.NameLockCounter

	err error
}

func (c failingEvents) AddNameFailure(ctx context.Context, f authgate.NameFailure) (authgate.NameLock, error) {
	if c.err != nil {
		return authgate.NameLock{}, c.err
	}

	// An entry of another scope cannot be appended.
	f.Events.Locked.Namespace = "acme"

	return c.NameLockCounter.AddNameFailure(ctx, f)
}

// TestLockRecordFailureFailsClosed sends a failure of the record of a lock
// directly to the sign-in: the attempt is refused with ErrUnavailable before
// the compare, so not even the good password passes. The count does not
// change, the error is in the log, and the refusal has its entry.
func TestLockRecordFailureFailsClosed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err  error
		name string
	}{
		{name: "the entry cannot be appended", err: nil},
		{name: "the write fails", err: errDiskOnFire},
	}

	for _, tc := range cases {
		f := newFixture(t)
		c := f.mustCreate(t, "alice", goodPassword)
		f.fail(t, authgate.DefaultNameLimit-1, "alice")

		var logs bytes.Buffer

		svc := f.serviceWithCounter(t, failingEvents{NameLockCounter: f.store, err: tc.err}, &logs)

		if _, err := svc.SignIn(t.Context(), from(lockedSource), "alice", goodPassword); !errors.Is(
			err, localuser.ErrUnavailable,
		) {
			t.Fatalf("%s: sign in = %v, want ErrUnavailable", tc.name, err)
		}

		checkFailedAdd(t, f, c.Principal.ID, logs.String())
	}
}

// serviceWithCounter returns a service over the store of the fixture with the
// counter locks, and a log that writes to logs.
func (f *fixture) serviceWithCounter(
	t *testing.T,
	locks authgate.NameLockCounter,
	logs *bytes.Buffer,
) *localusersvc.Service {
	t.Helper()

	svc, err := localusersvc.New(localusersvc.Config{
		Store: f.store, Principals: f.principals, Hasher: f.hasher, Blocklist: blocklist.Embedded(),
		Audit: f.store, Locks: locks, Keys: f.store, Settings: f.settings, SSO: nil, Kind: "", Now: nil,
		Reach: noReach(), Subjects: nil, Log: slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// checkFailedAdd checks what a refused add leaves: the error in the log with
// no password and no user name, the earlier count and no lock, no record of a
// lock, and the entry of the refusal.
func checkFailedAdd(t *testing.T, f *fixture, principalID, logs string) {
	t.Helper()

	if !strings.Contains(logs, "could not write the count of a name") {
		t.Fatalf("log = %q, want the error of the write", logs)
	}

	if strings.Contains(logs, goodPassword) || strings.Contains(logs, "alice") {
		t.Fatalf("log holds a password or a user name: %q", logs)
	}

	checkEarlierCount(t, f, principalID)

	if len(f.entriesWith(t, localusersvc.ActionNameLocked)) != 0 || len(f.alerts(t)) != 0 {
		t.Fatal("a failed add left a record of a lock")
	}

	// The list answers the newest entry first.
	refusals := f.entriesWith(t, localusersvc.ActionSignInPasswordFails)
	if len(refusals) == 0 || refusals[0].Outcome != audit.OutcomeFailure {
		t.Fatalf("refusals = %+v, want the newest with the outcome failure", refusals)
	}
}

// checkEarlierCount checks that the count of the password scope is the count
// before the failed add, with no lock.
func checkEarlierCount(t *testing.T, f *fixture, principalID string) {
	t.Helper()

	locks, err := f.svc.Locks(t.Context(), admin(), principalID)
	if err != nil {
		t.Fatalf("list the counts: %v", err)
	}

	for _, l := range locks {
		if l.Scope == authgate.ScopePassword && (l.Failures != authgate.DefaultNameLimit-1 || !l.LockedUntil.IsZero()) {
			t.Fatalf("count after the failed add = %+v, want the earlier count and no lock", l)
		}
	}
}
