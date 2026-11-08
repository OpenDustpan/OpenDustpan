package authgatetest

import (
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/notify"
)

// NameLockHarness opens handles on one store that counts the failures of
// names. Two handles act as two instances on one database.
type NameLockHarness struct {
	// Open returns a new handle on the store of the harness.
	Open func(t *testing.T) authgate.NameLockCounter
	// Age moves every stored name count of the store of the harness back in
	// time by d: each time column of a count goes back by d, and a column of
	// 0 stays 0.
	Age func(t *testing.T, d time.Duration)
	// Broken returns a handle whose writes fail, for example a closed store.
	Broken func(t *testing.T) authgate.NameLockCounter
	// Entries returns the audit entries of the installation that the store
	// of the harness holds, in the order of the append.
	Entries func(t *testing.T) []audit.Entry
	// Notices returns the notifications of the server scope that the store
	// of the harness holds, in the order of the append.
	Notices func(t *testing.T) []notify.Notification
}

// NameLockFactory returns a fresh harness on an empty store for one subtest.
type NameLockFactory func(t *testing.T) NameLockHarness

// Times of the cases. A short lock ends many times inside one case; a long
// lock never ends inside one case.
const (
	shortLock = 300 * time.Millisecond
	longLock  = time.Hour
	waitLimit = 10 * time.Second
	// clockSlack is how far the time of a lock can be from the time of the
	// test: the store and the test read clocks of one host.
	clockSlack = 5 * time.Second
	// concurrentAdds is the count of adds at the same time.
	concurrentAdds = 24
	// doubleLock is twice a short lock, and halfLonger one and a half times.
	doubleLock = 2 * shortLock
	halfLonger = shortLock + shortLock/2
	// pollEvery is the pause between two reads of a lock that should end,
	// and endSlack the wait past its end before the first read.
	pollEvery = 20 * time.Millisecond
	endSlack  = 50 * time.Millisecond
	// Limits of the cases.
	limitTwo   = 2
	limitThree = 3
	limitFive  = 5
	// levelTwo is the level of a second lock.
	levelTwo = 2
	// overLongName is a name above the limit of a name.
	overLongName = authgate.MaxLockNameBytes + 44
)

// nameCase is one case of the suite.
type nameCase struct {
	test func(t *testing.T, h NameLockHarness)
	name string
}

// nameCases returns the cases of the suite, in the order of the run.
func nameCases() []nameCase {
	return slices.Concat(countCases(), recordCases())
}

// countCases returns the cases of the counts, the locks, the clear and the
// prune.
func countCases() []nameCase {
	return []nameCase{
		{name: "a first failure counts 1 and sets no lock", test: testFirstFailure},
		{name: "the failure that reaches the limit passes and sets the lock", test: testReachLimit},
		{
			name: "a failure while the lock holds fails with ErrNameLocked and counts nothing",
			test: testLockedCountsNothing,
		},
		{name: "a failure after the lock ends starts a new count at 1", test: testAfterLockEnds},
		{name: "a second lock within the reset time lasts twice the first", test: testDoubles},
		{name: "a lock never lasts longer than the maximum", test: testMaximum},
		{
			name: "a lock after the reset time with no lock lasts the first lock time again",
			test: testLevelResets,
		},
		{name: "a rule whose maximum equals its lock time never doubles", test: testNeverDoubles},
		{name: "a clear sets the count to 0, ends the lock and keeps the level", test: testClear},
		{name: "a clear of a count that does not exist is no error", test: testClearMissing},
		{
			name: "a clear of a name ends the count of each sub-key in each scope, returns the count and appends " +
				"its entry",
			test: testClearName,
		},
		{
			name: "a clear of a name with an entry that fails validation fails with audit.ErrInvalidEntry and " +
				"changes nothing",
			test: testClearNameBadEntry,
		},
		{
			name: "a clear of a name with no scope, a scope that is not valid or an empty name fails with " +
				"ErrInvalidLock and changes nothing",
			test: testClearNameInvalid,
		},
		{
			name: "the list of a name returns one entry for each sub-key in the order of the sub-key",
			test: testList,
		},
		{name: "the count of one sub-key does not lock another sub-key of the same name", test: testSubKeys},
		{name: "two scopes keep separate counts for one name", test: testScopes},
		{name: "concurrent failures never pass the limit", test: testConcurrent},
		{name: "the lock time comes from the clock of the store", test: testStoreClock},
		{name: "every instance on one database shares one count", test: testShared},
		{
			name: "a prune deletes old counts, keeps a lock that holds, a level that has not reset and a recent " +
				"count, and appends its entry with the count",
			test: testPrune,
		},
		{name: "a prune deletes no more than its limit and the next prune deletes the rest", test: testPruneBound},
		{
			name: "a prune with an age of 0 or less, or a limit below 1 or above the maximum, fails with " +
				"ErrInvalidLock and changes nothing",
			test: testPruneInvalid,
		},
		{
			name: "a prune with an entry that fails validation fails with audit.ErrInvalidEntry and changes nothing",
			test: testPruneBadEntry,
		},
		{name: "a write that fails is an error that is not ErrNameLocked", test: testBroken},
		{
			name: "a scope with a character other than a to z, an empty name, a limit below 1, a lock time of 0, " +
				"a maximum below the lock time or a negative reset fails with ErrInvalidLock",
			test: testInvalid,
		},
		{name: "the store holds the name as the caller gives it and no user name", test: testExactName},
	}
}

// recordCases returns the cases of the records that an add writes.
func recordCases() []nameCase {
	return []nameCase{
		{
			name: "the add that sets a lock appends the entry and the alert of the lock in its transaction",
			test: testLockRecords,
		},
		{name: "an add that sets no lock, or that finds a lock, appends no record", test: testNoRecord},
		{
			name: "the first add after a lock ended appends the entry of the end, and the next add appends none",
			test: testEndRecord,
		},
		{
			name: "an entry of a lock that cannot be appended fails the add and changes nothing",
			test: testLockRecordFails,
		},
		{
			name: "events that miss an action or a type fail with ErrInvalidLock and change nothing",
			test: testEventsInvalid,
		},
		{
			name: "one name gets at most one alert of a lock by source prefix in one hour, and one at once for " +
				"each lock of the name from every source",
			test: testAlertBound,
		},
		{
			name: "a prune writes one entry for each lock that ended with no add to see it, and counts them in " +
				"its entry",
			test: testPruneEndedLocks,
		},
		{
			name: "a prune deletes no more counts with an ended lock than its limit, so each one has its entry",
			test: testPruneEndedBound,
		},
		{
			name: "a prune with an ended limit above its limit fails with ErrInvalidLock and deletes nothing, so a " +
				"run never deletes more counts than its limit",
			test: testPruneEndedAboveLimit,
		},
		{
			name: "an alert that cannot be appended fails the add and changes nothing",
			test: testAlertAppendFails,
		},
		{
			name: "an entry of the end of a lock that cannot be appended fails the add and changes nothing",
			test: testEndRecordFails,
		},
	}
}

// NameLockCaseNames returns the names of the cases of the suite of
// authgate.NameLockCounter, in the order of the run.
func NameLockCaseNames() []string {
	cases := nameCases()
	names := make([]string, 0, len(cases))

	for _, c := range cases {
		names = append(names, c.name)
	}

	return names
}

// RunNameLocks runs every case of the suite against the harnesses that
// newHarness returns.
func RunNameLocks(t *testing.T, newHarness NameLockFactory) {
	t.Helper()

	for _, c := range nameCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if newHarness == nil {
				t.Fatal("authgatetest.RunNameLocks needs a harness factory")
			}

			c.test(t, newHarness(t))
		})
	}
}

// rule returns a rule with the limit and the lock time, that never doubles.
func rule(limit int, lockFor time.Duration) authgate.LockRule {
	return authgate.LockRule{Limit: limit, LockFor: lockFor, MaxLockFor: lockFor, ResetAfter: 0}
}

// doubling returns a rule of limit 1 that doubles from lockFor up to max, and
// resets after reset with no lock.
func doubling(lockFor, maxLockFor, reset time.Duration) authgate.LockRule {
	return authgate.LockRule{Limit: 1, LockFor: lockFor, MaxLockFor: maxLockFor, ResetAfter: reset}
}

// countKey names one count: its scope, its name and its sub-key.
type countKey struct {
	scope, name, sub string
}

// key returns the key of the count of name and sub in scope.
func key(scope, name, sub string) countKey {
	return countKey{scope: scope, name: name, sub: sub}
}

// add adds one failure and fails the test on an error that is not want.
func add(t *testing.T, c authgate.NameLockCounter, k countKey, r authgate.LockRule, want error) authgate.NameLock {
	t.Helper()

	got, err := c.AddNameFailure(t.Context(), failure(k, r))
	if !errors.Is(err, want) || (want == nil && err != nil) {
		t.Fatalf("add %s/%s/%s = %v, want %v", k.scope, k.name, k.sub, err, want)
	}

	return got
}

// pause waits for d, or fails the test when its context ends first.
func pause(t *testing.T, d time.Duration) {
	t.Helper()

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal("the test ended during a pause")
	}
}

// waitUnlocked waits until the lock of name and sub in scope ended on the
// clock of the store.
func waitUnlocked(t *testing.T, c authgate.NameLockCounter, scope, name string, until time.Time) {
	t.Helper()

	deadline := time.Now().Add(waitLimit)

	for time.Now().Before(deadline) {
		if time.Now().After(until.Add(endSlack)) {
			locks, err := c.NameLocks(t.Context(), scope, name)
			if err != nil {
				t.Fatalf("list: %v", err)
			}

			if len(locks) == 0 || !locks[0].LockedUntil.After(time.Now()) {
				return
			}
		}

		pause(t, pollEvery)
	}

	t.Fatalf("the lock of %s/%s did not end", scope, name)
}

// lockLength returns how long the lock lasts from the time of its add.
func lockLength(t *testing.T, before time.Time, l authgate.NameLock, want time.Duration) {
	t.Helper()

	if l.LockedUntil.IsZero() {
		t.Fatal("no lock, want one")
	}

	got := l.LockedUntil.Sub(before)
	if got < want-clockSlack || got > want+clockSlack {
		t.Fatalf("lock of %s, want %s", got, want)
	}
}

func testFirstFailure(t *testing.T, h NameLockHarness) {
	t.Helper()

	got := add(t, h.Open(t), key(authgate.ScopePassword, "n1", "s"), rule(limitThree, longLock), nil)
	if got.Failures != 1 || !got.LockedUntil.IsZero() || got.Level != 0 || got.Sub != "s" {
		t.Fatalf("first failure = %+v, want 1 failure and no lock", got)
	}
}

func testReachLimit(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	before := time.Now()

	add(t, c, key(authgate.ScopePassword, "n1", "s"), rule(limitThree, longLock), nil)
	add(t, c, key(authgate.ScopePassword, "n1", "s"), rule(limitThree, longLock), nil)
	got := add(t, c, key(authgate.ScopePassword, "n1", "s"), rule(limitThree, longLock), nil)

	if got.Failures != limitThree || got.Level != 1 {
		t.Fatalf("third failure = %+v, want 3 failures at level 1", got)
	}

	lockLength(t, before, got, longLock)
}

func testLockedCountsNothing(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	locked := add(t, c, key(authgate.ScopePassword, "n1", "s"), rule(1, longLock), nil)
	again := add(t, c, key(authgate.ScopePassword, "n1", "s"), rule(1, longLock), authgate.ErrNameLocked)

	if again.Failures != locked.Failures || !again.LockedUntil.Equal(locked.LockedUntil) || again.Level != 1 {
		t.Fatalf("add while locked = %+v, want the state %+v", again, locked)
	}
}

func testAfterLockEnds(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	// The reset time is long, so the level of the lock stays after it ends.
	r := authgate.LockRule{Limit: limitTwo, LockFor: shortLock, MaxLockFor: shortLock, ResetAfter: longLock}
	add(t, c, key(authgate.ScopePassword, "n1", "s"), r, nil)
	locked := add(t, c, key(authgate.ScopePassword, "n1", "s"), r, nil)
	waitUnlocked(t, c, authgate.ScopePassword, "n1", locked.LockedUntil)

	got := add(t, c, key(authgate.ScopePassword, "n1", "s"), r, nil)
	if got.Failures != 1 || !got.LockedUntil.IsZero() || got.Level != 1 {
		t.Fatalf("add after the lock = %+v, want 1 failure, no lock, level 1", got)
	}
}

func testDoubles(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	r := doubling(shortLock, longLock, longLock)
	first := add(t, c, key(authgate.ScopePasswordName, "n1", ""), r, nil)
	waitUnlocked(t, c, authgate.ScopePasswordName, "n1", first.LockedUntil)

	before := time.Now()
	second := add(t, c, key(authgate.ScopePasswordName, "n1", ""), r, nil)

	if second.Level != levelTwo {
		t.Fatalf("second lock = %+v, want level 2", second)
	}

	lockLength(t, before, second, doubleLock)
}

func testMaximum(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	r := doubling(shortLock, halfLonger, longLock)
	first := add(t, c, key(authgate.ScopePasswordName, "n1", ""), r, nil)
	waitUnlocked(t, c, authgate.ScopePasswordName, "n1", first.LockedUntil)

	before := time.Now()
	second := add(t, c, key(authgate.ScopePasswordName, "n1", ""), r, nil)

	lockLength(t, before, second, r.MaxLockFor)
}

func testLevelResets(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	r := doubling(shortLock, longLock, shortLock)
	first := add(t, c, key(authgate.ScopePasswordName, "n1", ""), r, nil)
	waitUnlocked(t, c, authgate.ScopePasswordName, "n1", first.LockedUntil.Add(r.ResetAfter))

	before := time.Now()
	second := add(t, c, key(authgate.ScopePasswordName, "n1", ""), r, nil)

	if second.Level != 1 {
		t.Fatalf("lock after the reset time = %+v, want level 1", second)
	}

	lockLength(t, before, second, shortLock)
}

func testNeverDoubles(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	r := rule(1, shortLock)
	first := add(t, c, key(authgate.ScopeFactor, "prn-1", ""), r, nil)
	waitUnlocked(t, c, authgate.ScopeFactor, "prn-1", first.LockedUntil)

	before := time.Now()
	second := add(t, c, key(authgate.ScopeFactor, "prn-1", ""), r, nil)

	lockLength(t, before, second, shortLock)
}

func testClear(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	add(t, c, key(authgate.ScopePassword, "n1", "s"), rule(1, longLock), nil)

	if err := c.ClearNameFailures(t.Context(), authgate.ScopePassword, "n1", "s"); err != nil {
		t.Fatalf("clear: %v", err)
	}

	got := add(t, c, key(authgate.ScopePassword, "n1", "s"), rule(limitThree, longLock), nil)
	if got.Failures != 1 || !got.LockedUntil.IsZero() || got.Level != 1 {
		t.Fatalf("add after a clear = %+v, want 1 failure, no lock, level 1", got)
	}
}

func testClearMissing(t *testing.T, h NameLockHarness) {
	t.Helper()

	if err := h.Open(t).ClearNameFailures(t.Context(), authgate.ScopePassword, "nobody", "s"); err != nil {
		t.Fatalf("clear of a missing count = %v, want nil", err)
	}
}

func testClearName(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	for _, sub := range []string{"a", "b", "c"} {
		add(t, c, key(authgate.ScopePassword, "n1", sub), rule(1, longLock), nil)
	}

	add(t, c, key(authgate.ScopePasswordName, "n1", ""), rule(1, longLock), nil)
	add(t, c, key(authgate.ScopePassword, "n2", "a"), rule(1, longLock), nil)

	before := count(actions(h.Entries(t)), clearAction)

	n, err := c.ClearName(t.Context(), clearScopes(), "n1", clearEntry())
	if err != nil || n != limitThree+1 {
		t.Fatalf("clear name = %d, %v; want 4", n, err)
	}

	for _, sub := range []string{"a", "b", "c"} {
		add(t, c, key(authgate.ScopePassword, "n1", sub), rule(1, longLock), nil)
	}

	add(t, c, key(authgate.ScopePasswordName, "n1", ""), rule(1, longLock), nil)
	add(t, c, key(authgate.ScopePassword, "n2", "a"), rule(1, longLock), authgate.ErrNameLocked)

	entries := h.Entries(t)
	if got := count(actions(entries), clearAction); got != before+1 {
		t.Fatalf("entries of the clear after a clear = %d, want %d", got, before+1)
	}

	var cleared []string

	for i := range entries {
		if entries[i].Action != clearAction {
			continue
		}

		for _, ch := range entries[i].Changes {
			if ch.Key == "cleared" {
				cleared = append(cleared, ch.New)
			}
		}
	}

	if len(cleared) != 1 || cleared[0] != "4" {
		t.Fatalf("cleared counts of the entries of the clear = %v, want [4]", cleared)
	}
}

// clearAction is the action of the entry of a clear in the suite.
const clearAction = "local-user.name-unlocked"

// clearScopes returns the scopes of the clear of a name in the suite.
func clearScopes() []string {
	return []string{authgate.ScopePassword, authgate.ScopePasswordName}
}

// clearEntry returns a valid entry of the installation for a clear.
func clearEntry() audit.Entry {
	return audit.Entry{
		At: time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC), Namespace: audit.ServerScope,
		Subject: "corp:admin", IDPSubject: "", TokenID: "", Action: clearAction, Object: "local-user:lus-1",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test",
		Channel: audit.ChannelConsole, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "the person called",
		PrincipalKind: "", JobID: "",
	}
}

func testClearNameBadEntry(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	add(t, c, key(authgate.ScopePassword, "n1", "a"), rule(1, longLock), nil)

	before := len(h.Entries(t))

	bad := clearEntry()
	bad.Namespace = otherScope

	if _, err := c.ClearName(t.Context(), clearScopes(), "n1", bad); !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("clear with an entry of a namespace = %v, want audit.ErrInvalidEntry", err)
	}

	// The lock holds: the clear changed nothing.
	add(t, c, key(authgate.ScopePassword, "n1", "a"), rule(1, longLock), authgate.ErrNameLocked)

	if got := len(h.Entries(t)); got != before {
		t.Fatalf("entries after a refused clear = %d, want %d", got, before)
	}
}

func testClearNameInvalid(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	add(t, c, key(authgate.ScopePassword, "n1", "a"), rule(1, longLock), nil)

	cases := []struct {
		name   string
		lock   string
		scopes []string
	}{
		{name: "no scope", lock: "n1", scopes: nil},
		{name: "a scope with a digit", lock: "n1", scopes: []string{authgate.ScopePassword, "pass1"}},
		{name: "a scope with a separator", lock: "n1", scopes: []string{"password|x"}},
		{name: "an empty name", lock: "", scopes: clearScopes()},
		{name: "a name with a line feed", lock: "n1\n", scopes: clearScopes()},
	}

	for _, tc := range cases {
		if _, err := c.ClearName(t.Context(), tc.scopes, tc.lock, clearEntry()); !errors.Is(
			err, authgate.ErrInvalidLock,
		) {
			t.Fatalf("%s: clear = %v, want ErrInvalidLock", tc.name, err)
		}
	}

	add(t, c, key(authgate.ScopePassword, "n1", "a"), rule(1, longLock), authgate.ErrNameLocked)
}

func testList(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	for _, sub := range []string{"c", "a", "b"} {
		add(t, c, key(authgate.ScopePassword, "n1", sub), rule(limitFive, longLock), nil)
	}

	locks, err := c.NameLocks(t.Context(), authgate.ScopePassword, "n1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(locks) != limitThree || locks[0].Sub != "a" || locks[1].Sub != "b" || locks[2].Sub != "c" {
		t.Fatalf("list = %+v, want a, b, c", locks)
	}

	none, err := c.NameLocks(t.Context(), authgate.ScopePassword, "nobody")
	if err != nil || len(none) != 0 {
		t.Fatalf("list of a name with no count = %+v, %v; want none", none, err)
	}
}

func testSubKeys(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	add(t, c, key(authgate.ScopePassword, "n1", "192.0.2.1"), rule(1, longLock), nil)

	got := add(t, c, key(authgate.ScopePassword, "n1", "198.51.100.1"), rule(limitTwo, longLock), nil)
	if got.Failures != 1 || !got.LockedUntil.IsZero() {
		t.Fatalf("other sub-key = %+v, want 1 failure and no lock", got)
	}
}

func testScopes(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	add(t, c, key(authgate.ScopePassword, "n1", ""), rule(1, longLock), nil)

	got := add(t, c, key(authgate.ScopePasswordName, "n1", ""), rule(limitTwo, longLock), nil)
	if got.Failures != 1 || !got.LockedUntil.IsZero() {
		t.Fatalf("other scope = %+v, want 1 failure and no lock", got)
	}
}

func testConcurrent(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	limit := limitFive

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		passed int
		other  error
	)

	for range concurrentAdds {
		wg.Go(func() {
			_, err := c.AddNameFailure(
				t.Context(),
				failure(key(authgate.ScopePassword, "n1", "s"), rule(limit, longLock)),
			)

			mu.Lock()
			defer mu.Unlock()

			switch {
			case err == nil:
				passed++
			case !errors.Is(err, authgate.ErrNameLocked):
				other = err
			default:
			}
		})
	}

	wg.Wait()

	if other != nil {
		t.Fatalf("a concurrent add failed: %v", other)
	}

	if passed != limit {
		t.Fatalf("passed adds = %d, want the limit %d", passed, limit)
	}
}

func testStoreClock(t *testing.T, h NameLockHarness) {
	t.Helper()

	before := time.Now()
	got := add(t, h.Open(t), key(authgate.ScopePassword, "n1", "s"), rule(1, longLock), nil)

	lockLength(t, before, got, longLock)
}

func testShared(t *testing.T, h NameLockHarness) {
	t.Helper()

	a, b := h.Open(t), h.Open(t)
	add(t, a, key(authgate.ScopePassword, "n1", "s"), rule(limitTwo, longLock), nil)
	add(t, b, key(authgate.ScopePassword, "n1", "s"), rule(limitTwo, longLock), nil)
	add(t, a, key(authgate.ScopePassword, "n1", "s"), rule(limitTwo, longLock), authgate.ErrNameLocked)
}

func testPrune(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	add(t, c, key(authgate.ScopePassword, "old", "s"), rule(limitFive, longLock), nil)
	add(t, c, key(authgate.ScopePassword, "locked", "s"), rule(1, longLock), nil)
	add(t, c, key(authgate.ScopePasswordName, "level", ""), doubling(shortLock, longLock, longLock), nil)

	h.Age(t, doubleLock)

	// A count with a failure inside the age stays.
	add(t, c, key(authgate.ScopePassword, "recent", "s"), rule(limitFive, longLock), nil)

	before := len(h.Entries(t))

	n, err := c.PruneNameLocks(
		t.Context(),
		shortLock,
		authgate.MaxNamePruneRows,
		authgate.MaxNamePruneEnded,
		pruneEntry(),
	)
	if err != nil || n != 1 {
		t.Fatalf("prune = %d, %v; want 1", n, err)
	}

	kept := []struct{ scope, name string }{
		{scope: authgate.ScopePassword, name: "locked"},
		{scope: authgate.ScopePasswordName, name: "level"},
		{scope: authgate.ScopePassword, name: "recent"},
	}

	for _, k := range kept {
		locks, listErr := c.NameLocks(t.Context(), k.scope, k.name)
		if listErr != nil || len(locks) != 1 {
			t.Fatalf("count %s after a prune = %+v, %v; want it kept", k.name, locks, listErr)
		}
	}

	gone, err := c.NameLocks(t.Context(), authgate.ScopePassword, "old")
	if err != nil || len(gone) != 0 {
		t.Fatalf("count old after a prune = %+v, %v; want it deleted", gone, err)
	}

	entries := h.Entries(t)
	if len(entries) != before+1 {
		t.Fatalf("entries after a prune = %d, want %d", len(entries), before+1)
	}

	if got := prunedCounts(entries); len(got) != 1 || got[0] != "1" {
		t.Fatalf("pruned counts of the entries of the prune = %v, want [1]", got)
	}
}

// pruneRows is the count of old counts of the case of the bound.
const pruneRows = 5

func testPruneBound(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	for i := range pruneRows {
		add(t, c, key(authgate.ScopePassword, "n"+strconv.Itoa(i), "s"), rule(limitFive, longLock), nil)
	}

	h.Age(t, doubleLock)

	n, err := c.PruneNameLocks(t.Context(), shortLock, limitTwo, limitTwo, pruneEntry())
	if err != nil || n != limitTwo {
		t.Fatalf("prune with a limit of 2 = %d, %v; want 2", n, err)
	}

	n, err = c.PruneNameLocks(
		t.Context(),
		shortLock,
		authgate.MaxNamePruneRows,
		authgate.MaxNamePruneEnded,
		pruneEntry(),
	)
	if err != nil || n != pruneRows-limitTwo {
		t.Fatalf("second prune = %d, %v; want %d", n, err, pruneRows-limitTwo)
	}

	got := prunedCounts(h.Entries(t))
	slices.Sort(got)

	if len(got) != limitTwo || got[0] != "2" || got[1] != "3" {
		t.Fatalf("pruned counts of the entries = %v, want one 2 and one 3", got)
	}
}

func testPruneInvalid(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	add(t, c, key(authgate.ScopePassword, "old", "s"), rule(limitFive, longLock), nil)

	h.Age(t, doubleLock)

	before := len(h.Entries(t))

	cases := []struct {
		name  string
		age   time.Duration
		limit int
		ended int
	}{
		{name: "an age of 0", age: 0, limit: 1, ended: 1},
		{name: "a negative age", age: -longLock, limit: 1, ended: 1},
		{name: "a limit of 0", age: shortLock, limit: 0, ended: 1},
		{name: "a negative limit", age: shortLock, limit: -1, ended: 1},
		{name: "a limit above the maximum", age: shortLock, limit: authgate.MaxNamePruneRows + 1, ended: 1},
		{name: "an ended limit of 0", age: shortLock, limit: 1, ended: 0},
		{name: "a negative ended limit", age: shortLock, limit: 1, ended: -1},
		{name: "an ended limit above the maximum", age: shortLock, limit: 1, ended: authgate.MaxNamePruneEnded + 1},
		{name: "an ended limit above the limit", age: shortLock, limit: 1, ended: limitTwo},
	}

	for _, tc := range cases {
		if _, err := c.PruneNameLocks(t.Context(), tc.age, tc.limit, tc.ended, pruneEntry()); !errors.Is(
			err, authgate.ErrInvalidLock,
		) {
			t.Fatalf("%s: prune = %v, want ErrInvalidLock", tc.name, err)
		}
	}

	locks, err := c.NameLocks(t.Context(), authgate.ScopePassword, "old")
	if err != nil || len(locks) != 1 {
		t.Fatalf("count after refused prunes = %+v, %v; want it kept", locks, err)
	}

	if got := len(h.Entries(t)); got != before {
		t.Fatalf("entries after refused prunes = %d, want %d", got, before)
	}
}

func testPruneBadEntry(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	add(t, c, key(authgate.ScopePassword, "old", "s"), rule(limitFive, longLock), nil)

	h.Age(t, doubleLock)

	before := len(h.Entries(t))

	bad := pruneEntry()
	bad.Namespace = otherScope

	if _, err := c.PruneNameLocks(t.Context(), shortLock, 1, 1, bad); !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("prune with an entry of a namespace = %v, want audit.ErrInvalidEntry", err)
	}

	locks, err := c.NameLocks(t.Context(), authgate.ScopePassword, "old")
	if err != nil || len(locks) != 1 {
		t.Fatalf("count after a refused prune = %+v, %v; want it kept", locks, err)
	}

	if got := len(h.Entries(t)); got != before {
		t.Fatalf("entries after a refused prune = %d, want %d", got, before)
	}
}

// pruneAction is the action of the entry of a prune in the suite.
const pruneAction = "sign-in.name-locks-pruned"

// pruneEntry returns a valid entry of the installation for a prune.
func pruneEntry() audit.Entry {
	e := clearEntry()
	e.Subject, e.Action, e.Object, e.SourceIP, e.UserAgent = serverSubject, pruneAction, "name-locks", "", ""
	e.Channel, e.PrincipalKind, e.Reason = audit.ChannelAPI, audit.PrincipalKindService, "counts past their age"

	return e
}

// prunedCounts returns the change "pruned" of each entry of a prune, in the
// order of the append.
func prunedCounts(entries []audit.Entry) []string {
	var out []string

	for i := range entries {
		if entries[i].Action != pruneAction {
			continue
		}

		for _, ch := range entries[i].Changes {
			if ch.Key == "pruned" {
				out = append(out, ch.New)
			}
		}
	}

	return out
}

func testBroken(t *testing.T, h NameLockHarness) {
	t.Helper()

	_, err := h.Broken(t).
		AddNameFailure(t.Context(), failure(key(authgate.ScopePassword, "n1", "s"), rule(1, longLock)))
	if err == nil || errors.Is(err, authgate.ErrNameLocked) {
		t.Fatalf("add on a broken store = %v, want an error that is not ErrNameLocked", err)
	}
}

func testInvalid(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	good := rule(1, longLock)

	cases := []struct {
		name, scope, key, sub string
		r                     authgate.LockRule
	}{
		{name: "upper case scope", scope: "Password", key: "n1", sub: "", r: good},
		{name: "digit in the scope", scope: "pass1", key: "n1", sub: "", r: good},
		{name: "empty scope", scope: "", key: "n1", sub: "", r: good},
		{name: "empty name", scope: authgate.ScopePassword, key: "", sub: "", r: good},
		{name: "name with a newline", scope: authgate.ScopePassword, key: "n\n1", sub: "", r: good},
		{
			name: "name too long", scope: authgate.ScopePassword, key: string(make([]byte, overLongName)), sub: "",
			r: good,
		},
		{name: "limit 0", scope: authgate.ScopePassword, key: "n1", sub: "", r: rule(0, longLock)},
		{name: "lock time 0", scope: authgate.ScopePassword, key: "n1", sub: "", r: rule(1, 0)},
		{
			name:  "maximum below the lock time",
			scope: authgate.ScopePassword,
			key:   "n1",
			sub:   "",
			r:     doubling(longLock, shortLock, 0),
		},
		{
			name:  "negative reset",
			scope: authgate.ScopePassword,
			key:   "n1",
			sub:   "",
			r:     doubling(shortLock, longLock, -time.Second),
		},
	}

	for i := range cases {
		tc := &cases[i]
		sub := tc.sub + strconv.Itoa(i)

		_, err := c.AddNameFailure(t.Context(), failure(key(tc.scope, tc.key, sub), tc.r))
		if !errors.Is(err, authgate.ErrInvalidLock) {
			t.Fatalf("%s: add = %v, want ErrInvalidLock", tc.name, err)
		}
	}

	if locks, err := c.NameLocks(t.Context(), authgate.ScopePassword, "n1"); err != nil || len(locks) != 0 {
		t.Fatalf("counts after refused adds = %+v, %v; want none", locks, err)
	}
}

func testExactName(t *testing.T, h NameLockHarness) {
	t.Helper()

	c := h.Open(t)
	add(t, c, key(authgate.ScopePassword, "digest-AbC", "s"), rule(1, longLock), nil)

	for _, other := range []string{"digest-abc", "DIGEST-ABC", "digest-AbC "} {
		if locks, err := c.NameLocks(t.Context(), authgate.ScopePassword, other); err != nil || len(locks) != 0 {
			t.Fatalf("list of %q = %+v, %v; want none", other, locks, err)
		}
	}

	if locks, err := c.NameLocks(t.Context(), authgate.ScopePassword, "digest-AbC"); err != nil || len(locks) != 1 {
		t.Fatalf("list of the exact name = %+v, %v; want one", locks, err)
	}
}
