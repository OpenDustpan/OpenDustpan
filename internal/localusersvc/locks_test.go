package localusersvc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/argon2id"
	"opendustpan.org/opendustpan/pkg/localuser/blocklist"
)

// from returns the entry of a caller at the source address addr.
func from(addr string) audit.Entry {
	e := base("")
	e.SourceIP = addr

	return e
}

// lockedSource is the source address of the wrong passwords of the cases.
const lockedSource = "192.0.2.1"

// fail sends n wrong passwords for name from lockedSource, and fails the
// test unless each one answers ErrSignInFailed.
func (f *fixture) fail(t *testing.T, n int, name string) {
	t.Helper()

	for i := range n {
		if _, err := f.svc.SignIn(t.Context(), from(lockedSource), name, otherPassword); !errors.Is(
			err, localuser.ErrSignInFailed,
		) {
			t.Fatalf("wrong password %d = %v, want ErrSignInFailed", i+1, err)
		}
	}
}

// reasons returns the reasons of the failure entries of the password sign-in.
func (f *fixture) reasons(t *testing.T) map[string]int {
	t.Helper()

	out := map[string]int{}

	entries := f.entriesWith(t, localusersvc.ActionSignInPasswordFails)
	for i := range entries {
		for _, c := range entries[i].Changes {
			out[c.New]++
		}
	}

	return out
}

// TestLockoutByNameAndSource proves the lock of a name from one source
// prefix: after the limit of wrong passwords, the good password from that
// prefix fails with the one answer and the audit reason of a lock, the good
// password from another prefix signs in, and the lock is in the audit log.
func TestLockoutByNameAndSource(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	c := f.mustCreate(t, "alice", goodPassword)

	f.fail(t, authgate.DefaultNameLimit, "alice")

	if _, err := f.svc.SignIn(t.Context(), from("192.0.2.1"), "alice", goodPassword); !errors.Is(
		err, localuser.ErrSignInFailed,
	) {
		t.Fatalf("good password from the locked source = %v, want ErrSignInFailed", err)
	}

	if f.reasons(t)[localusersvc.ReasonNameLocked] != 1 {
		t.Fatalf("reasons = %v, want one lock refusal", f.reasons(t))
	}

	in, err := f.svc.SignIn(t.Context(), from("198.51.100.7"), "alice", goodPassword)
	if err != nil {
		t.Fatalf("good password from another source: %v", err)
	}

	if len(f.entriesWith(t, localusersvc.ActionNameLocked)) != 1 {
		t.Fatal("no audit entry of the lock")
	}

	locks, err := f.svc.Locks(t.Context(), admin(), c.Principal.ID)
	if err != nil {
		t.Fatalf("locks: %v", err)
	}

	locked := 0

	for _, l := range locks {
		if !l.LockedUntil.IsZero() && l.LockedUntil.After(time.Now()) {
			locked++
		}
	}

	if locked != 1 {
		t.Fatalf("locks = %+v, want one lock that holds", locks)
	}

	if err := f.svc.ClearLocks(t.Context(), in); err != nil {
		t.Fatalf("clear after the sign-in: %v", err)
	}
}

// TestUnknownNameLocksAsAKnownName proves that the counts take the digest of
// the name: an unknown name locks after the same count, so the answer and the
// lock tell nothing about a user.
func TestUnknownNameLocksAsAKnownName(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	f.fail(t, authgate.DefaultNameLimit, "nobody")
	f.fail(t, 1, "nobody")

	if got := f.reasons(t); got[localusersvc.ReasonNameLocked] != 1 ||
		got[localusersvc.ReasonUnknownName] != authgate.DefaultNameLimit {
		t.Fatalf("reasons = %v, want the limit of unknown names and one lock refusal", got)
	}
}

// TestUnlockClearsEveryCount proves that an unlock needs a reason, clears
// every count of the name, writes its entry, and lets the person sign in
// from the source that was locked.
func TestUnlockClearsEveryCount(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	c := f.mustCreate(t, "alice", goodPassword)

	f.fail(t, authgate.DefaultNameLimit, "alice")

	if _, err := f.svc.Unlock(t.Context(), admin(), c.Principal.ID, " "); !errors.Is(
		err, localuser.ErrReasonRequired,
	) {
		t.Fatalf("unlock with no reason = %v, want ErrReasonRequired", err)
	}

	n, err := f.svc.Unlock(t.Context(), admin(), c.Principal.ID, "the person called the help desk")
	if err != nil || n != 2 {
		t.Fatalf("unlock = %d, %v; want 2 counts cleared", n, err)
	}

	if len(f.entriesWith(t, localusersvc.ActionNameUnlocked)) == 0 {
		t.Fatal("no audit entry of the unlock")
	}

	if _, err := f.svc.SignIn(t.Context(), from("192.0.2.1"), "alice", goodPassword); err != nil {
		t.Fatalf("sign in after the unlock: %v", err)
	}
}

// TestWrongCurrentPasswordCounts proves that a wrong current password of a
// change counts in the lockout by name.
func TestWrongCurrentPasswordCounts(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	c := f.mustCreate(t, "alice", goodPassword)
	me := localusersvc.Actor{Entry: base(c.Principal.Subject), PrincipalID: c.Principal.ID, Admin: false}

	g := f.generation(t, c.Principal.ID)

	for range authgate.DefaultNameLimit {
		_, err := f.svc.ChangePassword(t.Context(), me, localusersvc.ChangeInput{
			Current: otherPassword, New: otherPassword, Generation: g, Fresh: false,
		})
		if !errors.Is(err, localuser.ErrWrongPassword) {
			t.Fatalf("wrong current password = %v, want ErrWrongPassword", err)
		}
	}

	if _, err := f.svc.SignIn(t.Context(), base(""), "alice", goodPassword); !errors.Is(
		err, localuser.ErrSignInFailed,
	) {
		t.Fatalf("sign in after the wrong current passwords = %v, want ErrSignInFailed", err)
	}
}

// busyHasher answers ErrHashBusy to each reservation, as a full budget does.
type busyHasher struct {
	localuser.PasswordHasher
}

func (busyHasher) Reserve(context.Context, localuser.PasswordHash) (*localuser.Reservation, error) {
	return nil, localuser.ErrHashBusy
}

// errDiskOnFire is the error of a counter whose writes fail.
var errDiskOnFire = errors.New("disk on fire")

// brokenLocks is a counter whose writes fail.
type brokenLocks struct {
	authgate.NameLockCounter
}

func (brokenLocks) AddNameFailure(context.Context, authgate.NameFailure) (authgate.NameLock, error) {
	return authgate.NameLock{}, errDiskOnFire
}

// TestLockLimits sends a full hash budget and a counter that cannot write
// directly to the sign-in: a full budget counts no failure, and a count that
// cannot be written refuses the sign-in with ErrUnavailable.
func TestLockLimits(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	c := f.mustCreate(t, "alice", goodPassword)

	budget, err := argon2id.NewBudget(localuser.DefaultHashMemoryBudgetKiB)
	if err != nil {
		t.Fatalf("budget: %v", err)
	}

	hasher, err := argon2id.New(budget)
	if err != nil {
		t.Fatalf("hasher: %v", err)
	}

	cases := []struct {
		want   error
		hasher localuser.PasswordHasher
		locks  authgate.NameLockCounter
		name   string
	}{
		{name: "full budget", hasher: busyHasher{PasswordHasher: hasher}, locks: f.store, want: localuser.ErrHashBusy},
		{
			name: "count cannot be written", hasher: hasher, locks: brokenLocks{NameLockCounter: f.store},
			want: localuser.ErrUnavailable,
		},
	}

	for _, tc := range cases {
		svc, newErr := localusersvc.New(localusersvc.Config{
			Log:   nil,
			Store: f.store, Principals: f.principals, Hasher: tc.hasher, Blocklist: blocklist.Embedded(),
			Audit: f.store, Locks: tc.locks, Keys: f.store, Settings: f.settings, SSO: nil, Kind: "", Now: nil,
			Reach: noReach(), Subjects: nil,
		})
		if newErr != nil {
			t.Fatalf("service: %v", newErr)
		}

		for range authgate.DefaultNameLimit + 1 {
			if _, signErr := svc.SignIn(t.Context(), base(""), "alice", otherPassword); !errors.Is(signErr, tc.want) {
				t.Fatalf("%s: sign in = %v, want %v", tc.name, signErr, tc.want)
			}
		}
	}

	locks, err := f.svc.Locks(t.Context(), admin(), c.Principal.ID)
	if err != nil || len(locks) != 0 {
		t.Fatalf("counts after the refused attempts = %+v, %v; want none", locks, err)
	}
}

// TestSourcePrefix sends hostile source addresses directly to the prefix of
// a count: each one gives a short, fixed form.
func TestSourcePrefix(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{in: "192.0.2.1", want: "192.0.2.1"},
		{in: "::ffff:192.0.2.1", want: "192.0.2.1"},
		{in: "2001:db8:1:2:3:4:5:6", want: "2001:db8:1:2::/64"},
		{in: "2001:db8:1:2::9", want: "2001:db8:1:2::/64"},
		{in: "fe80::1%eth0", want: "fe80::/64"},
		{in: "", want: "unknown"},
		{in: "192.0.2.1\r\nX-Forged: 1", want: "unknown"},
		{in: "not an address", want: "unknown"},
		{in: "192.0.2.1:443", want: "unknown"},
	}

	for _, tc := range cases {
		if got := localusersvc.SourcePrefix(tc.in); got != tc.want {
			t.Fatalf("prefix of %q = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// noReach returns a reach config with no store: each reset fails closed.
func noReach() access.ReachConfig {
	return access.ReachConfig{Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil}
}

// brokenClear is a counter whose clear of a name fails.
type brokenClear struct {
	authgate.NameLockCounter
}

func (brokenClear) ClearName(context.Context, []string, string, audit.Entry) (int64, error) {
	return 0, errDiskOnFire
}

// TestUnlockThatCannotClearChangesNothing proves that an unlock whose clear
// fails answers ErrUnavailable, writes no entry of a done unlock, and keeps
// the lock.
func TestUnlockThatCannotClearChangesNothing(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	c := f.mustCreate(t, "alice", goodPassword)

	f.fail(t, authgate.DefaultNameLimit, "alice")

	svc, err := localusersvc.New(localusersvc.Config{
		Log:   nil,
		Store: f.store, Principals: f.principals, Hasher: f.hasher, Blocklist: blocklist.Embedded(),
		Audit: f.store, Locks: brokenClear{NameLockCounter: f.store}, Keys: f.store, Settings: f.settings,
		SSO: nil, Kind: "", Now: nil, Reach: f.storeReach(), Subjects: f.store,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	if _, unlockErr := svc.Unlock(t.Context(), admin(), c.Principal.ID, "the person called"); !errors.Is(
		unlockErr, localuser.ErrUnavailable,
	) {
		t.Fatalf("unlock with a failed clear = %v, want ErrUnavailable", unlockErr)
	}

	for _, e := range f.entriesWith(t, localusersvc.ActionNameUnlocked) {
		if e.Outcome == audit.OutcomeSuccess {
			t.Fatalf("entry of a done unlock after a failed clear: %+v", e.Changes)
		}
	}

	if _, signErr := f.svc.SignIn(t.Context(), from("192.0.2.1"), "alice", goodPassword); !errors.Is(
		signErr, localuser.ErrSignInFailed,
	) {
		t.Fatalf("sign in after a failed unlock = %v, want ErrSignInFailed", signErr)
	}
}

// TestSourcePrefixUsesTheRules proves that the lengths in the rules that the
// policy shows are the lengths that the prefix of a source applies: addresses
// that agree in the first bits of the rule share one sub-key, and addresses
// that differ in the last bit of the rule do not.
func TestSourcePrefixUsesTheRules(t *testing.T) {
	t.Parallel()

	rules := localusersvc.Rules()
	if rules.IPv4PrefixBits != 32 || rules.IPv6PrefixBits != 64 {
		t.Fatalf("rules = %+v, want 32 and 64 bits", rules)
	}

	cases := []struct {
		name string
		a, b string
		same bool
	}{
		{name: "two IPv4 addresses", a: "192.0.2.1", b: "192.0.2.2", same: false},
		{name: "one IPv4 address", a: "192.0.2.1", b: "::ffff:192.0.2.1", same: true},
		{name: "IPv6 in one /64", a: "2001:db8:1:2::1", b: "2001:db8:1:2:ffff::9", same: true},
		{name: "IPv6 in two /64", a: "2001:db8:1:2::1", b: "2001:db8:1:3::1", same: false},
	}

	for _, tc := range cases {
		if got := localusersvc.SourcePrefix(tc.a) == localusersvc.SourcePrefix(tc.b); got != tc.same {
			t.Fatalf("%s: same sub-key = %v, want %v", tc.name, got, tc.same)
		}
	}
}
