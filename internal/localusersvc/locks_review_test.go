package localusersvc_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/blocklist"
)

// storeReach returns the reach config that reads the grants of the store of
// the fixture.
func (f *fixture) storeReach() access.ReachConfig {
	return access.ReachConfig{
		Grants: f.store, Roles: f.store, Callers: f.store, Principals: f.store, Collections: nil, Units: f.store,
	}
}

// lockedPerson makes the local user alice with the role of a site
// administrator, locks her name from lockedSource, and returns her principal
// ID with an administrator whose principal holds adminRole, or no grant for
// an empty role.
func (f *fixture) lockedPerson(t *testing.T, adminRole string) (string, localusersvc.Actor) {
	t.Helper()

	c := f.mustCreate(t, "alice", goodPassword)
	f.grantInstallation(t, c.Principal.ID, access.RoleSiteAdministrator, "grt-person")

	holder := f.mustCreate(t, "bob", otherPassword)
	if adminRole != "" {
		f.grantInstallation(t, holder.Principal.ID, adminRole, "grt-admin")
	}

	f.fail(t, authgate.DefaultNameLimit, "alice")

	return c.Principal.ID, localusersvc.Actor{Entry: base("local:bob"), PrincipalID: holder.Principal.ID, Admin: false}
}

// TestUnlockNeedsTheReachOfThePerson proves that an administrator whose
// reach does not cover the reach of the person can neither end the lock of
// the person nor read the counts, that each refusal is a denied audit entry,
// and that the lock holds after a refused unlock. An administrator with the
// same reach can do both.
func TestUnlockNeedsTheReachOfThePerson(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want      error
		name      string
		adminRole string
	}{
		{name: "an administrator with no grant", adminRole: "", want: localuser.ErrOutsideReach},
		{
			name: "an account administrator and a site administrator", adminRole: access.RoleAccountAdministrator,
			want: localuser.ErrOutsideReach,
		},
		{name: "two site administrators", adminRole: access.RoleSiteAdministrator, want: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			person, actor := f.lockedPerson(t, tc.adminRole)

			if _, err := f.svc.Locks(t.Context(), actor, person); !errors.Is(err, tc.want) {
				t.Fatalf("locks = %v, want %v", err, tc.want)
			}

			if _, err := f.svc.Unlock(t.Context(), actor, person, "the person called"); !errors.Is(err, tc.want) {
				t.Fatalf("unlock = %v, want %v", err, tc.want)
			}

			f.checkUnlockOutcome(t, tc.want)
		})
	}
}

// checkUnlockOutcome checks the denied entries of a read and an unlock that
// answered want, and that the lock holds after a refused unlock and ends
// after an unlock that passed.
func (f *fixture) checkUnlockOutcome(t *testing.T, want error) {
	t.Helper()

	for _, action := range []string{localusersvc.ActionNameLocksRead, localusersvc.ActionNameUnlocked} {
		if got := f.denied(t, action); got != deniedWant(want) {
			t.Fatalf("denied entries of %s = %d, want %d", action, got, deniedWant(want))
		}
	}

	_, err := f.svc.SignIn(t.Context(), from(lockedSource), "alice", goodPassword)

	switch {
	case want != nil && !errors.Is(err, localuser.ErrSignInFailed):
		t.Fatalf("sign in after a refused unlock = %v, want ErrSignInFailed", err)
	case want == nil && err != nil:
		t.Fatalf("sign in after the unlock: %v", err)
	default:
	}
}

// TestUnlockFailsClosedWithNoReach sends an unlock and a read to a service
// whose reach config cannot read the grants, as if the route had let any
// caller through: both fail closed with ErrOutsideReach and a denied entry,
// even for a person with no grant.
func TestUnlockFailsClosedWithNoReach(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	c := f.mustCreate(t, "alice", goodPassword)

	f.fail(t, authgate.DefaultNameLimit, "alice")

	svc := f.serviceWith(t, noReach())

	if _, err := svc.Locks(t.Context(), admin(), c.Principal.ID); !errors.Is(err, localuser.ErrOutsideReach) {
		t.Fatalf("locks with no reach = %v, want ErrOutsideReach", err)
	}

	if _, err := svc.Unlock(t.Context(), admin(), c.Principal.ID, "the person called"); !errors.Is(
		err, localuser.ErrOutsideReach,
	) {
		t.Fatalf("unlock with no reach = %v, want ErrOutsideReach", err)
	}

	if f.denied(t, localusersvc.ActionNameLocksRead) != 1 || f.denied(t, localusersvc.ActionNameUnlocked) != 1 {
		t.Fatal("want one denied entry of the read and one of the unlock")
	}

	// An actor with no principal ID fails closed too.
	anonymous := localusersvc.Actor{Entry: base("corp:admin"), PrincipalID: "", Admin: false}
	if _, err := f.svc.Unlock(t.Context(), anonymous, c.Principal.ID, "the person called"); !errors.Is(
		err, localuser.ErrOutsideReach,
	) {
		t.Fatalf("unlock with no actor = %v, want ErrOutsideReach", err)
	}
}

// TestGoodCurrentPasswordClearsTheCounts proves that only wrong current
// passwords in a row count: a good current password between two runs of
// wrong ones clears the counts, so neither run reaches the limit.
func TestGoodCurrentPasswordClearsTheCounts(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	c := f.mustCreate(t, "alice", goodPassword)
	me := localusersvc.Actor{Entry: base(c.Principal.Subject), PrincipalID: c.Principal.ID, Admin: false}

	wrong := func() {
		t.Helper()

		for range authgate.DefaultNameLimit - 1 {
			_, err := f.svc.ChangePassword(t.Context(), me, localusersvc.ChangeInput{
				Current: otherPassword, New: otherPassword, Generation: f.generation(t, c.Principal.ID), Fresh: false,
			})
			if !errors.Is(err, localuser.ErrWrongPassword) {
				t.Fatalf("wrong current password = %v, want ErrWrongPassword", err)
			}
		}
	}

	wrong()

	// A good current password: the change passes and clears the counts.
	good := localusersvc.ChangeInput{
		Current: goodPassword, New: "amber-falcon-river-62-signal", Generation: f.generation(t, c.Principal.ID),
		Fresh: false,
	}

	if _, err := f.svc.ChangePassword(t.Context(), me, good); err != nil {
		t.Fatalf("change with the good current password: %v", err)
	}

	locks, err := f.svc.Locks(t.Context(), admin(), c.Principal.ID)
	if err != nil {
		t.Fatalf("locks: %v", err)
	}

	for _, l := range locks {
		if l.Failures != 0 || !l.LockedUntil.IsZero() {
			t.Fatalf("count after a good current password = %+v, want 0 and no lock", l)
		}
	}

	wrong()

	if _, err := f.svc.SignIn(t.Context(), base(""), "alice", "amber-falcon-river-62-signal"); err != nil {
		t.Fatalf("sign in after two runs below the limit: %v", err)
	}
}

// demoFlag is a store that marks each credential that it reads as the demo
// person, as a row from a restore or a bug could.
type demoFlag struct {
	localusersvc.Stores
}

func (d demoFlag) UserByName(ctx context.Context, name string) (localuser.Credential, error) {
	cred, err := d.Stores.UserByName(ctx, name)
	if err != nil {
		return cred, fmt.Errorf("read the user %s: %w", name, err)
	}

	cred.User.Demo = true

	return cred, nil
}

// demoService returns a service of the kind over the store of f that reads
// each credential as the demo person.
func (f *fixture) demoService(t *testing.T, kind installation.Kind) *localusersvc.Service {
	t.Helper()

	svc, err := localusersvc.New(localusersvc.Config{
		Log:   nil,
		Store: demoFlag{Stores: f.store}, Principals: f.principals, Hasher: f.hasher, Blocklist: blocklist.Embedded(),
		Audit: f.store, Locks: f.store, Keys: f.store, Settings: f.settings,
		SSO: func(context.Context) (bool, error) { return false, nil }, Reach: f.storeReach(), Kind: kind, Now: nil,
		Subjects: f.store,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// TestDemoExceptionNeedsAKindDev sends a credential with the demo flag
// directly to the count of the sign-in, as if the store had kept a demo row
// on an installation of another kind. Only a dev installation skips the
// counts: on a production installation the name locks at the limit.
func TestDemoExceptionNeedsAKindDev(t *testing.T) {
	t.Parallel()

	cases := []struct {
		kind       installation.Kind
		name       string
		wantLocked bool
	}{
		{name: "a production installation counts the demo flag", kind: installation.KindProduction, wantLocked: true},
		{name: "an installation with no kind counts the demo flag", kind: "", wantLocked: true},
		{name: "a dev installation skips the counts", kind: installation.KindDev, wantLocked: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			c := f.mustCreate(t, "alice", goodPassword)
			svc := f.demoService(t, tc.kind)

			for range authgate.DefaultNameLimit + 1 {
				if _, err := svc.SignIn(t.Context(), from(lockedSource), "alice", otherPassword); err == nil {
					t.Fatal("a wrong password signed in")
				}
			}

			locked := f.hasLock(t, c.Principal.ID)
			if locked != tc.wantLocked || (f.reasons(t)[localusersvc.ReasonNameLocked] == 1) != tc.wantLocked {
				t.Fatalf("locked = %v, reasons = %v; want locked %v", locked, f.reasons(t), tc.wantLocked)
			}
		})
	}
}

// hasLock reports whether a count of the name of the person principalID
// holds a lock.
func (f *fixture) hasLock(t *testing.T, principalID string) bool {
	t.Helper()

	locks, err := f.svc.Locks(t.Context(), admin(), principalID)
	if err != nil {
		t.Fatalf("locks: %v", err)
	}

	for _, l := range locks {
		if !l.LockedUntil.IsZero() {
			return true
		}
	}

	return false
}

// recentCounts is the count of names with a recent failure in the case of the
// prune.
const recentCounts = 3

// TestPruneLocksKeepsRecentCountsWithItsRecord proves that one run of the
// prune of the server keeps the counts inside the prune age, that it runs
// with the age and the bound that the API shows, and that the store appends
// the entry of the run: the server as the subject, the reason and the count.
// The suite of the stores proves the delete of old counts and the bound.
func TestPruneLocksKeepsRecentCountsWithItsRecord(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.mustCreate(t, "alice", goodPassword)

	for i := range recentCounts {
		f.fail(t, 1, "recent"+strconv.Itoa(i))
	}

	f.fail(t, 1, "alice")

	res, err := localusersvc.PruneLocks(t.Context(), f.store, newFakePruneClock().clock())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	p := localusersvc.Prune()
	if res.Deleted != 0 || res.MaxRows != p.MaxRows || res.AgeSeconds != int64(p.Age/time.Second) {
		t.Fatalf("prune of recent counts = %+v, want 0 deleted with the age and the bound of %+v", res, p)
	}

	entries := f.entriesWith(t, localusersvc.ActionNameLocksPruned)
	if len(entries) != 1 {
		t.Fatalf("entries of the prune = %d, want 1", len(entries))
	}

	checkPruneEntry(t, &entries[0])

	if p.Interval != time.Hour || p.MaxRows != authgate.MaxNamePruneRows ||
		p.Age != authgate.DefaultNameGlobalMaxLockFor+authgate.DefaultNameGlobalResetAfter {
		t.Fatalf("prune settings = %+v, want every hour, the longest lock plus the reset, and the bound", p)
	}
}

// checkPruneEntry checks that e is the entry of a run of the prune that
// deleted nothing: the server as the subject, the reason, the object and the
// changes pruned 0 and locksEnded 0.
func checkPruneEntry(t *testing.T, e *audit.Entry) {
	t.Helper()

	if e.Subject != history.ServerSubject || e.PrincipalKind != audit.PrincipalKindService ||
		e.Reason != localusersvc.ReasonNameLocksPruned || e.Object != localusersvc.NameLocksObject {
		t.Fatalf("entry of the prune = %+v, want the server, the reason and the object", e)
	}

	got := map[string]string{}
	for _, ch := range e.Changes {
		got[ch.Key] = ch.New
	}

	if len(e.Changes) != 2 || got["pruned"] != "0" || got["locksEnded"] != "0" {
		t.Fatalf("changes of the entry of the prune = %+v, want pruned 0 and locksEnded 0", e.Changes)
	}
}

// failingPrune is a counter whose prune fails, as a store that is down does.
type failingPrune struct {
	authgate.NameLockCounter
}

func (failingPrune) PruneNameLocks(context.Context, time.Duration, int, int, audit.Entry) (int64, error) {
	return 0, errDiskOnFire
}

// TestPruneLocksFailsClosed proves that a prune with no counter, or whose
// store fails, returns an error so that the runner records a failed run.
func TestPruneLocksFailsClosed(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := localusersvc.PruneLocks(t.Context(), nil, newFakePruneClock().clock()); !errors.Is(
		err, localusersvc.ErrMissingDependency,
	) {
		t.Fatalf("prune with no counter = %v, want ErrMissingDependency", err)
	}

	_, err := localusersvc.PruneLocks(t.Context(), failingPrune{NameLockCounter: f.store}, newFakePruneClock().clock())
	if !errors.Is(err, errDiskOnFire) {
		t.Fatalf("prune with a failing store = %v, want its error", err)
	}
}

// TestUnlockByAnAccessToken sends an unlock with no console session, as an
// access token does, directly to the reach check: the principal of the
// subject of the token must cover the person, an unknown subject or a
// service with no resolver of subjects fails closed, and an administrator of
// the identity layer covers each person.
func TestUnlockByAnAccessToken(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want     error
		name     string
		subject  string
		role     string
		admin    bool
		resolver bool
	}{
		{
			name: "a site administrator by its subject", subject: "", role: access.RoleSiteAdministrator,
			admin: false, resolver: true, want: nil,
		},
		{
			name: "an account administrator by its subject", subject: "", role: access.RoleAccountAdministrator,
			admin: false, resolver: true, want: localuser.ErrOutsideReach,
		},
		{
			name: "an unknown subject", subject: "corp:nobody", role: "", admin: false, resolver: true,
			want: localuser.ErrOutsideReach,
		},
		{
			name: "a service with no resolver of subjects", subject: "", role: access.RoleSiteAdministrator,
			admin: false, resolver: false, want: localuser.ErrOutsideReach,
		},
		{
			name: "an administrator of the identity layer", subject: "corp:root", role: "", admin: true,
			resolver: true, want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			person, holder := f.lockedPerson(t, tc.role)

			subject := tc.subject
			if subject == "" {
				subject = f.subjectOf(t, holder.PrincipalID)
			}

			svc := f.svc
			if !tc.resolver {
				svc = f.serviceWithSubjects(t, nil)
			}

			token := localusersvc.Actor{Entry: base(subject), PrincipalID: "", Admin: tc.admin}

			if _, err := svc.Unlock(t.Context(), token, person, "the person called"); !errors.Is(err, tc.want) {
				t.Fatalf("unlock = %v, want %v", err, tc.want)
			}
		})
	}
}

// subjectOf returns the subject of the principal id.
func (f *fixture) subjectOf(t *testing.T, id string) string {
	t.Helper()

	p, err := f.principals.GetPrincipal(t.Context(), id)
	if err != nil {
		t.Fatalf("get principal %s: %v", id, err)
	}

	return p.Subject
}

// serviceWithSubjects returns a service over the store of f that finds the
// principal of a subject with subjects.
func (f *fixture) serviceWithSubjects(t *testing.T, subjects localusersvc.Subjects) *localusersvc.Service {
	t.Helper()

	svc, err := localusersvc.New(localusersvc.Config{
		Log:   nil,
		Store: f.store, Principals: f.principals, Hasher: f.hasher, Blocklist: blocklist.Embedded(),
		Audit: f.store, Locks: f.store, Keys: f.store, Settings: f.settings,
		SSO: func(context.Context) (bool, error) { return false, nil }, Reach: f.storeReach(),
		Kind: installation.KindProduction, Now: nil, Subjects: subjects,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// batchPrune is a counter whose prune returns a scripted count for each
// batch, then 0, and counts the batches. after runs after each batch.
type batchPrune struct {
	authgate.NameLockCounter

	after   func(calls int)
	batches []int64
	calls   int
}

func (b *batchPrune) PruneNameLocks(context.Context, time.Duration, int, int, audit.Entry) (int64, error) {
	b.calls++
	if b.after != nil {
		defer b.after(b.calls)
	}

	if b.calls > len(b.batches) {
		return 0, nil
	}

	return b.batches[b.calls-1], nil
}

// TestPruneLocksRunsAgainWhileBatchesAreFull proves that a run keeps
// deleting while a batch deletes its full limit, stops at the first batch
// below the limit, and returns the sum. A run that stops after one batch
// fails this test.
func TestPruneLocksRunsAgainWhileBatchesAreFull(t *testing.T) {
	t.Parallel()

	full := int64(authgate.MaxNamePruneRows)
	b := &batchPrune{NameLockCounter: nil, after: nil, calls: 0, batches: []int64{full, full, full, 7, full}}

	res, err := localusersvc.PruneLocks(t.Context(), b, newFakePruneClock().clock())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	if b.calls != 4 || res.Deleted != 3*full+7 {
		t.Fatalf("batches = %d, deleted = %d, want 4 batches and %d deleted", b.calls, res.Deleted, 3*full+7)
	}
}

// TestPruneLocksStopsWhenItsContextEnds proves that a run with only full
// batches stops when its context ends, so that it cannot run without bound.
// A run that ignores the context fails this test.
func TestPruneLocksStopsWhenItsContextEnds(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	full := int64(authgate.MaxNamePruneRows)
	b := &batchPrune{NameLockCounter: nil, calls: 0, batches: make([]int64, 1000), after: func(calls int) {
		if calls == 3 {
			cancel()
		}
	}}

	for i := range b.batches {
		b.batches[i] = full
	}

	res, err := localusersvc.PruneLocks(ctx, b, newFakePruneClock().clock())
	if !errors.Is(err, context.Canceled) || res.StopReason != localusersvc.PruneStopContext {
		t.Fatalf("prune = %+v, %v, want the error of the context and the stop reason %q",
			res, err, localusersvc.PruneStopContext)
	}

	if b.calls != 3 || res.Deleted != 3*full {
		t.Fatalf("batches = %d, deleted = %d, want 3 batches and %d deleted", b.calls, res.Deleted, 3*full)
	}
}

// TestPruneLocksKeepsTheCountOfBatchesBeforeAnError proves that a batch that
// fails after full batches returns the error and the count that the earlier
// batches deleted.
func TestPruneLocksKeepsTheCountOfBatchesBeforeAnError(t *testing.T) {
	t.Parallel()

	full := int64(authgate.MaxNamePruneRows)
	b := &errAfterPrune{
		batchPrune{NameLockCounter: nil, after: nil, calls: 0, batches: []int64{full, full}},
	}

	res, err := localusersvc.PruneLocks(t.Context(), b, newFakePruneClock().clock())
	if !errors.Is(err, errDiskOnFire) || res.Deleted != 2*full {
		t.Fatalf("prune = %+v, %v, want %d deleted and the error of the store", res, err, 2*full)
	}
}

// errAfterPrune fails the batch after its scripted batches.
type errAfterPrune struct {
	batchPrune
}

func (b *errAfterPrune) PruneNameLocks(ctx context.Context, a time.Duration, l, e int, en audit.Entry) (int64, error) {
	if b.calls >= len(b.batches) {
		return 0, errDiskOnFire
	}

	return b.batchPrune.PruneNameLocks(ctx, a, l, e, en)
}

// fakePruneClock is a clock that moves only when a run pauses: each pause
// adds its time. It records the pauses.
type fakePruneClock struct {
	now    time.Time
	pauses []time.Duration
	// step is added to the time at each call of Now, to model a slow batch.
	step time.Duration
}

func newFakePruneClock() *fakePruneClock {
	return &fakePruneClock{now: time.Unix(1_700_000_000, 0), pauses: nil, step: 0}
}

func (c *fakePruneClock) clock() localusersvc.PruneClock {
	return localusersvc.PruneClock{
		Now: func() time.Time {
			c.now = c.now.Add(c.step)

			return c.now
		},
		Pause: func(ctx context.Context, d time.Duration) bool {
			if ctx.Err() != nil {
				return false
			}

			c.pauses = append(c.pauses, d)
			c.now = c.now.Add(d)

			return true
		},
	}
}

// deadlineProbe is a counter that fails a batch whose context has a deadline,
// as a store does when the deadline ends inside a transaction.
type deadlineProbe struct {
	batchPrune
}

func (b *deadlineProbe) PruneNameLocks(ctx context.Context, a time.Duration, l, e int, en audit.Entry) (int64, error) {
	if _, ok := ctx.Deadline(); ok {
		return 0, context.DeadlineExceeded
	}

	return b.batchPrune.PruneNameLocks(ctx, a, l, e, en)
}

// TestPruneLocksStopsAtTheTimeBoundWithNoError proves that a run with only
// full batches stops when the injected clock passes NamePruneMaxRun, returns
// no error, reports the stop reason and the batches, and pauses between
// batches. A run with no time bound never stops here, and a run that gives
// the bound to the batch as a context deadline returns an error.
func TestPruneLocksStopsAtTheTimeBoundWithNoError(t *testing.T) {
	t.Parallel()

	full := int64(authgate.MaxNamePruneRows)
	c := newFakePruneClock()
	c.step = time.Minute // each read of the clock: one minute passes

	// The batches are many more than the bound allows.
	b := &deadlineProbe{batchPrune{NameLockCounter: nil, after: nil, calls: 0, batches: make([]int64, 100)}}
	for i := range b.batches {
		b.batches[i] = full
	}

	res, err := localusersvc.PruneLocks(t.Context(), b, c.clock())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	if res.StopReason != localusersvc.PruneStopTimeBound {
		t.Fatalf("stop reason = %q, want %q", res.StopReason, localusersvc.PruneStopTimeBound)
	}

	if res.Batches != b.calls || res.Batches < 2 || res.Batches >= 100 || res.Deleted != int64(res.Batches)*full {
		t.Fatalf("batches = %d (calls %d), deleted = %d", res.Batches, b.calls, res.Deleted)
	}

	if len(c.pauses) != res.Batches || c.pauses[0] != authgate.NamePruneBatchPause {
		t.Fatalf("pauses = %v, want one pause of %s after each batch", c.pauses, authgate.NamePruneBatchPause)
	}
}

// TestPruneLocksStopReasons proves the stop reason of a short batch, of a
// context that ends in a pause, and of a batch error.
func TestPruneLocksStopReasons(t *testing.T) {
	t.Parallel()

	full := int64(authgate.MaxNamePruneRows)

	b := &batchPrune{NameLockCounter: nil, after: nil, calls: 0, batches: []int64{full, 3}}
	if res, err := localusersvc.PruneLocks(t.Context(), b, newFakePruneClock().clock()); err != nil ||
		res.StopReason != localusersvc.PruneStopShortBatch || res.Batches != 2 {
		t.Fatalf("short batch: %+v, %v", res, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	b = &batchPrune{NameLockCounter: nil, after: nil, calls: 0, batches: []int64{full, full}}
	if res, err := localusersvc.PruneLocks(ctx, b, newFakePruneClock().clock()); !errors.Is(err, context.Canceled) ||
		res.StopReason != localusersvc.PruneStopContext || res.Batches != 1 {
		t.Fatalf("context: %+v, %v", res, err)
	}

	e := &errAfterPrune{batchPrune{NameLockCounter: nil, after: nil, calls: 0, batches: []int64{full}}}
	if res, err := localusersvc.PruneLocks(t.Context(), e, newFakePruneClock().clock()); err == nil ||
		res.StopReason != localusersvc.PruneStopError || res.Batches != 1 || res.Deleted != full {
		t.Fatalf("error: %+v, %v", res, err)
	}
}

// TestPruneLocksChecksItsContextAfterThePause proves that a run stops when its
// context ends, also when the pause of the clock ignores the context and
// always reports a full pause. The run does not rely on the clock. A run
// with no check of the context after the pause fails this test.
func TestPruneLocksChecksItsContextAfterThePause(t *testing.T) {
	t.Parallel()

	const stopAfter = 4

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	full := int64(authgate.MaxNamePruneRows)
	b := &batchPrune{NameLockCounter: nil, calls: 0, batches: make([]int64, 1000), after: func(calls int) {
		if calls == stopAfter {
			cancel()
		}
	}}

	for i := range b.batches {
		b.batches[i] = full
	}

	clock := localusersvc.PruneClock{
		Now:   func() time.Time { return time.Unix(1_700_000_000, 0) },
		Pause: func(context.Context, time.Duration) bool { return true },
	}

	res, err := localusersvc.PruneLocks(ctx, b, clock)
	if !errors.Is(err, context.Canceled) || res.StopReason != localusersvc.PruneStopContext ||
		res.Batches != stopAfter || b.calls != stopAfter {
		t.Fatalf("prune = %+v, %v after %d calls, want %d batches, the stop reason %q and the error of the context",
			res, err, b.calls, stopAfter, localusersvc.PruneStopContext)
	}
}

// TestPruneLocksNeedsAClock proves that a run with no clock fails closed.
func TestPruneLocksNeedsAClock(t *testing.T) {
	t.Parallel()

	b := &batchPrune{NameLockCounter: nil, after: nil, calls: 0, batches: nil}
	if _, err := localusersvc.PruneLocks(t.Context(), b, localusersvc.PruneClock{Now: nil, Pause: nil}); !errors.Is(
		err, localusersvc.ErrMissingDependency,
	) {
		t.Fatalf("prune with no clock = %v, want ErrMissingDependency", err)
	}

	if b.calls != 0 {
		t.Fatalf("batches = %d, want 0", b.calls)
	}
}
