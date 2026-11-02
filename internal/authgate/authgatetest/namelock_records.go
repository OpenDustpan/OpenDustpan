package authgatetest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/notify"
)

// otherScope is a scope that the audit log of the server refuses.
const otherScope = "acme"

// serverSubject is the subject of the records of the suite.
const serverSubject = "server"

// recordObject is the object of the entries and the alerts of the suite.
const recordObject = "local-user:lus-1"

// events returns valid events for one add: the entries of a lock and of the
// end of a lock, and the alert of a lock.
func events() *authgate.LockEvents {
	base := clearEntry()
	base.Subject, base.Object, base.Reason = serverSubject, recordObject, ""

	locked, ended := base, base
	locked.Action, ended.Action = authgate.ActionNameLocked, authgate.ActionNameLockEnded

	return &authgate.LockEvents{
		Locked: locked,
		Ended:  ended,
		Alert: notify.Notification{
			ID: ident.NewID(notify.IDPrefix), Type: notify.SignInLocked,
			Scope: audit.ServerScope, Object: notify.NotificationsObject(audit.ServerScope), ObjectID: "",
			Subject: serverSubject, Agent: "", Severity: notify.SeverityWarning, Summary: "", Links: nil,
			Attributes: nil, Recipients: []notify.Recipient{{Principal: "", Group: "", Admins: true}},
			DedupeKey: "", At: time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC), Sequence: 0,
		},
	}
}

// failure returns the add of one failure of k under r, with valid events.
func failure(k countKey, r authgate.LockRule) authgate.NameFailure {
	return authgate.NameFailure{Events: events(), Scope: k.scope, Name: k.name, Sub: k.sub, Rule: r}
}

// actions returns the actions of the entries, in the order of the append.
func actions(entries []audit.Entry) []string {
	out := make([]string, 0, len(entries))
	for i := range entries {
		out = append(out, entries[i].Action)
	}

	return out
}

// count returns how many times action is in actions.
func count(actions []string, action string) int {
	n := 0

	for _, a := range actions {
		if a == action {
			n++
		}
	}

	return n
}

// change returns the new value of the change key of e, or "".
func change(e audit.Entry, key string) string {
	for _, ch := range e.Changes {
		if ch.Key == key {
			return ch.New
		}
	}

	return ""
}

func testLockRecords(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	before := time.Now()
	got := add(t, c, key(authgate.ScopePasswordName, "n1", ""), rule(1, longLock), nil)
	lockLength(t, before, got, longLock)

	entries := h.Entries(t)
	if names := actions(entries); count(names, authgate.ActionNameLocked) != 1 {
		t.Fatalf("actions = %v, want one %s", names, authgate.ActionNameLocked)
	}

	checkLockEntry(t, &entries[len(entries)-1], got)
	checkLockAlert(t, h.Notices(t))
}

// checkLockEntry checks the entry of the lock got in the scope of the name
// from every source.
func checkLockEntry(t *testing.T, e *audit.Entry, got authgate.NameLock) {
	t.Helper()

	if e.Action != authgate.ActionNameLocked || e.Object != recordObject || e.Outcome != audit.OutcomeSuccess ||
		change(*e, "scope") != authgate.ScopePasswordName || change(*e, "level") != "1" ||
		change(*e, "lockedUntil") != got.LockedUntil.UTC().Format(time.RFC3339) {
		t.Fatalf("entry of the lock = %+v", *e)
	}
}

// checkLockAlert checks that notices holds the one alert of the lock.
func checkLockAlert(t *testing.T, notices []notify.Notification) {
	t.Helper()

	if len(notices) != 1 {
		t.Fatalf("alerts = %d, want 1", len(notices))
	}

	n := &notices[0]
	if n.Type != notify.SignInLocked || n.Scope != audit.ServerScope ||
		n.Object != notify.NotificationsObject(audit.ServerScope) ||
		!slices.Equal(n.Recipients, []notify.Recipient{{Principal: "", Group: "", Admins: true}}) ||
		n.Attributes["scope"] != authgate.ScopePasswordName || n.Attributes["object"] != recordObject {
		t.Fatalf("alert = %+v", *n)
	}
}

func testNoRecord(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	// A count under the limit sets no lock.
	add(t, c, key(authgate.ScopePassword, "n1", "s"), rule(limitThree, longLock), nil)

	if len(h.Entries(t)) != 0 || len(h.Notices(t)) != 0 {
		t.Fatalf("an add with no lock wrote %d entries and %d alerts", len(h.Entries(t)), len(h.Notices(t)))
	}

	add(t, c, key(authgate.ScopePassword, "n2", "s"), rule(1, longLock), nil)

	entries, notices := len(h.Entries(t)), len(h.Notices(t))

	// An add while the lock holds counts nothing and writes nothing.
	add(t, c, key(authgate.ScopePassword, "n2", "s"), rule(1, longLock), authgate.ErrNameLocked)

	if len(h.Entries(t)) != entries || len(h.Notices(t)) != notices {
		t.Fatal("an add while the lock holds wrote a record")
	}
}

func testEndRecord(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	r := authgate.LockRule{Limit: limitTwo, LockFor: shortLock, MaxLockFor: shortLock, ResetAfter: longLock}

	add(t, c, key(authgate.ScopePassword, "n1", "s"), r, nil)
	locked := add(t, c, key(authgate.ScopePassword, "n1", "s"), r, nil)
	waitUnlocked(t, c, authgate.ScopePassword, "n1", locked.LockedUntil)

	if count(actions(h.Entries(t)), authgate.ActionNameLockEnded) != 0 {
		t.Fatal("an entry of the end of a lock exists before any add saw the end")
	}

	add(t, c, key(authgate.ScopePassword, "n1", "s"), r, nil)

	entries := h.Entries(t)
	if count(actions(entries), authgate.ActionNameLockEnded) != 1 {
		t.Fatalf("actions after the first add past the end = %v, want one %s", actions(entries),
			authgate.ActionNameLockEnded)
	}

	var ended audit.Entry

	for i := range entries {
		if entries[i].Action == authgate.ActionNameLockEnded {
			ended = entries[i]
		}
	}

	if change(ended, "scope") != authgate.ScopePassword ||
		change(ended, "endedAt") != locked.LockedUntil.UTC().Format(time.RFC3339) {
		t.Fatalf("entry of the end = %+v", ended)
	}

	add(t, c, key(authgate.ScopePassword, "n1", "s"), r, nil)

	if got := count(actions(h.Entries(t)), authgate.ActionNameLockEnded); got != 1 {
		t.Fatalf("entries of the end after a second add = %d, want 1", got)
	}
}

func testLockRecordFails(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	k := key(authgate.ScopePassword, "n1", "s")
	r := rule(limitTwo, longLock)
	add(t, c, k, r, nil)

	// An entry of another scope cannot be appended, so the add that sets the
	// lock fails as a whole.
	bad := failure(k, r)
	bad.Events.Locked.Namespace = otherScope

	_, err := c.AddNameFailure(t.Context(), bad)
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("add with an entry that fails = %v, want audit.ErrInvalidEntry", err)
	}

	locks, listErr := c.NameLocks(t.Context(), k.scope, k.name)
	if listErr != nil || len(locks) != 1 || locks[0].Failures != 1 || !locks[0].LockedUntil.IsZero() {
		t.Fatalf("count after the failed add = %+v, %v; want 1 failure and no lock", locks, listErr)
	}

	if len(h.Entries(t)) != 0 || len(h.Notices(t)) != 0 {
		t.Fatal("a failed add left a record")
	}

	// The add passes with a good entry: nothing from the failed add stayed.
	got := add(t, c, k, r, nil)
	if got.LockedUntil.IsZero() || got.Failures != limitTwo {
		t.Fatalf("add after the failed add = %+v, want the lock", got)
	}
}

func testEventsInvalid(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	k := key(authgate.ScopePassword, "n1", "s")

	cases := []struct {
		mutate func(ev *authgate.LockEvents)
		name   string
	}{
		{name: "no events", mutate: nil},
		{name: "no lock entry", mutate: func(ev *authgate.LockEvents) { ev.Locked.Action = "" }},
		{name: "another action of the lock", mutate: func(ev *authgate.LockEvents) { ev.Locked.Action = "other" }},
		{name: "no end entry", mutate: func(ev *authgate.LockEvents) { ev.Ended.Action = "" }},
		{name: "no alert type", mutate: func(ev *authgate.LockEvents) { ev.Alert.Type = "" }},
		{name: "another alert type", mutate: func(ev *authgate.LockEvents) { ev.Alert.Type = notify.SinkTest }},
	}

	for _, tc := range cases {
		f := failure(k, rule(1, longLock))

		if tc.mutate == nil {
			f.Events = nil
		} else {
			tc.mutate(f.Events)
		}

		if _, err := c.AddNameFailure(t.Context(), f); !errors.Is(err, authgate.ErrInvalidLock) {
			t.Fatalf("%s: add = %v, want ErrInvalidLock", tc.name, err)
		}
	}

	if locks, err := c.NameLocks(t.Context(), k.scope, k.name); err != nil || len(locks) != 0 {
		t.Fatalf("count after refused events = %+v, %v; want none", locks, err)
	}
}

// alertsOf returns the alerts that carry the scope.
func alertsOf(notices []notify.Notification, scope string) []notify.Notification {
	var out []notify.Notification

	for i := range notices {
		if notices[i].Attributes["scope"] == scope {
			out = append(out, notices[i])
		}
	}

	return out
}

func testAlertBound(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)

	// Two source prefixes of one name lock inside one hour: one alert.
	add(t, c, key(authgate.ScopePassword, "n1", "10.0.0.1"), rule(1, longLock), nil)
	add(t, c, key(authgate.ScopePassword, "n1", "10.0.0.2"), rule(1, longLock), nil)

	if got := len(alertsOf(h.Notices(t), authgate.ScopePassword)); got != 1 {
		t.Fatalf("alerts of two locks by prefix of one name = %d, want 1", got)
	}

	// Another name gets its own alert.
	add(t, c, key(authgate.ScopePassword, "n2", "10.0.0.1"), rule(1, longLock), nil)

	if got := len(alertsOf(h.Notices(t), authgate.ScopePassword)); got != limitTwo {
		t.Fatalf("alerts after a lock of a second name = %d, want 2", got)
	}

	// The lock of a name from every source alerts at once, for each lock.
	r := doubling(shortLock, longLock, longLock)
	first := add(t, c, key(authgate.ScopePasswordName, "n1", ""), r, nil)
	waitUnlocked(t, c, authgate.ScopePasswordName, "n1", first.LockedUntil)
	add(t, c, key(authgate.ScopePasswordName, "n1", ""), r, nil)

	if got := len(alertsOf(h.Notices(t), authgate.ScopePasswordName)); got != limitTwo {
		t.Fatalf("alerts of two global locks = %d, want 2", got)
	}

	// A lock of the factors of a person alerts once in the hour too.
	add(t, c, key(authgate.ScopeFactor, "prn-1", ""), rule(1, shortLock), nil)

	if got := len(alertsOf(h.Notices(t), authgate.ScopeFactor)); got != 1 {
		t.Fatalf("alerts of a factor lock = %d, want 1", got)
	}
}

func testPruneEndedLocks(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	// This lock ends and no add sees the end.
	add(t, c, key(authgate.ScopePassword, "ended", "s"), rule(1, shortLock), nil)
	// This count has no lock.
	add(t, c, key(authgate.ScopePassword, "plain", "s"), rule(limitFive, longLock), nil)

	pause(t, doubleLock)

	n, err := c.PruneNameLocks(t.Context(), shortLock, authgate.MaxNamePruneRows, pruneEntry())
	if err != nil || n != limitTwo {
		t.Fatalf("prune = %d, %v; want 2", n, err)
	}

	var got []string

	entries := h.Entries(t)
	for i := range entries {
		if entries[i].Action == pruneAction {
			got = append(got, change(entries[i], "pruned")+"/"+change(entries[i], "locksEnded"))
		}
	}

	if !slices.Equal(got, []string{"2/1"}) {
		t.Fatalf("pruned and locksEnded of the entries of the prune = %v, want [2/1]", got)
	}
}
