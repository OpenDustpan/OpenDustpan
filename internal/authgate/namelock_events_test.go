package authgate_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/notify"
)

// alertTime returns a fixed time of the cases of the alert keys.
func alertTime() time.Time {
	return time.Date(2026, time.November, 2, 12, 30, 15, 0, time.UTC)
}

// lockEnding returns a lock that ends at until.
func lockEnding(until time.Time) authgate.NameLock {
	return authgate.NameLock{LockedUntil: until, Sub: "", Failures: 0, Level: 1}
}

// entryOf returns an entry with the action.
func entryOf(action string) audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: action, Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: "",
		Kind: "", Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

// goodEvents returns valid events: only the actions and the type that the
// check reads are set.
func goodEvents() *authgate.LockEvents {
	return &authgate.LockEvents{
		Locked: entryOf(authgate.ActionNameLocked), Ended: entryOf(authgate.ActionNameLockEnded),
		Alert: notify.Notification{
			ID: "", Type: notify.SignInLocked, Scope: "", Object: "", ObjectID: "", Subject: "", Agent: "",
			Severity: 0, Summary: "", Links: nil, Attributes: nil, Recipients: nil, DedupeKey: "",
			At: time.Time{}, Sequence: 0,
		},
	}
}

// keyCase is two alert keys and whether they must be equal.
type keyCase struct {
	name  string
	a, b  string
	equal bool
}

// TestAlertKey sends the inputs of an alert key directly to the key: a key is
// the same for one name, scope and minute, and differs for another name,
// another scope or another minute. The key of a lock of the name from every
// source differs for each lock.
func TestAlertKey(t *testing.T) {
	t.Parallel()

	at := alertTime()
	lock := lockEnding(at.Add(30 * time.Minute))
	later := lockEnding(lock.LockedUntil.Add(time.Hour))

	cases := []keyCase{
		{
			name: "one name, one scope, one minute", equal: true,
			a: authgate.AlertKey(authgate.ScopePassword, "n1", lock, at),
			b: authgate.AlertKey(authgate.ScopePassword, "n1", lock, at.Add(10*time.Second)),
		},
		{
			name: "another name", equal: false,
			a: authgate.AlertKey(authgate.ScopePassword, "n1", lock, at),
			b: authgate.AlertKey(authgate.ScopePassword, "n2", lock, at),
		},
		{
			name: "another scope", equal: false,
			a: authgate.AlertKey(authgate.ScopePassword, "n1", lock, at),
			b: authgate.AlertKey(authgate.ScopeFactor, "n1", lock, at),
		},
		{
			name: "the next minute", equal: false,
			a: authgate.AlertKey(authgate.ScopePassword, "n1", lock, at),
			b: authgate.AlertKey(authgate.ScopePassword, "n1", lock, at.Add(time.Minute)),
		},
		{
			name: "two locks of the name from every source", equal: false,
			a: authgate.AlertKey(authgate.ScopePasswordName, "n1", lock, at),
			b: authgate.AlertKey(authgate.ScopePasswordName, "n1", later, at),
		},
		{
			name: "one lock of the name from every source at two times", equal: true,
			a: authgate.AlertKey(authgate.ScopePasswordName, "n1", lock, at),
			b: authgate.AlertKey(authgate.ScopePasswordName, "n1", lock, at.Add(time.Minute)),
		},
	}

	for _, tc := range cases {
		if (tc.a == tc.b) != tc.equal {
			t.Fatalf("%s: keys %q and %q, equal = %v, want %v", tc.name, tc.a, tc.b, tc.a == tc.b, tc.equal)
		}

		if len(tc.a) > notify.MaxAttributeLen || !strings.HasPrefix(tc.a, string(notify.SignInLocked)+"/") {
			t.Fatalf("%s: key %q is not a key of the type with a valid length", tc.name, tc.a)
		}
	}
}

// TestAlertWindowKeys proves the rolling hour: the window of a lock by source
// prefix holds the key of each minute of the hour before the add and of the
// minute of the add, including the key that AlertKey gives for the add. The
// window of the name from every source is empty.
func TestAlertWindowKeys(t *testing.T) {
	t.Parallel()

	at := alertTime()
	none := lockEnding(time.Time{})

	keys := authgate.AlertWindowKeys(authgate.ScopePassword, "n1", at)
	if len(keys) != 61 {
		t.Fatalf("keys of the window = %d, want 61", len(keys))
	}

	own := authgate.AlertKey(authgate.ScopePassword, "n1", none, at)
	hourAgo := authgate.AlertKey(authgate.ScopePassword, "n1", none, at.Add(-time.Hour))
	tooOld := authgate.AlertKey(authgate.ScopePassword, "n1", none, at.Add(-time.Hour-time.Minute))
	nextMinute := authgate.AlertKey(authgate.ScopePassword, "n1", none, at.Add(time.Minute))

	if !slices.Contains(keys, own) || !slices.Contains(keys, hourAgo) ||
		slices.Contains(keys, tooOld) || slices.Contains(keys, nextMinute) {
		t.Fatal("the window does not hold exactly the hour before the add and the minute of the add")
	}

	other := authgate.AlertWindowKeys(authgate.ScopePassword, "n2", at)
	if slices.ContainsFunc(other, func(k string) bool { return slices.Contains(keys, k) }) {
		t.Fatal("the windows of two names share a key")
	}

	if got := authgate.AlertWindowKeys(authgate.ScopePasswordName, "n1", at); len(got) != 0 {
		t.Fatalf("window of the name from every source = %v, want none", got)
	}
}

// prunedCase is the counts of a prune and the changes that they give.
type prunedCase struct {
	name    string
	deleted int64
	ended   int64
	want    [2]string
}

// TestPrunedChanges sends the counts of a prune directly to the changes of its
// entry: none deleted, some ended and all ended.
func TestPrunedChanges(t *testing.T) {
	t.Parallel()

	cases := []prunedCase{
		{name: "nothing deleted", deleted: 0, ended: 0, want: [2]string{"0", "0"}},
		{name: "some ended", deleted: 5, ended: 2, want: [2]string{"5", "2"}},
		{name: "all ended", deleted: 3, ended: 3, want: [2]string{"3", "3"}},
	}

	for _, tc := range cases {
		changes := authgate.PrunedChanges(tc.deleted, tc.ended)
		if len(changes) != 2 || changes[0].Key != "pruned" || changes[0].New != tc.want[0] ||
			changes[1].Key != "locksEnded" || changes[1].New != tc.want[1] {
			t.Fatalf("%s: changes = %+v, want pruned %s and locksEnded %s", tc.name, changes, tc.want[0], tc.want[1])
		}
	}
}

// eventsCase is hostile events and the name of the mistake in them.
type eventsCase struct {
	events *authgate.LockEvents
	name   string
}

// mutated returns good events changed by f.
func mutated(f func(ev *authgate.LockEvents)) *authgate.LockEvents {
	ev := goodEvents()
	f(ev)

	return ev
}

// TestLockEventsValidate sends hostile events directly to the check of the
// events: no events, a missing or another action, and a missing or another
// type each fail closed with ErrInvalidLock.
func TestLockEventsValidate(t *testing.T) {
	t.Parallel()

	if err := goodEvents().Validate(); err != nil {
		t.Fatalf("good events = %v, want nil", err)
	}

	cases := []eventsCase{
		{name: "nil events", events: nil},
		{name: "no lock action", events: mutated(func(ev *authgate.LockEvents) { ev.Locked.Action = "" })},
		{name: "another lock action", events: mutated(func(ev *authgate.LockEvents) { ev.Locked.Action = "x" })},
		{name: "no end action", events: mutated(func(ev *authgate.LockEvents) { ev.Ended.Action = "" })},
		{name: "swapped actions", events: mutated(func(ev *authgate.LockEvents) {
			ev.Locked.Action, ev.Ended.Action = ev.Ended.Action, ev.Locked.Action
		})},
		{name: "no alert type", events: mutated(func(ev *authgate.LockEvents) { ev.Alert.Type = "" })},
		{
			name:   "another alert type",
			events: mutated(func(ev *authgate.LockEvents) { ev.Alert.Type = notify.SinkTest }),
		},
	}

	for _, tc := range cases {
		if err := tc.events.Validate(); !errors.Is(err, authgate.ErrInvalidLock) {
			t.Fatalf("%s: validate = %v, want ErrInvalidLock", tc.name, err)
		}
	}
}
