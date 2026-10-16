// Package sessiontest is the conformance suite for session.Store,
// session.PolicyStore, session.ProviderStore and
// session.ProviderConfigStore. Every built-in store runs it, and a custom store
// passed to dustpan.NewServer should too:
//
//	func TestSessionConformance(t *testing.T) {
//		t.Parallel()
//		sessiontest.Run(t, func(t *testing.T) sessiontest.Store { return newMyStore(t) })
//	}
package sessiontest

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// Store is what the suite needs: the session port, the session policy
// port, the principal records that own the sessions, and the access
// tokens, whose inactivity disable counts a sign-in as activity.
type Store interface {
	session.Store
	session.PolicyStore
	session.ProviderStore
	session.ProviderConfigStore
	principal.RecordStore
	accesstoken.Store
}

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) Store

// Run executes the conformance suite against stores produced by newStore.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s Store)
		name string
	}{
		{name: "session round trip", test: testRoundTrip},
		{name: "a session that fails validation is ErrInvalid", test: testInvalidSession},
		{name: "a session needs a person that exists and is enabled", test: testPrincipalRules},
		{name: "duplicate ID or digest is ErrAlreadyExists", test: testDuplicate},
		{name: "missing session is ErrNotFound", test: testMissing},
		{name: "a deleted owner hides its sessions from the lookup", test: testDeletedOwner},
		{name: "a disabled owner comes back with its disable time", test: testDisabledOwner},
		{name: "touch records the last use at most once for each period", test: testTouch},
		{name: "touch keeps the authentication time", test: testTouchKeepsAuthTime},
		{name: "a stored sign-in provider round trips", test: testProviderRoundTrip},
		{name: "an invalid sign-in provider is refused", test: testProviderRefusesInvalid},
		{name: "an update of a provider checks its version", test: testProviderUpdate},
		{name: "a provider with principals is not deleted", test: testProviderDelete},
		{name: "each provider change appends its entry", test: testProviderChangeAppendsEntry},
		{name: "a disable of a provider ends its sessions", test: testProviderDisableEndsSessions},
		{name: "end sets the end time and the reason once", test: testEnd},
		{name: "a change appends its audit entry in @server", test: testChangeAppendsEntry},
		{name: "an entry that the audit log refuses changes nothing", test: testRefusedEntryChangesNothing},
		{name: "a sign-in counts as activity for the inactivity disable", test: testSignInIsActivity},
		{name: "the last sign-in never moves back", test: testLastSignInNeverMovesBack},
		{name: "a session use counts as activity for the inactivity disable", test: testSessionUseIsActivity},
		{name: "a session use counts as activity in the token lookup", test: testSessionUseInTokenLookup},
		{name: "a session use never enables a disabled person", test: testSessionUseKeepsDisable},
		{name: "a touch leaves an idle or expired session as it is", test: testTouchKeepsInactiveSession},
		{name: "the lookup answers the activity and the break-glass flag of the owner", test: testOwnerActivity},
		{name: "a sign-in request works once", test: testRequestOnce},
		{name: "a sign-in request keeps its max_age", test: testRequestMaxAge},
		{name: "the token freshness window keeps the default until it is set", test: testPolicyTokenFreshness},
		{name: "a provider ID keeps the issuer of its first binding", test: testBindProvider},
		{name: "an expired sign-in request is gone", test: testRequestExpired},
		{name: "a sign-in request that fails validation is ErrInvalid", test: testInvalidRequest},
		{name: "a duplicate state digest is ErrAlreadyExists", test: testDuplicateRequest},
		{name: "prune deletes the expired sign-in requests only", test: testPruneRequests},
		{name: "concurrent takes of one sign-in request: one wins", test: testConcurrentTakes},
		{name: "the session policy starts at the defaults and keeps a change", test: testPolicy},
		{name: "concurrent session policy updates: each sees the one before", test: testConcurrentPolicyUpdates},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// at is a fixed, millisecond-precision instant: stores keep milliseconds.
func at(minute int) time.Time {
	return time.Date(2026, time.October, 15, 9, minute, 0, 0, time.UTC)
}

// entry returns a valid audit entry of the kind change in the scope
// @server for the session id of prn-1, as a service builds it.
func entry(id string, when time.Time) audit.Entry {
	return audit.Entry{
		At:           when,
		Namespace:    audit.ServerScope,
		Subject:      "alice",
		IDPSubject:   "",
		TokenID:      "",
		Action:       "server:sign-in",
		Object:       session.FullRef("prn-1", id),
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "192.0.2.1",
		UserAgent:    "sessiontest",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindChange,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "",
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		FenceVersion: 0,
	}
}

// refused returns an entry that fails validation: it names no subject.
func refused(id string, when time.Time) audit.Entry {
	e := entry(id, when)
	e.Subject = ""

	return e
}

// digest returns a distinct digest for n.
func digest(n int) session.Digest {
	return sha256.Sum256([]byte("session " + strconv.Itoa(n)))
}

// newSession returns a session of principalID made at created, after an
// authentication one minute before.
func newSession(id, principalID string, created time.Time) session.Session {
	return session.Session{
		CreatedAt:   created,
		LastSeenAt:  created,
		AuthTime:    created.Add(-time.Minute),
		EndedAt:     time.Time{},
		ID:          id,
		PrincipalID: principalID,
		Provider:    "corp-idp",
		EndReason:   "",
		SourceIP:    "192.0.2.10",
		UserAgent:   "Mozilla/5.0 (sessiontest)",
	}
}

// mustPrincipalAt creates the principal id of kind, made at created.
func mustPrincipalAt(t *testing.T, s Store, id string, kind principal.Kind, created time.Time) {
	t.Helper()

	p := principal.Principal{
		CreatedAt:   created,
		UpdatedAt:   created,
		DisabledAt:  time.Time{},
		ID:          id,
		Subject:     "subject of " + id,
		DisplayName: "Name of " + id,
		Kind:        kind,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
		BreakGlass:  false,
	}

	e := entry("ses-x", created)
	e.Object = principal.Ref(id)

	if err := s.CreatePrincipal(t.Context(), p, e); err != nil {
		t.Fatalf("create principal %q: %v", id, err)
	}
}

// mustPerson creates the person id, made at minute 0.
func mustPerson(t *testing.T, s Store, id string) {
	t.Helper()
	mustPrincipalAt(t, s, id, principal.KindPerson, at(0))
}

// mustSession creates ss with the digest d.
func mustSession(t *testing.T, s Store, ss session.Session, d session.Digest) {
	t.Helper()

	if err := s.CreateSession(t.Context(), ss, d, entry(ss.ID, ss.CreatedAt)); err != nil {
		t.Fatalf("create session %q: %v", ss.ID, err)
	}
}

// mustGet returns the session ses-1, which most cases make.
func mustGet(t *testing.T, s Store) session.Session {
	t.Helper()

	got, err := s.GetSession(t.Context(), "ses-1")
	if err != nil {
		t.Fatalf("get session ses-1: %v", err)
	}

	return got
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want %v", what, err, want)
	}
}

func equalSession(t *testing.T, got, want session.Session) {
	t.Helper()

	same := got.CreatedAt.Equal(want.CreatedAt) && got.LastSeenAt.Equal(want.LastSeenAt) &&
		got.AuthTime.Equal(want.AuthTime) && got.EndedAt.Equal(want.EndedAt) &&
		got.ID == want.ID && got.PrincipalID == want.PrincipalID && got.Provider == want.Provider &&
		got.EndReason == want.EndReason && got.SourceIP == want.SourceIP && got.UserAgent == want.UserAgent
	if !same {
		t.Fatalf("session = %+v, want %+v", got, want)
	}
}

func testRoundTrip(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPerson(t, s, "prn-1")

	ss := newSession("ses-1", "prn-1", at(5))
	mustSession(t, s, ss, digest(1))

	equalSession(t, mustGet(t, s), ss)

	m, err := s.GetSessionByDigest(ctx, digest(1))
	if err != nil {
		t.Fatalf("get by digest: %v", err)
	}

	equalSession(t, m.Session, ss)

	if m.Digest != digest(1) {
		t.Fatalf("stored digest = %s, want %s", m.Digest, digest(1))
	}

	// The owner was made at minute 0. Its latest activity is the sign-in
	// of the session, at minute 5.
	wantOwner := session.Owner{
		ActiveAt: ss.CreatedAt, DisabledAt: time.Time{}, ID: "prn-1", Subject: "subject of prn-1",
		DisplayName: "Name of prn-1", Kind: principal.KindPerson, BreakGlass: false,
	}
	if !m.Owner.ActiveAt.Equal(wantOwner.ActiveAt) {
		t.Fatalf("owner active at = %s, want %s", m.Owner.ActiveAt, wantOwner.ActiveAt)
	}

	m.Owner.ActiveAt = wantOwner.ActiveAt
	if m.Owner != wantOwner {
		t.Fatalf("owner = %+v, want %+v", m.Owner, wantOwner)
	}
}

func testInvalidSession(t *testing.T, s Store) {
	t.Helper()

	mustPerson(t, s, "prn-1")

	cases := map[string]func(ss *session.Session){
		"an ID without the prefix":             func(ss *session.Session) { ss.ID = "tok-1" },
		"no principal":                         func(ss *session.Session) { ss.PrincipalID = "" },
		"a provider that is not an identifier": func(ss *session.Session) { ss.Provider = "corp idp" },
		"an authentication after the creation": func(ss *session.Session) { ss.AuthTime = ss.CreatedAt.Add(time.Second) },
		"a last use apart from the creation":   func(ss *session.Session) { ss.LastSeenAt = ss.CreatedAt.Add(time.Second) },
		"an end time":                          func(ss *session.Session) { ss.EndedAt = ss.CreatedAt },
		"a user agent that is too long":        func(ss *session.Session) { ss.UserAgent = strings.Repeat("a", 513) },
		"a source IP that is not UTF-8":        func(ss *session.Session) { ss.SourceIP = "\xff" },
	}

	for name, change := range cases {
		ss := newSession("ses-1", "prn-1", at(5))
		change(&ss)

		err := s.CreateSession(t.Context(), ss, digest(1), entry("ses-1", at(5)))
		wantErr(t, "create with "+name, err, session.ErrInvalid)
	}

	_, err := s.GetSession(t.Context(), "ses-1")
	wantErr(t, "a refused session", err, session.ErrNotFound)
}

func testPrincipalRules(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPerson(t, s, "prn-gone")
	mustPrincipalAt(t, s, "prn-svc", principal.KindService, at(0))

	e := entry("ses-x", at(1))
	e.Object = principal.Ref("prn-gone")

	if err := s.DeletePrincipal(ctx, "prn-gone", e); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	for i, id := range []string{"prn-missing", "prn-gone", "prn-svc"} {
		ss := newSession("ses-"+strconv.Itoa(i), id, at(5))
		err := s.CreateSession(ctx, ss, digest(i), entry(ss.ID, at(5)))
		wantErr(t, "session of "+id, err, session.ErrPrincipalNotFound)
	}
}

func testDuplicate(t *testing.T, s Store) {
	t.Helper()

	mustPerson(t, s, "prn-1")
	mustSession(t, s, newSession("ses-1", "prn-1", at(5)), digest(1))

	err := s.CreateSession(t.Context(), newSession("ses-1", "prn-1", at(6)), digest(2), entry("ses-1", at(6)))
	wantErr(t, "duplicate ID", err, session.ErrAlreadyExists)

	err = s.CreateSession(t.Context(), newSession("ses-2", "prn-1", at(6)), digest(1), entry("ses-2", at(6)))
	wantErr(t, "duplicate digest", err, session.ErrAlreadyExists)
}

func testMissing(t *testing.T, s Store) {
	t.Helper()

	_, err := s.GetSession(t.Context(), "ses-none")
	wantErr(t, "get", err, session.ErrNotFound)

	_, err = s.GetSessionByDigest(t.Context(), digest(1))
	wantErr(t, "get by digest", err, session.ErrNotFound)

	wantErr(t, "end", s.EndSession(t.Context(), "ses-none", session.ReasonSignOut, entry("ses-none", at(5))),
		session.ErrNotFound)

	if err := s.TouchSession(t.Context(), "ses-none", at(5), time.Minute, noLimits()); err != nil {
		t.Fatalf("touch of a missing session: %v", err)
	}
}

func testDeletedOwner(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPerson(t, s, "prn-1")
	mustSession(t, s, newSession("ses-1", "prn-1", at(5)), digest(1))

	e := entry("ses-x", at(6))
	e.Object = principal.Ref("prn-1")

	if err := s.DeletePrincipal(ctx, "prn-1", e); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	_, err := s.GetSessionByDigest(ctx, digest(1))
	wantErr(t, "lookup of a session of a deleted owner", err, session.ErrNotFound)
}

func testDisabledOwner(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()
	now := testNow()
	old := now.Add(-3 * sweepPeriod)

	mustPrincipalAt(t, s, "prn-1", principal.KindPerson, old)
	mustSession(t, s, newSession("ses-1", "prn-1", old), digest(1))

	res := mustSweep(t, s)
	if !slices.Equal(res.Principals, []string{"prn-1"}) {
		t.Fatalf("sweep disabled %v, want [prn-1]: a sign-in before the cutoff is not activity", res.Principals)
	}

	m, err := s.GetSessionByDigest(ctx, digest(1))
	if err != nil {
		t.Fatalf("get by digest: %v", err)
	}

	if !m.Owner.DisabledAt.Equal(res.At) {
		t.Fatalf("owner disabled at = %s, want the sweep time %s", m.Owner.DisabledAt, res.At)
	}

	err = s.CreateSession(ctx, newSession("ses-2", "prn-1", now), digest(2), entry("ses-2", now))
	wantErr(t, "session of a disabled principal", err, session.ErrPrincipalDisabled)
}

func testTouch(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPerson(t, s, "prn-1")
	mustSession(t, s, newSession("ses-1", "prn-1", at(5)), digest(1))

	touch := func(when time.Time) {
		t.Helper()

		if err := s.TouchSession(ctx, "ses-1", when, time.Minute, noLimits()); err != nil {
			t.Fatalf("touch: %v", err)
		}
	}

	touch(at(5).Add(30 * time.Second))

	if got := mustGet(t, s).LastSeenAt; !got.Equal(at(5)) {
		t.Fatalf("last use after an early touch = %s, want %s", got, at(5))
	}

	touch(at(6))

	if got := mustGet(t, s).LastSeenAt; !got.Equal(at(6)) {
		t.Fatalf("last use after a touch = %s, want %s", got, at(6))
	}

	if err := s.EndSession(ctx, "ses-1", session.ReasonSignOut, entry("ses-1", at(7))); err != nil {
		t.Fatalf("end: %v", err)
	}

	touch(at(9))

	if got := mustGet(t, s).LastSeenAt; !got.Equal(at(6)) {
		t.Fatalf("last use after a touch of an ended session = %s, want %s", got, at(6))
	}
}

// testTouchKeepsAuthTime proves that a touch after the touch period
// records the last use and keeps the authentication time: the token
// freshness check reads it, so a use of the session must not make an old
// sign-in fresh.
func testTouchKeepsAuthTime(t *testing.T, s Store) {
	t.Helper()

	mustPerson(t, s, "prn-1")

	created := newSession("ses-1", "prn-1", at(5))
	mustSession(t, s, created, digest(1))

	if err := s.TouchSession(t.Context(), "ses-1", at(8), time.Minute, noLimits()); err != nil {
		t.Fatalf("touch: %v", err)
	}

	got := mustGet(t, s)
	if !got.LastSeenAt.Equal(at(8)) {
		t.Fatalf("last use after a touch = %s, want %s", got.LastSeenAt, at(8))
	}

	if !got.AuthTime.Equal(created.AuthTime) {
		t.Fatalf("authentication time after a touch = %s, want %s", got.AuthTime, created.AuthTime)
	}
}

func testEnd(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPerson(t, s, "prn-1")

	ss := newSession("ses-1", "prn-1", at(5))
	mustSession(t, s, ss, digest(1))

	wantErr(t, "end with an unknown reason", s.EndSession(ctx, "ses-1", "lost", entry("ses-1", at(6))),
		session.ErrInvalid)

	if err := s.EndSession(ctx, "ses-1", session.ReasonEnded, entry("ses-1", at(7))); err != nil {
		t.Fatalf("end: %v", err)
	}

	ss.EndedAt, ss.EndReason = at(7), session.ReasonEnded
	equalSession(t, mustGet(t, s), ss)

	wantErr(t, "second end", s.EndSession(ctx, "ses-1", session.ReasonSignOut, entry("ses-1", at(8))),
		session.ErrEnded)
	equalSession(t, mustGet(t, s), ss)
}

type auditLister interface {
	List(ctx context.Context, ns namespace.Name, page audit.Page) (audit.Listing, error)
}

func testChangeAppendsEntry(t *testing.T, s Store) {
	t.Helper()

	entries, ok := s.(auditLister)
	if !ok {
		t.Skip("the store keeps no audit log to read")
	}

	ctx := t.Context()

	mustPerson(t, s, "prn-1")
	mustSession(t, s, newSession("ses-1", "prn-1", at(5)), digest(1))

	if err := s.TouchSession(ctx, "ses-1", at(6), time.Minute, noLimits()); err != nil {
		t.Fatalf("touch: %v", err)
	}

	if err := s.EndSession(ctx, "ses-1", session.ReasonSignOut, entry("ses-1", at(7))); err != nil {
		t.Fatalf("end: %v", err)
	}

	listing, err := entries.List(ctx, audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	var objects []string

	for i := range listing.Entries {
		if strings.Contains(listing.Entries[i].Object, "#session:") {
			objects = append(objects, listing.Entries[i].Object)
		}
	}

	want := []string{session.FullRef("prn-1", "ses-1"), session.FullRef("prn-1", "ses-1")}
	if !slices.Equal(objects, want) {
		t.Fatalf("session entries = %v, want one for the create and one for the end, and none for the touch: %v",
			objects, want)
	}
}

func testRefusedEntryChangesNothing(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPerson(t, s, "prn-1")

	err := s.CreateSession(ctx, newSession("ses-x", "prn-1", at(5)), digest(9), refused("ses-x", at(5)))
	wantErr(t, "create with a refused entry", err, audit.ErrInvalidEntry)

	_, err = s.GetSession(ctx, "ses-x")
	wantErr(t, "session of a refused create", err, session.ErrNotFound)

	ss := newSession("ses-1", "prn-1", at(6))
	mustSession(t, s, ss, digest(1))

	wantErr(t, "end with a refused entry", s.EndSession(ctx, "ses-1", session.ReasonSignOut, refused("ses-1", at(7))),
		audit.ErrInvalidEntry)
	equalSession(t, mustGet(t, s), ss)
}

// testTouchKeepsInactiveSession proves that a touch never moves the last
// use of a session that is idle or expired at the time of the touch.
func testTouchKeepsInactiveSession(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPerson(t, s, "prn-1")
	mustSession(t, s, newSession("ses-1", "prn-1", at(0)), digest(1))
	mustSession(t, s, newSession("ses-2", "prn-1", at(0)), digest(2))

	touch := func(id string, when time.Time, l session.Limits) time.Time {
		t.Helper()

		if err := s.TouchSession(ctx, id, when, time.Minute, l); err != nil {
			t.Fatalf("touch %s: %v", id, err)
		}

		got, err := s.GetSession(ctx, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}

		return got.LastSeenAt
	}

	idle := session.Limits{Idle: 30 * time.Minute, MaxLength: 0}

	if got := touch("ses-1", at(30), idle); !got.Equal(at(0)) {
		t.Fatalf("last use after a touch of an idle session = %s, want %s", got, at(0))
	}

	if got := touch("ses-1", at(29), idle); !got.Equal(at(29)) {
		t.Fatalf("last use after a touch within the idle limit = %s, want %s", got, at(29))
	}

	short := session.Limits{Idle: 0, MaxLength: time.Hour}

	if got := touch("ses-2", at(60), short); !got.Equal(at(0)) {
		t.Fatalf("last use after a touch of an expired session = %s, want %s", got, at(0))
	}

	if got := touch("ses-2", at(59), short); !got.Equal(at(59)) {
		t.Fatalf("last use after a touch within the maximum length = %s, want %s", got, at(59))
	}
}

// testOwnerActivity proves that the lookup by digest answers the latest
// activity of the owner, from its sessions and its tokens, and its
// break-glass flag.
func testOwnerActivity(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()
	old := testNow().Add(-3 * sweepPeriod)
	recent := mustUseLater(t, s, "prn-1", old)

	m, err := s.GetSessionByDigest(ctx, digest(1))
	if err != nil {
		t.Fatalf("get by digest: %v", err)
	}

	if !m.Owner.ActiveAt.Equal(recent) || m.Owner.BreakGlass {
		t.Fatalf("owner active at %s, break glass %t; want %s and false", m.Owner.ActiveAt, m.Owner.BreakGlass, recent)
	}

	mustOldToken(t, s, "prn-1", old)

	used := recent.Add(time.Minute)
	if touchErr := s.TouchToken(ctx, "tok-1", used, time.Minute); touchErr != nil {
		t.Fatalf("touch token: %v", touchErr)
	}

	if m, err = s.GetSessionByDigest(ctx, digest(1)); err != nil || !m.Owner.ActiveAt.Equal(used) {
		t.Fatalf("owner active at %s, %v; want the token use %s", m.Owner.ActiveAt, err, used)
	}

	p := principal.Principal{
		CreatedAt: old, UpdatedAt: old, DisabledAt: time.Time{}, ID: "prn-bg", Subject: "subject of prn-bg",
		DisplayName: "Break glass", Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "",
		GroupCount: 0, BreakGlass: true,
	}

	e := entry("ses-x", old)
	e.Object = principal.Ref(p.ID)

	if createErr := s.CreatePrincipal(ctx, p, e); createErr != nil {
		t.Fatalf("create break-glass principal: %v", createErr)
	}

	mustSession(t, s, newSession("ses-bg", "prn-bg", old), digest(3))

	if m, err = s.GetSessionByDigest(ctx, digest(3)); err != nil || !m.Owner.BreakGlass {
		t.Fatalf("break-glass owner = %+v, %v; want the break-glass flag", m.Owner, err)
	}
}

// noLimits applies no idle limit and no maximum length to a touch.
func noLimits() session.Limits {
	return session.Limits{Idle: 0, MaxLength: 0}
}

// sweepPeriod is the inactivity period of the sweeps of the suite.
const sweepPeriod = time.Hour

// testNow returns the time of the test to the millisecond. The sweep runs
// at the time of the database clock, so its tests use real times.
func testNow() time.Time {
	return time.Now().UTC().Truncate(time.Millisecond)
}

// mustSweep runs an account sweep with sweepPeriod and sorts its result.
func mustSweep(t *testing.T, s Store) accesstoken.SweepResult {
	t.Helper()

	entryFn := func(when time.Time, object string) audit.Entry {
		e := entry("ses-x", when)
		e.Object = object

		return e
	}

	res, err := s.SweepInactive(t.Context(), accesstoken.Sweep{Inactivity: sweepPeriod, Tokens: false, Accounts: true},
		entryFn)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	slices.Sort(res.Principals)

	return res
}

func testSignInIsActivity(t *testing.T, s Store) {
	t.Helper()

	now := testNow()
	old := now.Add(-3 * sweepPeriod)

	mustPrincipalAt(t, s, "prn-idle", principal.KindPerson, old)
	mustPrincipalAt(t, s, "prn-signed-in", principal.KindPerson, old)

	// A sign-in after the cutoff, with no access token at all.
	mustSession(t, s, newSession("ses-1", "prn-signed-in", now.Add(-sweepPeriod/4)), digest(1))

	res := mustSweep(t, s)
	if !slices.Equal(res.Principals, []string{"prn-idle"}) {
		t.Fatalf("sweep disabled %v, want only prn-idle: a recent sign-in is activity", res.Principals)
	}
}

func testLastSignInNeverMovesBack(t *testing.T, s Store) {
	t.Helper()

	now := testNow()
	old := now.Add(-3 * sweepPeriod)

	mustPrincipalAt(t, s, "prn-1", principal.KindPerson, old)

	// A recent sign-in, then a session that a slow callback records with
	// an earlier time: the earlier time must not hide the recent sign-in.
	mustSession(t, s, newSession("ses-1", "prn-1", now.Add(-sweepPeriod/4)), digest(1))
	mustSession(t, s, newSession("ses-2", "prn-1", old), digest(2))

	if res := mustSweep(t, s); len(res.Principals) != 0 {
		t.Fatalf("sweep disabled %v, want none: the later sign-in counts", res.Principals)
	}
}

// mustUseLater makes the person id with a sign-in at old, then records a
// use of its session at a recent time, after the cutoff of mustSweep. The
// sign-in alone is inactivity; the use alone keeps the person active, for
// any maximum length of a session.
func mustUseLater(t *testing.T, s Store, id string, old time.Time) time.Time {
	t.Helper()

	recent := testNow().Add(-sweepPeriod / 4).Truncate(time.Millisecond)

	mustPrincipalAt(t, s, id, principal.KindPerson, old)
	mustSession(t, s, newSession("ses-1", id, old), digest(1))

	if err := s.TouchSession(t.Context(), "ses-1", recent, time.Minute, noLimits()); err != nil {
		t.Fatalf("touch: %v", err)
	}

	return recent
}

func testSessionUseIsActivity(t *testing.T, s Store) {
	t.Helper()

	old := testNow().Add(-3 * sweepPeriod)
	mustUseLater(t, s, "prn-1", old)

	if res := mustSweep(t, s); len(res.Principals) != 0 {
		t.Fatalf("sweep disabled %v, want none: a session use after the cutoff is activity", res.Principals)
	}
}

func testSessionUseInTokenLookup(t *testing.T, s Store) {
	t.Helper()

	old := testNow().Add(-3 * sweepPeriod)
	recent := mustUseLater(t, s, "prn-1", old)

	d := mustOldToken(t, s, "prn-1", old)

	m, err := s.GetTokenByDigest(t.Context(), d)
	if err != nil {
		t.Fatalf("token lookup: %v", err)
	}

	if !m.Owner.ActiveAt.Equal(recent) {
		t.Fatalf("owner active at = %s, want the session use %s", m.Owner.ActiveAt, recent)
	}
}

// testSessionUseKeepsDisable proves that a session use only feeds the
// activity time: a use after the disable does not enable the person, and a
// later sweep keeps the first disable time.
func testSessionUseKeepsDisable(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()
	old := testNow().Add(-3 * sweepPeriod)

	mustPrincipalAt(t, s, "prn-1", principal.KindPerson, old)
	mustSession(t, s, newSession("ses-1", "prn-1", old), digest(1))

	first := mustSweep(t, s)
	if !slices.Equal(first.Principals, []string{"prn-1"}) {
		t.Fatalf("first sweep disabled %v, want [prn-1]", first.Principals)
	}

	recent := testNow().Truncate(time.Millisecond)
	if err := s.TouchSession(ctx, "ses-1", recent, time.Minute, noLimits()); err != nil {
		t.Fatalf("touch: %v", err)
	}

	if again := mustSweep(t, s); len(again.Principals) != 0 {
		t.Fatalf("second sweep disabled %v, want none", again.Principals)
	}

	m, err := s.GetSessionByDigest(ctx, digest(1))
	if err != nil {
		t.Fatalf("get by digest: %v", err)
	}

	if !m.Owner.DisabledAt.Equal(first.At) {
		t.Fatalf("owner disabled at = %s after a session use, want the first sweep time %s",
			m.Owner.DisabledAt, first.At)
	}

	err = s.CreateSession(ctx, newSession("ses-2", "prn-1", recent), digest(2), entry("ses-2", recent))
	wantErr(t, "session of a disabled person after a session use", err, session.ErrPrincipalDisabled)

	// The token lookup shows the recent use as activity, and still the
	// disable time, which refuses the token before any activity check.
	d := mustOldToken(t, s, "prn-1", old)

	tm, err := s.GetTokenByDigest(ctx, d)
	if err != nil {
		t.Fatalf("token lookup: %v", err)
	}

	if !tm.Owner.DisabledAt.Equal(first.At) || !tm.Owner.ActiveAt.Equal(recent) {
		t.Fatalf("token owner disabled at %s, active at %s; want disabled at %s, active at %s",
			tm.Owner.DisabledAt, tm.Owner.ActiveAt, first.At, recent)
	}
}

// mustOldToken creates the personal token tok-1 of principalID, made at
// created and live for a day from now, and returns its digest.
func mustOldToken(t *testing.T, s Store, principalID string, created time.Time) accesstoken.Digest {
	t.Helper()

	tok := accesstoken.Token{
		CreatedAt: created, ExpiresAt: testNow().Add(24 * time.Hour), LastUsedAt: time.Time{},
		RevokedAt: time.Time{}, DisabledAt: time.Time{}, ID: "tok-1", PrincipalID: principalID, Name: "laptop",
		Kind: accesstoken.KindPersonal, ReplacedBy: "", CreatedBy: principalID,
	}
	d := accesstoken.Digest(digest(9))

	if err := s.CreateToken(t.Context(), tok, d, entry("tok-1", created)); err != nil {
		t.Fatalf("create token: %v", err)
	}

	return d
}

// newRequest returns a sign-in request made at created, that expires after
// session.RequestLifetime.
func newRequest(created time.Time) session.Request {
	return session.Request{
		CreatedAt:  created,
		ExpiresAt:  created.Add(session.RequestLifetime),
		Provider:   "corp-idp",
		Nonce:      "n-0S6_WzA2Mj-rG7k3QpL",
		Verifier:   "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk",
		ReturnPath: "/tokens?new=1",
		MaxAge:     0,
		HasMaxAge:  false,
	}
}

func equalRequest(t *testing.T, got, want session.Request) {
	t.Helper()

	same := got.CreatedAt.Equal(want.CreatedAt) && got.ExpiresAt.Equal(want.ExpiresAt) &&
		got.Provider == want.Provider && got.Nonce == want.Nonce && got.Verifier == want.Verifier &&
		got.ReturnPath == want.ReturnPath && got.MaxAge == want.MaxAge && got.HasMaxAge == want.HasMaxAge
	if !same {
		t.Fatalf("request = %+v, want %+v", got, want)
	}
}

func testRequestOnce(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()
	r := newRequest(at(5))

	if err := s.CreateRequest(ctx, r, digest(1)); err != nil {
		t.Fatalf("create request: %v", err)
	}

	got, err := s.TakeRequest(ctx, digest(1), at(6))
	if err != nil {
		t.Fatalf("take: %v", err)
	}

	equalRequest(t, got, r)

	_, err = s.TakeRequest(ctx, digest(1), at(6))
	wantErr(t, "second take", err, session.ErrNotFound)

	_, err = s.TakeRequest(ctx, digest(2), at(6))
	wantErr(t, "take of an unknown state", err, session.ErrNotFound)
}

func testRequestMaxAge(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	for i, c := range []struct {
		maxAge time.Duration
		has    bool
	}{
		{maxAge: 0, has: true},
		{maxAge: 5 * time.Minute, has: true},
		{maxAge: 0, has: false},
	} {
		r := newRequest(at(5))
		r.MaxAge, r.HasMaxAge = c.maxAge, c.has

		if err := s.CreateRequest(ctx, r, digest(10+i)); err != nil {
			t.Fatalf("create request with max_age %s (%t): %v", c.maxAge, c.has, err)
		}

		got, err := s.TakeRequest(ctx, digest(10+i), at(6))
		if err != nil {
			t.Fatalf("take: %v", err)
		}

		equalRequest(t, got, r)
	}

	r := newRequest(at(5))
	r.MaxAge = time.Minute

	wantErr(t, "a max_age that the request did not send", s.CreateRequest(ctx, r, digest(20)), session.ErrInvalid)
}

func testPolicyTokenFreshness(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	update := func(change func(*session.Policy)) session.Policy {
		t.Helper()

		stored, err := s.UpdateSessionPolicy(ctx, func(old session.Policy) (session.Policy, audit.Entry, error) {
			change(&old)
			old.UpdatedAt = at(5)

			return old, policyEntry(at(5)), nil
		})
		if err != nil {
			t.Fatalf("update policy: %v", err)
		}

		got, err := s.GetSessionPolicy(ctx)
		if err != nil || got != stored {
			t.Fatalf("policy after the update = %+v, %v; want %+v", got, err, stored)
		}

		return got
	}

	// A change of the limits only keeps the window of the server default.
	got := update(func(p *session.Policy) { p.Limits.Idle = 30 * time.Minute })
	if !got.TokenFreshnessDefault || got.TokenFreshness != session.DefaultTokenFreshness {
		t.Fatalf("window after a change of the limits = %s (default %t), want the default", got.TokenFreshness,
			got.TokenFreshnessDefault)
	}

	got = update(func(p *session.Policy) { p.TokenFreshness, p.TokenFreshnessDefault = 0, false })
	if got.TokenFreshnessDefault || got.TokenFreshness != 0 {
		t.Fatalf("window set to 0 = %s (default %t), want 0 that an administrator set", got.TokenFreshness,
			got.TokenFreshnessDefault)
	}

	got = update(func(p *session.Policy) { p.TokenFreshness, p.TokenFreshnessDefault = 30*time.Minute, false })
	if got.TokenFreshnessDefault || got.TokenFreshness != 30*time.Minute {
		t.Fatalf("window set to 30 minutes = %s (default %t)", got.TokenFreshness, got.TokenFreshnessDefault)
	}
}

func testBindProvider(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	bind := func(id, issuer string, when time.Time) string {
		t.Helper()

		recorded, err := s.BindProvider(ctx, id, issuer, when)
		if err != nil {
			t.Fatalf("bind %s to %s: %v", id, issuer, err)
		}

		return recorded
	}

	if got := bind("corp", "https://idp.example", at(5)); got != "https://idp.example" {
		t.Fatalf("first binding = %q, want the issuer", got)
	}

	if got := bind("corp", "https://idp.example", at(6)); got != "https://idp.example" {
		t.Fatalf("second binding with the same issuer = %q", got)
	}

	if got := bind("corp", "https://other.example", at(7)); got != "https://idp.example" {
		t.Fatalf("binding with another issuer = %q, want the first issuer", got)
	}

	if got := bind("partner", "https://other.example", at(7)); got != "https://other.example" {
		t.Fatalf("binding of another provider ID = %q, want its own issuer", got)
	}
}

func testRequestExpired(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()
	r := newRequest(at(5))

	if err := s.CreateRequest(ctx, r, digest(1)); err != nil {
		t.Fatalf("create request: %v", err)
	}

	_, err := s.TakeRequest(ctx, digest(1), r.ExpiresAt)
	wantErr(t, "take at the expiry", err, session.ErrNotFound)

	_, err = s.TakeRequest(ctx, digest(1), at(6))
	wantErr(t, "take after a take of the expired request", err, session.ErrNotFound)
}

func testInvalidRequest(t *testing.T, s Store) {
	t.Helper()

	cases := map[string]func(r *session.Request){
		"a provider that is not an identifier": func(r *session.Request) { r.Provider = "" },
		"a short nonce":                        func(r *session.Request) { r.Nonce = "abc" },
		"a short verifier":                     func(r *session.Request) { r.Verifier = "short" },
		"a verifier with a space":              func(r *session.Request) { r.Verifier = strings.Repeat("a", 42) + " " },
		"a return path to another host":        func(r *session.Request) { r.ReturnPath = "//evil.example/" },
		"an absolute return URL":               func(r *session.Request) { r.ReturnPath = "https://evil.example/" },
		"a return path with a backslash":       func(r *session.Request) { r.ReturnPath = "/\\evil.example" },
		"an expiry at the creation":            func(r *session.Request) { r.ExpiresAt = r.CreatedAt },
	}

	for name, change := range cases {
		r := newRequest(at(5))
		change(&r)

		wantErr(t, "create with "+name, s.CreateRequest(t.Context(), r, digest(1)), session.ErrInvalid)
	}

	_, err := s.TakeRequest(t.Context(), digest(1), at(6))
	wantErr(t, "take after refused creates", err, session.ErrNotFound)
}

func testDuplicateRequest(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	if err := s.CreateRequest(ctx, newRequest(at(5)), digest(1)); err != nil {
		t.Fatalf("create request: %v", err)
	}

	wantErr(t, "duplicate state digest", s.CreateRequest(ctx, newRequest(at(6)), digest(1)), session.ErrAlreadyExists)
}

func testPruneRequests(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	for i, created := range []time.Time{at(0), at(1), at(20)} {
		if err := s.CreateRequest(ctx, newRequest(created), digest(i)); err != nil {
			t.Fatalf("create request %d: %v", i, err)
		}
	}

	// At minute 11, the requests of minutes 0 and 1 expired.
	n, err := s.PruneRequests(ctx, at(11))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	if n != 2 {
		t.Fatalf("prune deleted %d requests, want 2", n)
	}

	if _, err := s.TakeRequest(ctx, digest(2), at(21)); err != nil {
		t.Fatalf("take of the request that did not expire: %v", err)
	}
}

func testConcurrentTakes(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	if err := s.CreateRequest(ctx, newRequest(at(5)), digest(1)); err != nil {
		t.Fatalf("create request: %v", err)
	}

	const takers = 4

	errs := make([]error, takers)

	var wg sync.WaitGroup

	for i := range takers {
		wg.Go(func() {
			_, errs[i] = s.TakeRequest(ctx, digest(1), at(6))
		})
	}

	wg.Wait()

	won := 0

	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, session.ErrNotFound):
		default:
			t.Fatalf("take %d: %v", i, err)
		}
	}

	if won != 1 {
		t.Fatalf("%d takes won, want exactly 1", won)
	}
}

// policyEntry returns a valid audit entry of a change of the session
// policy, as a service builds it.
func policyEntry(when time.Time) audit.Entry {
	e := entry("", when)
	e.Action = "server:manage-sign-in"
	e.Object = session.PolicyRef

	return e
}

func testPolicy(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	got, err := s.GetSessionPolicy(ctx)
	if err != nil || got != session.DefaultPolicy() {
		t.Fatalf("seeded policy = %+v, %v; want %+v", got, err, session.DefaultPolicy())
	}

	want := session.Policy{
		UpdatedAt: at(5), Limits: session.Limits{Idle: 30 * time.Minute, MaxLength: 8 * time.Hour},
		TokenFreshness: 0, TokenFreshnessDefault: false,
	}

	stored, err := s.UpdateSessionPolicy(ctx, func(old session.Policy) (session.Policy, audit.Entry, error) {
		if old != session.DefaultPolicy() {
			t.Errorf("update read %+v, want the seeded policy", old)
		}

		return want, policyEntry(at(5)), nil
	})
	if err != nil || stored != want {
		t.Fatalf("update policy = %+v, %v; want %+v", stored, err, want)
	}

	changed := want
	changed.Limits.Idle = time.Hour

	_, err = s.UpdateSessionPolicy(ctx, func(session.Policy) (session.Policy, audit.Entry, error) {
		e := policyEntry(at(6))
		e.Subject = ""

		return changed, e, nil
	})
	wantErr(t, "update with a refused entry", err, audit.ErrInvalidEntry)

	_, err = s.UpdateSessionPolicy(ctx, func(session.Policy) (session.Policy, audit.Entry, error) {
		return changed, policyEntry(at(6)), session.ErrInvalid
	})
	wantErr(t, "update that refuses the change", err, session.ErrInvalid)

	got, err = s.GetSessionPolicy(ctx)
	if err != nil || got != want {
		t.Fatalf("policy = %+v, %v; want %+v", got, err, want)
	}

	if changes, ok := policyEntries(t, s); ok && changes != 1 {
		t.Fatalf("%d entries of %s, want 1: a refused change appends no entry", changes, session.PolicyRef)
	}
}

// policyEntries counts the audit entries of @server whose object is the
// session policy. It reports false when the store keeps no audit log to
// read.
func policyEntries(t *testing.T, s Store) (int, bool) {
	t.Helper()

	entries, ok := s.(auditLister)
	if !ok {
		return 0, false
	}

	listing, err := entries.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	var changes int

	for i := range listing.Entries {
		if listing.Entries[i].Object == session.PolicyRef {
			changes++
		}
	}

	return changes, true
}

func testConcurrentPolicyUpdates(t *testing.T, s Store) {
	t.Helper()

	const updates = 8

	var wg sync.WaitGroup

	errs := make(chan error, updates)

	for range updates {
		wg.Go(func() {
			_, err := s.UpdateSessionPolicy(
				t.Context(),
				func(old session.Policy) (session.Policy, audit.Entry, error) {
					old.Limits.MaxLength += time.Hour

					return old, policyEntry(at(7)), nil
				},
			)
			errs <- err
		})
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("update: %v", err)
		}
	}

	got, err := s.GetSessionPolicy(t.Context())
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}

	if want := session.DefaultMaxLength + updates*time.Hour; got.Limits.MaxLength != want {
		t.Fatalf("maximum length = %s, want %s: an update did not see the one before it", got.Limits.MaxLength, want)
	}
}
