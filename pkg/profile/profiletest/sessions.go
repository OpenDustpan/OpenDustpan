package profiletest

import (
	"bytes"
	"fmt"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// SessionStore is every persistence port the session suite exercises.
type SessionStore struct {
	Profiles   profile.Store
	Sessions   profile.Sessions
	Namespaces namespace.Store
	Audit      audit.Store
	// Records is the store of replay records that completes in the
	// transaction of a Put.
	Records idempotency.Store
}

// SessionFactory returns a fresh, empty store for one subtest.
type SessionFactory func(t *testing.T) SessionStore

// RunSessions runs the suite of profile.Sessions: Put and Get, the owner, the
// namespace, the end of a session, the limit, the fence, the audit entries,
// the replay record, Delete and Sweep. Every store adapter runs it.
func RunSessions(t *testing.T, newStore SessionFactory) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, s SessionStore)
	}{
		{"put and get round trip", testSessionRoundTrip},
		{"a session with no paths reads back with no paths", testSessionNoPaths},
		{"another owner reads not found", testSessionOtherOwner},
		{"another namespace reads not found", testSessionOtherNamespace},
		{"an ended session reads not found", testSessionEnded},
		{"a forged ID reads not found", testSessionForgedID},
		{"the limit is 100 live sessions of one owner", testSessionLimit},
		{"an ended session does not count against the limit", testSessionLimitEnded},
		{"the limit is for each owner", testSessionLimitPerOwner},
		{"an ID in use is a conflict and changes nothing", testSessionDuplicate},
		{"a stale fence refuses a put and stores nothing", testSessionFenced},
		{"an unknown namespace is not found", testSessionUnknownNamespace},
		{"a put appends every entry in order", testSessionAudit},
		{"a failed audit entry leaves no session", testSessionAuditRollsBack},
		{"a put with no audit entry is invalid", testSessionAuditNone},
		{"a refused put leaves no audit entry", testSessionRefusedLeavesNoEntry},
		{"a put refuses an invalid session", testSessionInvalid},
		{"a put stores the answer of its request in its transaction", testSessionCompletionStored},
		{"a refused put leaves the record of its request pending", testSessionCompletionKeptPending},
		{"a put whose record is gone changes nothing", testSessionCompletionLost},
		{"delete removes a session of its owner only", testSessionDelete},
		{"delete of an ended session is not found and removes it", testSessionDeleteEnded},
		{"sweep deletes only ended sessions", testSessionSweep},
		{"sweep deletes at most its limit", testSessionSweepLimit},
		{"sweep refuses a limit below 1", testSessionSweepInvalid},
	}

	runCases(t, newStore, cases)
}

// sessionTTL is the duration of a session of the suite.
const sessionTTL = 2 * time.Hour

// farFuture is the end of a session that stays live for the wall clock.
//
//nolint:gochecknoglobals // Fixed fixture of the suite, never written.
var farFuture = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)

// ownerAlice and ownerBob are the owners of the sessions of the suite.
//
//nolint:gochecknoglobals // Fixed fixtures of the suite, never written.
var (
	ownerAlice = profile.SessionOwner{Kind: profile.OwnerPrincipal, ID: "alice"}
	ownerBob   = profile.SessionOwner{Kind: profile.OwnerPrincipal, ID: "bob"}
)

// sessionDoc is a minimal document of a root profile of the suite.
const sessionDoc = "schemaVersion: 1\nvalues:\n  region: eu-west-1\n"

func sessionNamespace(t *testing.T, s SessionStore, name string) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           epoch(),
		Name:                namespace.Name(name),
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.Namespaces.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %q: %v", name, err)
	}

	return ns
}

// sessionRoot creates the root profile that the sessions of the suite open.
// The foreign key of the session table names it.
func sessionRoot(t *testing.T, s SessionStore, ns namespace.Namespace) profile.Profile {
	t.Helper()

	id := profile.NewID()

	p := profile.Profile{
		ID: id, Namespace: ns.Name, Name: "session-root", Latest: 1, CreatedAt: epoch(), CreatedBy: subject,
		UpdatedAt: epoch(),
	}
	r := profile.Revision{
		Profile: id, Number: 1, Document: []byte(sessionDoc), SHA256: sha(1), Author: subject, CreatedAt: epoch(),
		Reason: "",
	}

	created, err := s.Profiles.Create(t.Context(), p, r, ns.Fence(), entries(ns.Name, "profile:write"))
	if err != nil {
		t.Fatalf("create session root: %v", err)
	}

	return created
}

// newSession is a session of owner that starts at the epoch and ends two
// hours later.
func newSession(ns namespace.Name, root profile.Profile, n int, owner profile.SessionOwner) profile.Session {
	id := fmt.Sprintf("%s%04d", profile.SessionIDPrefix, n)

	return profile.Session{
		ID: id, Namespace: ns, Root: root.ID, Owner: owner, Consumer: "api",
		Paths: []string{"region", "db/host"}, Sealed: []byte("sealed-" + id), CreatedAt: epoch(),
		ExpiresAt: epoch().Add(sessionTTL),
	}
}

// liveAt is a time at which the sessions of newSession are live.
func liveAt() time.Time { return epoch().Add(time.Minute) }

func putSession(t *testing.T, s SessionStore, ns namespace.Namespace, sess profile.Session) {
	t.Helper()

	if err := s.Sessions.Put(t.Context(), sess, ns.Fence(), entries(ns.Name, "profile:open")); err != nil {
		t.Fatalf("Put(%s): %v", sess.ID, err)
	}
}

// wantSessionMissing checks that the session reads as not found.
func wantSessionMissing(t *testing.T, s SessionStore, sess profile.Session, what string) {
	t.Helper()

	_, err := s.Sessions.Get(t.Context(), sess.Namespace, sess.ID, sess.Owner, liveAt())
	wantErr(t, what, err, profile.ErrNotFound)
}

// auditTotal returns the number of audit entries of ns.
func auditTotal(t *testing.T, s SessionStore, ns namespace.Name) int64 {
	t.Helper()

	list, err := s.Audit.List(t.Context(), ns, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}

	if list.More {
		t.Fatalf("List audit has more than %d entries", audit.MaxPageSize)
	}

	return int64(len(list.Entries))
}

func sameSession(a, b profile.Session) bool {
	return a.ID == b.ID && a.Namespace == b.Namespace && a.Root == b.Root && a.Owner == b.Owner &&
		a.Consumer == b.Consumer && slices.Equal(a.Paths, b.Paths) &&
		bytes.Equal(a.Sealed, b.Sealed) && a.CreatedAt.Equal(b.CreatedAt) && a.ExpiresAt.Equal(b.ExpiresAt)
}

func testSessionRoundTrip(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)
	before := auditTotal(t, s, ns.Name)

	want := newSession(ns.Name, root, 1, ownerAlice)
	putSession(t, s, ns, want)

	got, err := s.Sessions.Get(t.Context(), ns.Name, want.ID, ownerAlice, liveAt())
	if err != nil || !sameSession(got, want) {
		t.Errorf("Get = %+v, %v, want %+v", got, err, want)
	}

	if !got.Live(liveAt()) {
		t.Error("the session read back is not live")
	}

	// The last millisecond of the session still reads.
	last := want.ExpiresAt.Add(-time.Millisecond)
	if _, err = s.Sessions.Get(t.Context(), ns.Name, want.ID, ownerAlice, last); err != nil {
		t.Errorf("Get one millisecond before the end: %v", err)
	}

	if n := auditTotal(t, s, ns.Name) - before; n != 1 {
		t.Errorf("audit entries of the put = %d, want 1", n)
	}
}

func testSessionNoPaths(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)
	want := newSession(ns.Name, root, 1, ownerAlice)
	want.Paths = nil
	putSession(t, s, ns, want)

	got, err := s.Sessions.Get(t.Context(), ns.Name, want.ID, ownerAlice, liveAt())
	if err != nil || len(got.Paths) != 0 {
		t.Errorf("Get = %+v, %v, want no paths", got, err)
	}
}

func testSessionOtherOwner(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)
	sess := newSession(ns.Name, root, 1, ownerAlice)
	putSession(t, s, ns, sess)

	others := map[string]profile.SessionOwner{
		"another principal":          ownerBob,
		"an empty owner":             {Kind: "", ID: ""},
		"the owner ID in other case": {Kind: profile.OwnerPrincipal, ID: "ALICE"},
	}

	for name, owner := range others {
		_, err := s.Sessions.Get(t.Context(), ns.Name, sess.ID, owner, liveAt())
		wantErr(t, "Get by "+name, err, profile.ErrNotFound)
	}

	err := s.Sessions.Delete(t.Context(), ns.Name, sess.ID, ownerBob)
	wantErr(t, "Delete by another principal", err, profile.ErrNotFound)

	if _, err = s.Sessions.Get(t.Context(), ns.Name, sess.ID, ownerAlice, liveAt()); err != nil {
		t.Errorf("Get by the owner after a refused delete: %v", err)
	}
}

func testSessionOtherNamespace(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	other := sessionNamespace(t, s, "globex")
	root := sessionRoot(t, s, ns)
	otherRoot := sessionRoot(t, s, other)
	sess := newSession(ns.Name, root, 1, ownerAlice)
	putSession(t, s, ns, sess)

	_, err := s.Sessions.Get(t.Context(), other.Name, sess.ID, ownerAlice, liveAt())
	wantErr(t, "Get in another namespace", err, profile.ErrNotFound)

	_, err = s.Sessions.Get(t.Context(), "nowhere", sess.ID, ownerAlice, liveAt())
	wantErr(t, "Get in an unknown namespace", err, profile.ErrNotFound)

	_, err = s.Sessions.Get(t.Context(), ns.Name, "pos-unknown", ownerAlice, liveAt())
	wantErr(t, "Get of an unknown ID", err, profile.ErrNotFound)

	err = s.Sessions.Delete(t.Context(), other.Name, sess.ID, ownerAlice)
	wantErr(t, "Delete in another namespace", err, profile.ErrNotFound)

	// The same ID is free in another namespace.
	twin := sess
	twin.Namespace = other.Name
	twin.Root = otherRoot.ID

	putSession(t, s, other, twin)
}

func testSessionEnded(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)
	sess := newSession(ns.Name, root, 1, ownerAlice)
	putSession(t, s, ns, sess)

	for name, at := range map[string]time.Time{
		"at the end":     sess.ExpiresAt,
		"after the end":  sess.ExpiresAt.Add(time.Hour),
		"long after end": sess.ExpiresAt.Add(1000 * time.Hour),
	} {
		_, err := s.Sessions.Get(t.Context(), ns.Name, sess.ID, ownerAlice, at)
		wantErr(t, "Get "+name, err, profile.ErrNotFound)
	}
}

func testSessionForgedID(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)
	sess := newSession(ns.Name, root, 1, ownerAlice)
	putSession(t, s, ns, sess)

	for name, id := range map[string]string{
		"no prefix":    "0001",
		"empty":        "",
		"prefix only":  profile.SessionIDPrefix,
		"other prefix": "ses-0001",
	} {
		_, err := s.Sessions.Get(t.Context(), ns.Name, id, ownerAlice, liveAt())
		wantErr(t, "Get with "+name, err, profile.ErrNotFound)
	}
}

func testSessionLimit(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)
	before := auditTotal(t, s, ns.Name)

	for n := range profile.MaxLiveSessions {
		putSession(t, s, ns, newSession(ns.Name, root, n, ownerAlice))
	}

	extra := newSession(ns.Name, root, profile.MaxLiveSessions, ownerAlice)
	err := s.Sessions.Put(t.Context(), extra, ns.Fence(), entries(ns.Name, "profile:open"))
	wantErr(t, "the 101st put", err, profile.ErrSessionLimit)
	wantErr(t, "the 101st put wraps the limit error", err, profile.ErrLimit)
	wantSessionMissing(t, s, extra, "Get of the 101st session")

	if n := auditTotal(t, s, ns.Name) - before; n != profile.MaxLiveSessions {
		t.Errorf("audit entries = %d, want %d: a refused put left an entry", n, profile.MaxLiveSessions)
	}
}

func testSessionLimitEnded(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)

	// Half of the sessions end before the time of the next put.
	for n := range profile.MaxLiveSessions {
		sess := newSession(ns.Name, root, n, ownerAlice)
		if n%2 == 0 {
			sess.ExpiresAt = epoch().Add(time.Minute)
		}

		putSession(t, s, ns, sess)
	}

	next := newSession(ns.Name, root, profile.MaxLiveSessions, ownerAlice)
	next.CreatedAt = epoch().Add(time.Hour)
	next.ExpiresAt = next.CreatedAt.Add(sessionTTL)
	putSession(t, s, ns, next)

	// The session that ends exactly at the time of the put does not count.
	edge := newSession(ns.Name, root, profile.MaxLiveSessions+1, ownerBob)
	edge.ExpiresAt = epoch().Add(time.Hour)
	putSession(t, s, ns, edge)

	again := newSession(ns.Name, root, profile.MaxLiveSessions+2, ownerBob)
	again.CreatedAt = epoch().Add(time.Hour)
	again.ExpiresAt = again.CreatedAt.Add(sessionTTL)
	putSession(t, s, ns, again)
}

func testSessionLimitPerOwner(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)

	for n := range profile.MaxLiveSessions {
		putSession(t, s, ns, newSession(ns.Name, root, n, ownerAlice))
	}

	// Another principal has its own count.
	putSession(t, s, ns, newSession(ns.Name, root, profile.MaxLiveSessions, ownerBob))

	err := s.Sessions.Put(t.Context(), newSession(ns.Name, root, profile.MaxLiveSessions+1, ownerAlice), ns.Fence(),
		entries(ns.Name, "profile:open"))
	wantErr(t, "a put of the full owner", err, profile.ErrSessionLimit)
}

func testSessionDuplicate(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)
	before := auditTotal(t, s, ns.Name)

	sess := newSession(ns.Name, root, 1, ownerAlice)
	putSession(t, s, ns, sess)

	again := sess
	again.Sealed = []byte("other")
	err := s.Sessions.Put(t.Context(), again, ns.Fence(), entries(ns.Name, "profile:open"))
	wantErr(t, "a put of the same ID", err, profile.ErrConflict)

	// The ID of another owner is in use too.
	steal := sess
	steal.Owner = ownerBob
	err = s.Sessions.Put(t.Context(), steal, ns.Fence(), entries(ns.Name, "profile:open"))
	wantErr(t, "a put of the same ID by another owner", err, profile.ErrConflict)

	got, err := s.Sessions.Get(t.Context(), ns.Name, sess.ID, ownerAlice, liveAt())
	if err != nil || !sameSession(got, sess) {
		t.Errorf("Get after the refused puts = %+v, %v, want the first session", got, err)
	}

	if n := auditTotal(t, s, ns.Name) - before; n != 1 {
		t.Errorf("audit entries = %d, want 1", n)
	}
}

func testSessionFenced(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)
	before := auditTotal(t, s, ns.Name)

	stale := ns.Fence()
	stale.FailoverVersion = ns.FailoverVersion - 1

	sess := newSession(ns.Name, root, 1, ownerAlice)
	err := s.Sessions.Put(t.Context(), sess, stale, entries(ns.Name, "profile:open"))
	wantErr(t, "Put with a stale fence", err, namespace.ErrFenced)
	wantSessionMissing(t, s, sess, "Get after the fenced put")

	if n := auditTotal(t, s, ns.Name) - before; n != 0 {
		t.Errorf("audit entries = %d, want 0", n)
	}

	// A stale fence does not hide behind a full owner.
	for n := range profile.MaxLiveSessions {
		putSession(t, s, ns, newSession(ns.Name, root, n+10, ownerAlice))
	}

	err = s.Sessions.Put(t.Context(), sess, stale, entries(ns.Name, "profile:open"))
	wantErr(t, "Put with a stale fence and a full owner", err, namespace.ErrFenced)
}

func testSessionUnknownNamespace(t *testing.T, s SessionStore) {
	t.Helper()

	sess := newSession("nowhere", profile.Profile{
		ID: profile.NewID(), Namespace: "nowhere", Name: "nowhere", Latest: 0,
		CreatedAt: time.Time{}, CreatedBy: "", UpdatedAt: time.Time{},
	}, 1, ownerAlice)
	fence := namespace.Fence{Namespace: "nowhere", FailoverVersion: 1}

	err := s.Sessions.Put(t.Context(), sess, fence, entries("nowhere", "profile:open"))
	wantErr(t, "Put in an unknown namespace", err, namespace.ErrNotFound)
}

func testSessionAudit(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)
	sess := newSession(ns.Name, root, 1, ownerAlice)

	es := []audit.Entry{
		entry(ns.Name, "profile:open"),
		entry(ns.Name, "profile:access"),
		entry(ns.Name, "profile:read"),
	}
	if err := s.Sessions.Put(t.Context(), sess, ns.Fence(), es); err != nil {
		t.Fatalf("Put: %v", err)
	}

	list, err := s.Audit.List(t.Context(), ns.Name, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}

	// The root profile create wrote one entry before the put.
	if len(list.Entries) != len(es)+1 {
		t.Fatalf("audit entries = %d, want %d", len(list.Entries), len(es)+1)
	}

	// The listing is newest first, and the root entry is the oldest.
	for n := range es {
		if got := list.Entries[len(es)-1-n].Action; got != es[n].Action {
			t.Errorf("entry %d action = %q, want %q", n, got, es[n].Action)
		}
	}
}

func testSessionAuditRollsBack(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)
	bad := entry(ns.Name, "profile:open")
	bad.Subject = ""

	sess := newSession(ns.Name, root, 1, ownerAlice)

	err := s.Sessions.Put(t.Context(), sess, ns.Fence(), []audit.Entry{bad})
	wantErr(t, "Put with an invalid entry", err, audit.ErrInvalidEntry)
	wantSessionMissing(t, s, sess, "Get after the invalid entry")

	err = s.Sessions.Put(t.Context(), sess, ns.Fence(), []audit.Entry{entry(ns.Name, "profile:open"), bad})
	wantErr(t, "Put with a valid and an invalid entry", err, audit.ErrInvalidEntry)
	wantSessionMissing(t, s, sess, "Get after the second invalid entry")

	err = s.Sessions.Put(t.Context(), sess, ns.Fence(), entries("globex", "profile:open"))
	wantErr(t, "Put with an entry of another namespace", err, audit.ErrInvalidEntry)
	wantSessionMissing(t, s, sess, "Get after the entry of another namespace")

	before := auditTotal(t, s, ns.Name)
	putSession(t, s, ns, sess)

	if n := auditTotal(t, s, ns.Name) - before; n != 1 {
		t.Errorf("audit entries of a clean put after refused puts = %d, want 1", n)
	}
}

func testSessionAuditNone(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)

	sess := newSession(ns.Name, root, 1, ownerAlice)

	err := s.Sessions.Put(t.Context(), sess, ns.Fence(), nil)
	wantErr(t, "Put with no entry", err, profile.ErrInvalid)

	err = s.Sessions.Put(t.Context(), sess, ns.Fence(), []audit.Entry{})
	wantErr(t, "Put with an empty slice of entries", err, profile.ErrInvalid)
	wantSessionMissing(t, s, sess, "Get after the puts with no entry")
}

func testSessionRefusedLeavesNoEntry(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)
	before := auditTotal(t, s, ns.Name)

	sess := newSession(ns.Name, root, 1, ownerAlice)
	putSession(t, s, ns, sess)

	err := s.Sessions.Put(t.Context(), sess, ns.Fence(), entries(ns.Name, "profile:open"))
	wantErr(t, "Put of an ID in use", err, profile.ErrConflict)

	if n := auditTotal(t, s, ns.Name) - before; n != 1 {
		t.Errorf("audit entries = %d, want 1: a refused put left an entry", n)
	}
}

func testSessionInvalid(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)

	cases := map[string]func(*profile.Session){
		"an ID with no prefix":       func(x *profile.Session) { x.ID = "0001" },
		"an empty ID":                func(x *profile.Session) { x.ID = "" },
		"a prefix only":              func(x *profile.Session) { x.ID = profile.SessionIDPrefix },
		"an empty sealed value":      func(x *profile.Session) { x.Sealed = nil },
		"a zero-length sealed value": func(x *profile.Session) { x.Sealed = []byte{} },
		"an end at the start":        func(x *profile.Session) { x.ExpiresAt = x.CreatedAt },
		"an end before the start":    func(x *profile.Session) { x.ExpiresAt = x.CreatedAt.Add(-time.Second) },
		"an owner with no ID":        func(x *profile.Session) { x.Owner.ID = "" },
		"an owner with no kind":      func(x *profile.Session) { x.Owner.Kind = "" },
		"an owner of an unknown kind": func(x *profile.Session) {
			x.Owner.Kind = "robot"
		},
		"another namespace than the fence": func(x *profile.Session) { x.Namespace = "globex" },
	}

	for name, mutate := range cases {
		sess := newSession(ns.Name, root, 1, ownerAlice)
		mutate(&sess)

		err := s.Sessions.Put(t.Context(), sess, ns.Fence(), entries(ns.Name, "profile:open"))
		wantErr(t, "Put with "+name, err, profile.ErrInvalid)
	}

	wantSessionMissing(t, s, newSession(ns.Name, root, 1, ownerAlice), "Get after the invalid puts")

	before := auditTotal(t, s, ns.Name)
	putSession(t, s, ns, newSession(ns.Name, root, 1, ownerAlice))

	if n := auditTotal(t, s, ns.Name) - before; n != 1 {
		t.Errorf("audit entries of a clean put after invalid puts = %d, want 1", n)
	}
}

// sessionAnswer builds the answer of a request that stored a session.
func sessionAnswer(result any) (idempotency.Answer, error) {
	w, ok := result.(profile.SessionWritten)
	if !ok {
		return idempotency.Answer{}, errNotWritten
	}

	return idempotency.Answer{Status: statusCreated, Headers: nil, Body: []byte(w.Session.ID)}, nil
}

func testSessionCompletionStored(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)
	store := Store{Profiles: nil, Namespaces: nil, Audit: nil, Records: s.Records}
	ctx, c, ref := pendingRequest(t, store, "open", sessionAnswer)

	sess := newSession(ns.Name, root, 1, ownerAlice)
	if err := s.Sessions.Put(ctx, sess, ns.Fence(), entries(ns.Name, "profile:open")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if !c.Stored() || string(replayOf(t, store, ref).Body) != sess.ID {
		t.Errorf("stored = %t, replay = %q, want %q", c.Stored(), replayOf(t, store, ref).Body, sess.ID)
	}
}

func testSessionCompletionKeptPending(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)
	sess := newSession(ns.Name, root, 1, ownerAlice)
	putSession(t, s, ns, sess)

	store := Store{Profiles: nil, Namespaces: nil, Audit: nil, Records: s.Records}
	ctx, c, ref := pendingRequest(t, store, "dup", sessionAnswer)

	err := s.Sessions.Put(ctx, sess, ns.Fence(), entries(ns.Name, "profile:open"))
	wantErr(t, "Put of an ID in use with a completion", err, profile.ErrConflict)

	res, beginErr := s.Records.Begin(t.Context(), ref, []byte("body"), epoch())
	if beginErr != nil || res.Outcome != idempotency.InProgress || c.Stored() {
		t.Errorf("after a refused put: outcome = %d, err = %v, stored = %t, want InProgress, nil, false",
			res.Outcome, beginErr, c.Stored())
	}
}

func testSessionCompletionLost(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)
	store := Store{Profiles: nil, Namespaces: nil, Audit: nil, Records: s.Records}
	ctx, c, ref := pendingRequest(t, store, "lost", sessionAnswer)

	// The record expires and a retry takes the key.
	late := epoch().Add(idempotency.PendingTTL + time.Second)

	retry, beginErr := s.Records.Begin(t.Context(), ref, []byte("body"), late)
	if beginErr != nil || retry.Outcome != idempotency.Started {
		t.Fatalf("retry: outcome = %d, err = %v, want Started", retry.Outcome, beginErr)
	}

	sess := newSession(ns.Name, root, 1, ownerAlice)
	before := auditTotal(t, s, ns.Name)

	err := s.Sessions.Put(ctx, sess, ns.Fence(), entries(ns.Name, "profile:open"))
	wantErr(t, "Put with a lost record", err, idempotency.ErrLost)
	wantSessionMissing(t, s, sess, "Get after a lost record")

	if n := auditTotal(t, s, ns.Name) - before; n != 0 || c.Stored() {
		t.Errorf("after a lost record: audit total = %d, stored = %t, want 0, false", n, c.Stored())
	}
}

func testSessionDelete(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)
	sess := newSession(ns.Name, root, 1, ownerAlice)
	sess.ExpiresAt = farFuture
	putSession(t, s, ns, sess)

	if err := s.Sessions.Delete(t.Context(), ns.Name, sess.ID, ownerAlice); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	wantSessionMissing(t, s, sess, "Get after the delete")

	err := s.Sessions.Delete(t.Context(), ns.Name, sess.ID, ownerAlice)
	wantErr(t, "a second Delete", err, profile.ErrNotFound)

	// The ID is free again.
	putSession(t, s, ns, sess)
}

func testSessionDeleteEnded(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)

	// The session ended at the epoch plus two hours, long before the wall
	// clock of the run.
	sess := newSession(ns.Name, root, 1, ownerAlice)
	putSession(t, s, ns, sess)

	err := s.Sessions.Delete(t.Context(), ns.Name, sess.ID, ownerAlice)
	wantErr(t, "Delete of an ended session", err, profile.ErrNotFound)

	n, err := s.Sessions.Sweep(t.Context(), time.Now().Add(time.Hour), bigPage)
	if err != nil || n != 0 {
		t.Errorf("Sweep after the delete = %d, %v, want 0: the delete left the session", n, err)
	}
}

func testSessionSweep(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	other := sessionNamespace(t, s, "globex")
	root := sessionRoot(t, s, ns)
	otherRoot := sessionRoot(t, s, other)

	ended := newSession(ns.Name, root, 1, ownerAlice)
	ended.ExpiresAt = epoch().Add(time.Hour)
	live := newSession(ns.Name, root, 2, ownerAlice)
	live.ExpiresAt = epoch().Add(3 * time.Hour)
	endedOther := newSession(other.Name, otherRoot, 3, ownerBob)
	endedOther.ExpiresAt = epoch().Add(time.Hour)

	putSession(t, s, ns, ended)
	putSession(t, s, ns, live)
	putSession(t, s, other, endedOther)

	// At a time before every end, nothing goes.
	n, err := s.Sessions.Sweep(t.Context(), epoch().Add(time.Minute), bigPage)
	if err != nil || n != 0 {
		t.Fatalf("Sweep before any end = %d, %v, want 0", n, err)
	}

	// A session that ends at now is ended.
	n, err = s.Sessions.Sweep(t.Context(), ended.ExpiresAt, bigPage)
	if err != nil || n != 2 {
		t.Fatalf("Sweep = %d, %v, want 2: the ended sessions of both namespaces", n, err)
	}

	if _, err = s.Sessions.Get(t.Context(), ns.Name, live.ID, ownerAlice, liveAt()); err != nil {
		t.Errorf("Get of the live session after the sweep: %v", err)
	}

	// The sweep freed the IDs.
	putSession(t, s, ns, ended)

	n, err = s.Sessions.Sweep(t.Context(), ended.ExpiresAt, bigPage)
	if err != nil || n != 1 {
		t.Errorf("second Sweep = %d, %v, want 1", n, err)
	}
}

func testSessionSweepLimit(t *testing.T, s SessionStore) {
	t.Helper()

	ns := sessionNamespace(t, s, "acme")
	root := sessionRoot(t, s, ns)

	const total = 5

	for n := range total {
		sess := newSession(ns.Name, root, n, ownerAlice)
		sess.ExpiresAt = epoch().Add(time.Hour)
		putSession(t, s, ns, sess)
	}

	at := epoch().Add(time.Hour)

	for _, want := range []int{smallPage, smallPage, 1, 0} {
		n, err := s.Sessions.Sweep(t.Context(), at, smallPage)
		if err != nil || n != want {
			t.Fatalf("Sweep = %d, %v, want %d", n, err, want)
		}
	}
}

func testSessionSweepInvalid(t *testing.T, s SessionStore) {
	t.Helper()

	for _, limit := range []int{0, -1} {
		_, err := s.Sessions.Sweep(t.Context(), epoch(), limit)
		wantErr(t, fmt.Sprintf("Sweep with limit %d", limit), err, profile.ErrInvalid)
	}
}
