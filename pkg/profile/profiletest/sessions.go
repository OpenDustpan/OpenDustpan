package profiletest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// RunSessions runs every case of the suite of profile.Sessions against the
// stores that newStore returns. Run calls it. A Store with no Sessions skips
// each case. The Store that newStore returns holds the fields Sessions,
// Profiles, Namespaces, Audit and Records.
func RunSessions(t *testing.T, newStore Factory) {
	t.Helper()

	withSessions := func(t *testing.T) Store {
		t.Helper()

		s := newStore(t)
		if s.Sessions == nil {
			t.Skip("the store has no open sessions")
		}

		return s
	}

	cases := []struct {
		name string
		test func(t *testing.T, s Store)
	}{
		{"a session is stored and read back whole", testSessionRoundTrip},
		{"a read of another namespace is not found", testSessionOtherNamespace},
		{"a read of another owner is not found", testSessionOtherOwner},
		{"a read at or after the end is not found", testSessionEnded},
		{"a read with an ID that is not a session ID is not found", testSessionForgedID},
		{"the 101st live session of one owner is refused and leaves no row", testSessionLimit},
		{"an ended session does not count for the limit", testSessionLimitEnded},
		{"the limit is for each owner", testSessionLimitOwners},
		{"101 concurrent puts of one owner leave 100", testSessionLimitConcurrent},
		{"a stale fence refuses the put, with no row and no entry", testSessionFenced},
		{"a put that is not valid changes nothing", testSessionInvalidPut},
		{"a put with an ID in use is a conflict and leaves one row", testSessionConflict},
		{"a put of a root profile that is not live is not found", testSessionRootNotLive},
		{"a failed audit write leaves no session", testSessionAuditRollsBack},
		{"a put appends every entry in order", testSessionAuditMany},
		{"a put of a session of another namespace than the fence is refused", testSessionPutOtherFence},
		{"delete of another owner leaves the row", testSessionDeleteOtherOwner},
		{"delete with an owner that cannot own a session is not found", testSessionDeleteInvalidOwner},
		{"a stale fence refuses the delete, with the row and no entry", testSessionDeleteFenced},
		{"delete writes its entry and a session that is not found writes none", testSessionDeleteAudit},
		{"delete removes the session of its owner, ended or not", testSessionDelete},
		{"sweep with a limit below 1 is invalid", testSessionSweepInvalid},
		{"sweep deletes the ended sessions and keeps the live ones", testSessionSweep},
		{"sweep deletes at most the limit", testSessionSweepLimit},
		{"a put stores the answer of its request in its transaction", testSessionCompletionStored},
		{"a failed put leaves the record of its request pending", testSessionCompletionKeptPending},
		{"a put whose record is gone changes nothing", testSessionCompletionLost},
	}

	runCases(t, withSessions, cases)
}

// firstSessionID is the ID of the session that most cases make.
const firstSessionID = "pos-one"

// maxOwnerSessions is profile.MaxLiveSessions for the cases.
const maxOwnerSessions = profile.MaxLiveSessions

// Times that the cases read at, after the end of a session of one minute or
// one hour.
const (
	afterEnd        = 2 * time.Minute
	longAfter       = 24 * time.Hour
	halfMillisecond = 500 * time.Microsecond
)

// openSessionFixture is a namespace with a root profile and the start of its
// sessions.
type openSessionFixture struct {
	ns   namespace.Namespace
	root profile.Profile
}

func newOpenFixture(t *testing.T, s Store) openSessionFixture {
	t.Helper()

	return newOpenFixtureIn(t, s, "acme")
}

func newOpenFixtureIn(t *testing.T, s Store, name string) openSessionFixture {
	t.Helper()

	ns := newNamespace(t, s, name)

	return openSessionFixture{ns: ns, root: create(t, s, ns, "payments/aws-prod")}
}

// SeedOpenSession makes the namespace name with a profile and one live open
// session of that profile, through the fields Namespaces, Profiles and
// Sessions of s, and returns the session ID. The restore suite of the stores
// uses it.
func SeedOpenSession(t *testing.T, s Store, name string) string {
	t.Helper()

	f := newOpenFixtureIn(t, s, name)
	sess := f.session(t, profile.NewSessionID(), subject)
	f.put(t, s, sess)

	return sess.ID()
}

// SeedOwner returns the owner of the session that SeedOpenSession makes. The
// session ends one hour after the time that SeedNow returns.
func SeedOwner() profile.SessionOwner { return profile.PrincipalOwner(subject) }

// SeedNow returns the time at which the session of SeedOpenSession starts.
func SeedNow() time.Time { return epoch() }

// HasOpenSession reports whether the namespace name holds the live open
// session id of the subject of SeedOpenSession, read through the field
// Sessions of s.
func HasOpenSession(t *testing.T, s Store, name, id string) bool {
	t.Helper()

	_, err := s.Sessions.Get(t.Context(), namespace.Name(name), id, profile.PrincipalOwner(subject), epoch())
	if err != nil && !errors.Is(err, profile.ErrNotFound) {
		t.Fatalf("Get(%s): %v", id, err)
	}

	return err == nil
}

// sessionAt makes a session of owner that starts at start and lasts d.
func (f openSessionFixture) sessionAt(
	t *testing.T, id, owner string, start time.Time, d time.Duration,
) profile.Session {
	t.Helper()

	return makeSession(t, sessionSpec{
		ns: f.ns.Name, root: f.root.ID, id: id, owner: owner, start: start, length: d,
	})
}

// sessionSpec is what makeSession needs to make a session.
type sessionSpec struct {
	ns     namespace.Name
	root   profile.ID
	id     string
	owner  string
	start  time.Time
	length time.Duration
}

func makeSession(t *testing.T, spec sessionSpec) profile.Session {
	t.Helper()

	sess, err := profile.NewSession(profile.SessionParams{
		ID: spec.id, Namespace: spec.ns, Root: spec.root, Owner: profile.PrincipalOwner(spec.owner),
		Consumer: profile.OpenConsumerAPI, Paths: []string{"db.password", "region"},
		Sealed: []byte("sealed:" + spec.id), CreatedAt: spec.start, ExpiresAt: spec.start.Add(spec.length),
	})
	if err != nil {
		t.Fatalf("NewSession(%q): %v", spec.id, err)
	}

	return sess
}

// session makes a live session of owner at the epoch.
func (f openSessionFixture) session(t *testing.T, id, owner string) profile.Session {
	t.Helper()

	return f.sessionAt(t, id, owner, epoch(), time.Hour)
}

func openEntries(ns namespace.Name) []audit.Entry {
	e := entry(ns, "profile:open")
	e.Kind = audit.KindAccess

	return []audit.Entry{e}
}

func (f openSessionFixture) put(t *testing.T, s Store, sess profile.Session) {
	t.Helper()

	if err := s.Sessions.Put(t.Context(), sess, f.ns.Fence(), openEntries(f.ns.Name)); err != nil {
		t.Fatalf("Put(%s): %v", sess.ID(), err)
	}
}

// delete removes the session id of owner under the fence of the namespace of
// f, with one audit entry.
func (f openSessionFixture) delete(s Store, id, owner string) error {
	return s.Sessions.Delete(context.Background(), id, profile.PrincipalOwner(owner), f.ns.Fence(),
		openEntries(f.ns.Name))
}

func (f openSessionFixture) get(s Store, id, owner string, now time.Time) (profile.Session, error) {
	return s.Sessions.Get(context.Background(), f.ns.Name, id, profile.PrincipalOwner(owner), now)
}

// wantFound checks that the live session id of owner is in the store.
func (f openSessionFixture) wantFound(t *testing.T, s Store, id, owner string) {
	t.Helper()

	if _, err := f.get(s, id, owner, epoch()); err != nil {
		t.Errorf("session %s of %s: Get = %v, want the session", id, owner, err)
	}
}

// wantGone checks that the store holds no live session id of owner.
func (f openSessionFixture) wantGone(t *testing.T, s Store, id, owner string) {
	t.Helper()

	if _, err := f.get(s, id, owner, epoch()); !errors.Is(err, profile.ErrNotFound) {
		t.Errorf("session %s of %s: Get error = %v, want ErrNotFound", id, owner, err)
	}
}

func auditCount(t *testing.T, s Store, ns namespace.Name) int {
	t.Helper()

	list, err := s.Audit.List(t.Context(), ns, audit.Page{Size: bigPage * bigPage, Before: 0})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	return len(list.Entries)
}

func testSessionRoundTrip(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	before := auditCount(t, s, f.ns.Name)
	sess := f.session(t, firstSessionID, subject)
	f.put(t, s, sess)

	got, err := f.get(s, firstSessionID, subject, epoch())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ID() != firstSessionID || got.Namespace() != f.ns.Name || got.Root() != f.root.ID ||
		got.Owner() != profile.PrincipalOwner(subject) || got.Consumer() != profile.OpenConsumerAPI ||
		!slices.Equal(got.Paths(), []string{"db.password", "region"}) ||
		string(got.Sealed()) != "sealed:"+firstSessionID || !got.CreatedAt().Equal(epoch()) ||
		!got.ExpiresAt().Equal(epoch().Add(time.Hour)) {
		t.Errorf("Get = %+v, want the session that Put stored", got)
	}

	if n := auditCount(t, s, f.ns.Name); n != before+1 {
		t.Errorf("audit entries after the put = %d, want %d", n, before+1)
	}
}

func testSessionOtherNamespace(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	f.put(t, s, f.session(t, firstSessionID, subject))

	other := newNamespace(t, s, "globex")

	_, err := s.Sessions.Get(t.Context(), other.Name, firstSessionID, profile.PrincipalOwner(subject), epoch())
	wantErr(t, "Get in another namespace", err, profile.ErrNotFound)

	err = s.Sessions.Delete(t.Context(), firstSessionID, profile.PrincipalOwner(subject), other.Fence(),
		openEntries(other.Name))
	wantErr(t, "Delete in another namespace", err, profile.ErrNotFound)

	f.wantFound(t, s, firstSessionID, subject)
}

func testSessionOtherOwner(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	f.put(t, s, f.session(t, firstSessionID, subject))

	for _, owner := range []string{otherSubject, "alic", "alice ", "Alice", "alice%", "%"} {
		_, err := f.get(s, firstSessionID, owner, epoch())
		wantErr(t, fmt.Sprintf("Get of owner %q", owner), err, profile.ErrNotFound)
	}

	// The kind of the owner is part of the key.
	_, err := s.Sessions.Get(t.Context(), f.ns.Name, firstSessionID,
		profile.SessionOwner{Kind: "job", ID: subject}, epoch())
	wantErr(t, "Get with the owner kind job", err, profile.ErrNotFound)
}

func testSessionEnded(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	f.put(t, s, f.session(t, firstSessionID, subject))

	end := epoch().Add(time.Hour)

	for _, before := range []time.Duration{time.Millisecond, halfMillisecond} {
		if _, err := f.get(s, firstSessionID, subject, end.Add(-before)); err != nil {
			t.Errorf("Get %s before the end: %v, want the session", before, err)
		}
	}

	for _, now := range []time.Time{end, end.Add(time.Millisecond), end.Add(longAfter)} {
		_, err := f.get(s, firstSessionID, subject, now)
		wantErr(t, "Get at "+now.Format(time.RFC3339Nano), err, profile.ErrNotFound)
	}
}

func testSessionForgedID(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	f.put(t, s, f.session(t, firstSessionID, subject))

	for _, id := range []string{
		"", "one", "pos-", "pos-one ", "POS-one", "xpos-one", "pos-one' OR '1'='1", "pos-%", "pos-_ne", "pos-one\x00",
		"pos-one\n", "../pos-one", "pos-one/../pos-one",
	} {
		_, err := f.get(s, id, subject, epoch())
		wantErr(t, fmt.Sprintf("Get of %q", id), err, profile.ErrNotFound)

		err = f.delete(s, id, subject)
		wantErr(t, fmt.Sprintf("Delete of %q", id), err, profile.ErrNotFound)
	}

	f.wantFound(t, s, firstSessionID, subject)
}

// fillOwner stores n live sessions of owner, with the IDs pos-<prefix><n>.
func (f openSessionFixture) fillOwner(t *testing.T, s Store, owner, prefix string, n int) {
	t.Helper()

	for i := range n {
		f.put(t, s, f.session(t, fmt.Sprintf("pos-%s%d", prefix, i), owner))
	}
}

func testSessionLimit(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	f.fillOwner(t, s, subject, "a", maxOwnerSessions)

	before := auditCount(t, s, f.ns.Name)
	err := s.Sessions.Put(t.Context(), f.session(t, "pos-over", subject), f.ns.Fence(), openEntries(f.ns.Name))
	wantErr(t, "the 101st put", err, profile.ErrSessionLimit)
	wantErr(t, "the 101st put wraps ErrLimit", err, profile.ErrLimit)

	f.wantGone(t, s, "pos-over", subject)

	if after := auditCount(t, s, f.ns.Name); after != before {
		t.Errorf("audit entries after the refused put = %d, want %d", after, before)
	}
}

func testSessionLimitEnded(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)

	for i := range maxOwnerSessions {
		f.put(t, s, f.sessionAt(t, fmt.Sprintf("pos-old%d", i), subject, epoch(), time.Minute))
	}

	// All 100 ended before the start of the new session.
	later := epoch().Add(afterEnd)
	f.put(t, s, f.sessionAt(t, "pos-new", subject, later, time.Hour))

	// A session that ends exactly at the start of the new one does not count.
	if _, err := f.get(s, "pos-new", subject, later); err != nil {
		t.Errorf("Get of the new session: %v", err)
	}
}

func testSessionLimitOwners(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	f.fillOwner(t, s, subject, "a", maxOwnerSessions)
	f.put(t, s, f.session(t, "pos-bob", otherSubject))

	f.wantFound(t, s, "pos-bob", otherSubject)
}

func testSessionLimitConcurrent(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	attempts := maxOwnerSessions + 1
	sessions := make([]profile.Session, attempts)

	for i := range attempts {
		sessions[i] = f.session(t, fmt.Sprintf("pos-c%d", i), subject)
	}

	var wg sync.WaitGroup

	errs := make([]error, attempts)

	for i := range attempts {
		wg.Go(func() {
			errs[i] = s.Sessions.Put(t.Context(), sessions[i], f.ns.Fence(), openEntries(f.ns.Name))
		})
	}

	wg.Wait()

	refused := 0

	for _, err := range errs {
		if err != nil {
			wantErr(t, "a concurrent put", err, profile.ErrSessionLimit)

			refused++
		}
	}

	live := 0

	for i := range attempts {
		if _, err := f.get(s, fmt.Sprintf("pos-c%d", i), subject, epoch()); err == nil {
			live++
		}
	}

	if live != maxOwnerSessions || refused != 1 {
		t.Errorf("after %d concurrent puts: live = %d, refused = %d, want %d and 1",
			attempts, live, refused, maxOwnerSessions)
	}
}

func testSessionFenced(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	before := auditCount(t, s, f.ns.Name)

	stale := f.ns.Fence()
	stale.FailoverVersion = f.ns.FailoverVersion - 1

	err := s.Sessions.Put(t.Context(), f.session(t, firstSessionID, subject), stale, openEntries(f.ns.Name))
	wantErr(t, "Put with a stale fence", err, namespace.ErrFenced)

	f.wantGone(t, s, firstSessionID, subject)

	if after := auditCount(t, s, f.ns.Name); after != before {
		t.Errorf("audit entries after the fenced put = %d, want %d", after, before)
	}
}

func testSessionInvalidPut(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	before := auditCount(t, s, f.ns.Name)

	err := s.Sessions.Put(t.Context(), profile.Session{}, f.ns.Fence(), openEntries(f.ns.Name))
	wantErr(t, "Put of the zero session", err, profile.ErrInvalid)

	err = s.Sessions.Put(t.Context(), f.session(t, firstSessionID, subject), f.ns.Fence(), nil)
	wantErr(t, "Put with no entry", err, profile.ErrInvalid)

	err = s.Sessions.Put(t.Context(), f.session(t, firstSessionID, subject), f.ns.Fence(), openEntries("globex"))
	wantErr(t, "Put with an entry of another namespace", err, audit.ErrInvalidEntry)

	// A session of another namespace than the fence.
	other := newNamespace(t, s, "globex")
	foreign := makeSession(t, sessionSpec{
		ns: other.Name, root: f.root.ID, id: firstSessionID, owner: subject, start: epoch(), length: time.Hour,
	})
	err = s.Sessions.Put(t.Context(), foreign, f.ns.Fence(), openEntries(f.ns.Name))
	wantErr(t, "Put of a session of another namespace than the fence", err, profile.ErrInvalid)

	f.wantGone(t, s, firstSessionID, subject)

	if after := auditCount(t, s, f.ns.Name); after != before {
		t.Errorf("audit entries after the invalid puts = %d, want %d", after, before)
	}
}

func testSessionConflict(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	f.put(t, s, f.session(t, firstSessionID, subject))

	before := auditCount(t, s, f.ns.Name)

	// The same ID for another owner is a conflict too: the ID is the key.
	for _, owner := range []string{subject, otherSubject} {
		err := s.Sessions.Put(t.Context(), f.session(t, firstSessionID, owner), f.ns.Fence(), openEntries(f.ns.Name))
		wantErr(t, "Put with an ID in use by "+owner, err, profile.ErrConflict)
	}

	got, err := f.get(s, firstSessionID, subject, epoch())
	if err != nil || string(got.Sealed()) != "sealed:"+firstSessionID {
		t.Errorf("Get after the conflicts = %v, %v, want the first session", got, err)
	}

	f.wantGone(t, s, firstSessionID, otherSubject)

	if after := auditCount(t, s, f.ns.Name); after != before {
		t.Errorf("audit entries after the conflicts = %d, want %d", after, before)
	}
}

func testSessionRootNotLive(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)

	unknown := makeSession(t, sessionSpec{
		ns: f.ns.Name, root: profile.NewID(), id: firstSessionID, owner: subject, start: epoch(), length: time.Hour,
	})
	err := s.Sessions.Put(t.Context(), unknown, f.ns.Fence(), openEntries(f.ns.Name))
	wantErr(t, "Put with an unknown root profile", err, profile.ErrNotFound)

	remove(t, s, f.ns, f.root)

	err = s.Sessions.Put(t.Context(), f.session(t, "pos-two", subject), f.ns.Fence(), openEntries(f.ns.Name))
	wantErr(t, "Put with a deleted root profile", err, profile.ErrNotFound)

	f.wantGone(t, s, firstSessionID, subject)
	f.wantGone(t, s, "pos-two", subject)
}

func testSessionAuditRollsBack(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)

	bad := openEntries(f.ns.Name)
	bad[0].Subject = ""

	err := s.Sessions.Put(t.Context(), f.session(t, firstSessionID, subject), f.ns.Fence(), bad)
	wantErr(t, "Put with an invalid entry", err, audit.ErrInvalidEntry)

	f.wantGone(t, s, firstSessionID, subject)
}

func testSessionAuditMany(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	before := auditCount(t, s, f.ns.Name)

	es := openEntries(f.ns.Name)
	second := entry(f.ns.Name, "profile:open")
	second.Kind = audit.KindAccess
	second.Object = "namespace:" + f.ns.Name.String()
	second.ObjectID = "second"
	es = append(es, second)

	if err := s.Sessions.Put(t.Context(), f.session(t, firstSessionID, subject), f.ns.Fence(), es); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if after := auditCount(t, s, f.ns.Name); after != before+len(es) {
		t.Errorf("audit entries = %d, want %d", after, before+len(es))
	}
}

func testSessionDeleteOtherOwner(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	f.put(t, s, f.session(t, firstSessionID, subject))

	err := f.delete(s, firstSessionID, otherSubject)
	wantErr(t, "Delete of another owner", err, profile.ErrNotFound)

	f.wantFound(t, s, firstSessionID, subject)
}

func testSessionPutOtherFence(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	other := newNamespace(t, s, "globex")
	before := auditCount(t, s, other.Name)

	// The session is of namespace A. The fence and the entries are of B.
	err := s.Sessions.Put(t.Context(), f.session(t, firstSessionID, subject), other.Fence(), openEntries(other.Name))
	wantErr(t, "Put of a session of namespace A under the fence of B", err, profile.ErrInvalid)

	f.wantGone(t, s, firstSessionID, subject)

	if _, err = s.Sessions.Get(t.Context(), other.Name, firstSessionID, profile.PrincipalOwner(subject), epoch()); err == nil {
		t.Errorf("namespace B holds the session of namespace A")
	}

	if after := auditCount(t, s, other.Name); after != before {
		t.Errorf("audit entries of B after the refused put = %d, want %d", after, before)
	}
}

func testSessionDeleteInvalidOwner(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	f.put(t, s, f.session(t, firstSessionID, subject))

	for _, owner := range []profile.SessionOwner{
		{Kind: "job", ID: subject}, {Kind: "", ID: subject}, {Kind: profile.OwnerPrincipal, ID: ""},
		{Kind: profile.OwnerPrincipal, ID: "al\nice"},
	} {
		err := s.Sessions.Delete(t.Context(), firstSessionID, owner, f.ns.Fence(), openEntries(f.ns.Name))
		wantErr(t, fmt.Sprintf("Delete of owner %+v", owner), err, profile.ErrNotFound)
	}

	f.wantFound(t, s, firstSessionID, subject)
}

func testSessionDeleteFenced(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	f.put(t, s, f.session(t, firstSessionID, subject))

	before := auditCount(t, s, f.ns.Name)

	stale := f.ns.Fence()
	stale.FailoverVersion = f.ns.FailoverVersion - 1

	err := s.Sessions.Delete(t.Context(), firstSessionID, profile.PrincipalOwner(subject), stale,
		openEntries(f.ns.Name))
	wantErr(t, "Delete with a stale fence", err, namespace.ErrFenced)

	f.wantFound(t, s, firstSessionID, subject)

	if after := auditCount(t, s, f.ns.Name); after != before {
		t.Errorf("audit entries after the fenced delete = %d, want %d", after, before)
	}
}

func testSessionDeleteAudit(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	f.put(t, s, f.session(t, firstSessionID, subject))

	before := auditCount(t, s, f.ns.Name)

	if err := f.delete(s, firstSessionID, subject); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if after := auditCount(t, s, f.ns.Name); after != before+1 {
		t.Errorf("audit entries after the delete = %d, want %d", after, before+1)
	}

	// The session is gone: the second delete finds none and writes no entry.
	err := f.delete(s, firstSessionID, subject)
	wantErr(t, "second Delete", err, profile.ErrNotFound)

	if after := auditCount(t, s, f.ns.Name); after != before+1 {
		t.Errorf("audit entries after the refused delete = %d, want %d", after, before+1)
	}

	// An invalid entry changes nothing.
	f.put(t, s, f.session(t, "pos-two", subject))

	bad := openEntries(f.ns.Name)
	bad[0].Subject = ""

	err = s.Sessions.Delete(t.Context(), "pos-two", profile.PrincipalOwner(subject), f.ns.Fence(), bad)
	wantErr(t, "Delete with an invalid entry", err, audit.ErrInvalidEntry)

	f.wantFound(t, s, "pos-two", subject)
}

func testSessionDelete(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	f.put(t, s, f.session(t, "pos-live", subject))
	f.put(t, s, f.sessionAt(t, "pos-ended", subject, epoch(), time.Minute))

	for _, id := range []string{"pos-live", "pos-ended"} {
		if err := f.delete(s, id, subject); err != nil {
			t.Errorf("Delete(%s): %v", id, err)
		}

		err := f.delete(s, id, subject)
		wantErr(t, "second Delete of "+id, err, profile.ErrNotFound)
	}

	f.wantGone(t, s, "pos-live", subject)
}

func testSessionSweepInvalid(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	f.put(t, s, f.sessionAt(t, "pos-ended", subject, epoch(), time.Minute))

	for _, limit := range []int{0, -1, -1000} {
		n, err := s.Sessions.Sweep(t.Context(), epoch().Add(time.Hour), limit)
		wantErr(t, fmt.Sprintf("Sweep with limit %d", limit), err, profile.ErrInvalid)

		if n != 0 {
			t.Errorf("Sweep with limit %d deleted %d, want 0", limit, n)
		}
	}

	// The refused sweeps left the row, which Delete still finds.
	if err := f.delete(s, "pos-ended", subject); err != nil {
		t.Errorf("Delete after the refused sweeps: %v", err)
	}
}

func testSessionSweep(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	other := newNamespace(t, s, "globex")
	otherRoot := create(t, s, other, "shared")

	f.put(t, s, f.sessionAt(t, "pos-ended", subject, epoch(), time.Minute))
	f.put(t, s, f.session(t, "pos-live", subject))

	foreign := makeSession(t, sessionSpec{
		ns: other.Name, root: otherRoot.ID, id: "pos-other", owner: subject, start: epoch(), length: time.Minute,
	})
	if err := s.Sessions.Put(t.Context(), foreign, other.Fence(), openEntries(other.Name)); err != nil {
		t.Fatalf("Put in another namespace: %v", err)
	}

	// A session that ends exactly at now has ended.
	n, err := s.Sessions.Sweep(t.Context(), epoch().Add(time.Minute), bigPage)
	if err != nil || n != 2 {
		t.Fatalf("Sweep = %d, %v, want 2 (one in each namespace), nil", n, err)
	}

	f.wantFound(t, s, "pos-live", subject)

	n, err = s.Sessions.Sweep(t.Context(), epoch().Add(time.Minute), bigPage)
	if err != nil || n != 0 {
		t.Errorf("second Sweep = %d, %v, want 0, nil", n, err)
	}
}

func testSessionSweepLimit(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)

	for i := range firstPage + 2 {
		f.put(t, s, f.sessionAt(t, fmt.Sprintf("pos-e%d", i), subject, epoch(), time.Minute))
	}

	n, err := s.Sessions.Sweep(t.Context(), epoch().Add(time.Hour), firstPage)
	if err != nil || n != firstPage {
		t.Fatalf("Sweep with limit %d = %d, %v, want %d", firstPage, n, err, firstPage)
	}

	n, err = s.Sessions.Sweep(t.Context(), epoch().Add(time.Hour), bigPage)
	if err != nil || n != 2 {
		t.Errorf("second Sweep = %d, %v, want 2", n, err)
	}
}

func sessionAnswer(result any) (idempotency.Answer, error) {
	w, ok := result.(profile.SessionWritten)
	if !ok {
		return idempotency.Answer{}, errNotWritten
	}

	return idempotency.Answer{Status: statusCreated, Headers: nil, Body: []byte(w.Session.ID())}, nil
}

func testSessionCompletionStored(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	ctx, c, ref := pendingRequest(t, s, "open", sessionAnswer)

	sess := f.session(t, firstSessionID, subject)
	if err := s.Sessions.Put(ctx, sess, f.ns.Fence(), openEntries(f.ns.Name)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if !c.Stored() || string(replayOf(t, s, ref).Body) != firstSessionID {
		t.Errorf("put: stored = %t, replay = %q, want the session ID", c.Stored(), replayOf(t, s, ref).Body)
	}
}

func testSessionCompletionKeptPending(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	f.put(t, s, f.session(t, firstSessionID, subject))

	ctx, c, ref := pendingRequest(t, s, "dup", sessionAnswer)

	err := s.Sessions.Put(ctx, f.session(t, firstSessionID, subject), f.ns.Fence(), openEntries(f.ns.Name))
	wantErr(t, "duplicate put with a completion", err, profile.ErrConflict)

	res, beginErr := s.Records.Begin(t.Context(), ref, []byte("body"), epoch())
	if beginErr != nil || res.Outcome != idempotency.InProgress || c.Stored() {
		t.Errorf("after a failed put: outcome = %d, err = %v, stored = %t, want InProgress, nil, false",
			res.Outcome, beginErr, c.Stored())
	}
}

func testSessionCompletionLost(t *testing.T, s Store) {
	t.Helper()

	f := newOpenFixture(t, s)
	ctx, c, ref := pendingRequest(t, s, "lost", sessionAnswer)

	// The record expires and a retry takes the key.
	late := epoch().Add(idempotency.PendingTTL + time.Second)

	retry, beginErr := s.Records.Begin(t.Context(), ref, []byte("body"), late)
	if beginErr != nil || retry.Outcome != idempotency.Started {
		t.Fatalf("retry: outcome = %d, err = %v, want Started", retry.Outcome, beginErr)
	}

	before := auditCount(t, s, f.ns.Name)
	err := s.Sessions.Put(ctx, f.session(t, firstSessionID, subject), f.ns.Fence(), openEntries(f.ns.Name))
	wantErr(t, "put with a lost record", err, idempotency.ErrLost)

	f.wantGone(t, s, firstSessionID, subject)

	if after := auditCount(t, s, f.ns.Name); after != before || c.Stored() {
		t.Errorf("after a lost record: audit entries = %d (want %d), stored = %t, want false",
			after, before, c.Stored())
	}
}
