// Package accesstokentest is the conformance suite for accesstoken.Store.
// Every built-in store runs it, and a custom store passed to
// dustpan.NewServer should too:
//
//	func TestAccessTokenConformance(t *testing.T) {
//		t.Parallel()
//		accesstokentest.Run(t, func(t *testing.T) accesstokentest.Store { return newMyStore(t) })
//	}
package accesstokentest

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
)

// Store is what the suite needs: the token port, and the principal records
// that own the tokens.
type Store interface {
	accesstoken.Store
	principal.RecordStore
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
		{name: "token round trip", test: testRoundTrip},
		{name: "a token needs a principal that exists", test: testMissingPrincipal},
		{name: "duplicate ID or digest is ErrAlreadyExists", test: testDuplicate},
		{name: "missing token is ErrNotFound", test: testMissingToken},
		{name: "lookup by digest answers the token, its owner and the digest", test: testByDigest},
		{name: "a deleted owner hides its tokens from the lookup", test: testDeletedOwner},
		{name: "revoke sets the revoke time once", test: testRevoke},
		{name: "rotation ends the old token and issues the new one", test: testRotate},
		{name: "rotation of an ended token is ErrEnded", test: testRotateEnded},
		{name: "a failed rotation changes nothing", test: testRotateRollsBack},
		{name: "touch records the last use at most once for each period", test: testTouch},
		{name: "list orders newest first and filters", test: testList},
		{name: "list pages with a cursor", test: testListPages},
		{name: "a change appends its audit entry in @server", test: testChangeAppendsEntry},
		{name: "an entry that the audit log refuses changes nothing", test: testRefusedEntryChangesNothing},
		{name: "concurrent rotations of one token: one wins", test: testConcurrentRotations},
		{name: "the policy starts at the defaults and keeps a change", test: testPolicy},
		{name: "the sweep disables inactive tokens", test: testSweepTokens},
		{name: "the sweep disables inactive user accounts", test: testSweepAccounts},
		{name: "a sweep with a refused entry changes nothing", test: testSweepRefused},
		{name: "enable, then sweep: the principal stays enabled", test: testEnablePrincipal},
		{name: "concurrent policy updates: each sees the one before", test: testConcurrentPolicyUpdates},
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
	return time.Date(2026, time.October, 14, 9, minute, 0, 0, time.UTC)
}

// entry returns a valid audit entry of the kind change in the scope
// @server for the token id, as a Service builds it.
func entry(id string) audit.Entry {
	return audit.Entry{
		At:         at(9),
		Namespace:  audit.ServerScope,
		Subject:    "admin",
		IDPSubject: "",
		TokenID:    "",
		Action:     "server:manage-tokens",
		Object:     accesstoken.Ref(id),
		Version:    "",
		ValuePath:  "",
		ValueCount: nil,
		Changes:    nil,
		SourceIP:   "192.0.2.1",
		UserAgent:  "accesstokentest",
		Channel:    audit.ChannelAPI,
		Kind:       audit.KindChange,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  "",
		ObjectID:   "",
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	}
}

// entryAt returns entry(id) at the time when.
func entryAt(id string, when time.Time) audit.Entry {
	e := entry(id)
	e.At = when

	return e
}

// refused returns an entry that fails validation: it names no subject.
func refused(id string) audit.Entry {
	e := entry(id)
	e.Subject = ""

	return e
}

// digest returns a distinct digest for n.
func digest(n int) accesstoken.Digest {
	return sha256.Sum256([]byte(strconv.Itoa(n)))
}

// newToken returns a token of principalID made at minute created, that
// expires a day later.
func newToken(id, principalID string, created int) accesstoken.Token {
	return accesstoken.Token{
		CreatedAt:   at(created),
		ExpiresAt:   at(created).Add(24 * time.Hour),
		LastUsedAt:  time.Time{},
		RevokedAt:   time.Time{},
		DisabledAt:  time.Time{},
		ID:          id,
		PrincipalID: principalID,
		Name:        "token " + id,
		Kind:        accesstoken.KindService,
		ReplacedBy:  "",
		CreatedBy:   "admin",
	}
}

// mustPrincipal creates the principal id with the subject subject.
func mustPrincipal(t *testing.T, s Store, id, subject string, kind principal.Kind) {
	t.Helper()

	p := principal.Principal{
		CreatedAt: at(
			0,
		),
		UpdatedAt:   at(0),
		DisabledAt:  time.Time{},
		ID:          id,
		Subject:     subject,
		DisplayName: "Name of " + subject,
		Kind:        kind,
		Source:      principal.SourceLocal,
		Namespace:   "", GroupCount: 0,
	}

	e := entry("tok-x")
	e.Object = principal.Ref(id)

	if err := s.CreatePrincipal(t.Context(), p, e); err != nil {
		t.Fatalf("create principal %q: %v", id, err)
	}
}

// mustToken creates tok with the digest d.
func mustToken(t *testing.T, s Store, tok accesstoken.Token, d accesstoken.Digest) {
	t.Helper()

	if err := s.CreateToken(t.Context(), tok, d, entryAt(tok.ID, tok.CreatedAt)); err != nil {
		t.Fatalf("create token %q: %v", tok.ID, err)
	}
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want %v", what, err, want)
	}
}

func equalToken(t *testing.T, got, want accesstoken.Token) {
	t.Helper()

	same := got.CreatedAt.Equal(want.CreatedAt) && got.ExpiresAt.Equal(want.ExpiresAt) &&
		got.LastUsedAt.Equal(want.LastUsedAt) && got.RevokedAt.Equal(want.RevokedAt) &&
		got.DisabledAt.Equal(want.DisabledAt) &&
		got.ID == want.ID && got.PrincipalID == want.PrincipalID && got.Name == want.Name &&
		got.Kind == want.Kind && got.ReplacedBy == want.ReplacedBy && got.CreatedBy == want.CreatedBy

	if !same {
		t.Fatalf("token = %+v, want %+v", got, want)
	}
}

func mustGet(t *testing.T, s Store, id string) accesstoken.Token {
	t.Helper()

	got, err := s.GetToken(t.Context(), id)
	if err != nil {
		t.Fatalf("get token %q: %v", id, err)
	}

	return got
}

func ids(tokens []accesstoken.Token) []string {
	out := make([]string, 0, len(tokens))
	for i := range tokens {
		out = append(out, tokens[i].ID)
	}

	return out
}

func testRoundTrip(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, "prn-1", "alice", principal.KindPerson)

	tok := newToken("tok-1", "prn-1", 1)
	tok.Kind = accesstoken.KindPersonal
	tok.Name = "laptop: ünïcode"
	mustToken(t, s, tok, digest(1))

	equalToken(t, mustGet(t, s, "tok-1"), tok)
}

func testMissingPrincipal(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	err := s.CreateToken(ctx, newToken("tok-1", "prn-none", 1), digest(1), entry("tok-1"))
	wantErr(t, "create for a missing principal", err, accesstoken.ErrPrincipalNotFound)

	mustPrincipal(t, s, "prn-1", "alice", principal.KindService)

	if delErr := s.DeletePrincipal(ctx, "prn-1", entry("tok-1")); delErr != nil {
		t.Fatalf("delete principal: %v", delErr)
	}

	err = s.CreateToken(ctx, newToken("tok-1", "prn-1", 1), digest(1), entry("tok-1"))
	wantErr(t, "create for a deleted principal", err, accesstoken.ErrPrincipalNotFound)

	_, err = s.GetToken(ctx, "tok-1")
	wantErr(t, "token of a refused create", err, accesstoken.ErrNotFound)
}

func testDuplicate(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, "prn-1", "alice", principal.KindService)
	mustToken(t, s, newToken("tok-1", "prn-1", 1), digest(1))

	err := s.CreateToken(t.Context(), newToken("tok-1", "prn-1", 2), digest(2), entry("tok-1"))
	wantErr(t, "duplicate ID", err, accesstoken.ErrAlreadyExists)

	err = s.CreateToken(t.Context(), newToken("tok-2", "prn-1", 2), digest(1), entry("tok-2"))
	wantErr(t, "duplicate digest", err, accesstoken.ErrAlreadyExists)
}

func testMissingToken(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	_, err := s.GetToken(ctx, "tok-none")
	wantErr(t, "get", err, accesstoken.ErrNotFound)

	_, err = s.GetTokenByDigest(ctx, digest(7))
	wantErr(t, "get by digest", err, accesstoken.ErrNotFound)

	wantErr(t, "revoke", s.RevokeToken(ctx, "tok-none", entry("tok-none")), accesstoken.ErrNotFound)

	err = s.RotateToken(
		ctx,
		accesstoken.Replacement{
			OldEndsAt: at(10),
			OldID:     "tok-none",
			Next:      newToken("tok-2", "prn-1", 9),
			Digest:    digest(2),
		},
		entry("tok-none"),
	)
	wantErr(t, "rotate", err, accesstoken.ErrNotFound)

	if err := s.TouchToken(ctx, "tok-none", at(10), time.Minute); err != nil {
		t.Fatalf("touch of a missing token: %v, want nil", err)
	}
}

func testByDigest(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, "prn-1", "ci-deploy", principal.KindService)
	tok := newToken("tok-1", "prn-1", 1)
	mustToken(t, s, tok, digest(1))
	mustToken(t, s, newToken("tok-2", "prn-1", 2), digest(2))

	m, err := s.GetTokenByDigest(t.Context(), digest(1))
	if err != nil {
		t.Fatalf("get by digest: %v", err)
	}

	equalToken(t, m.Token, tok)

	if !m.Digest.Equal(digest(1)) {
		t.Fatalf("stored digest = %s, want %s", m.Digest, digest(1))
	}

	// The latest activity is the creation of tok-2: the principal was made
	// earlier, and no token was used.
	want := accesstoken.Owner{
		ActiveAt:    at(2),
		DisabledAt:  time.Time{},
		ID:          "prn-1",
		Subject:     "ci-deploy",
		DisplayName: "Name of ci-deploy",
		Kind:        principal.KindService,
	}
	if m.Owner != want {
		t.Fatalf("owner = %+v, want %+v", m.Owner, want)
	}
}

func testDeletedOwner(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPrincipal(t, s, "prn-1", "ci-deploy", principal.KindService)
	mustToken(t, s, newToken("tok-1", "prn-1", 1), digest(1))

	if err := s.DeletePrincipal(ctx, "prn-1", entry("tok-1")); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	_, err := s.GetTokenByDigest(ctx, digest(1))
	wantErr(t, "lookup of a token of a deleted principal", err, accesstoken.ErrNotFound)

	// The record stays for the history.
	mustGet(t, s, "tok-1")
}

func testRevoke(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPrincipal(t, s, "prn-1", "alice", principal.KindService)
	tok := newToken("tok-1", "prn-1", 1)
	mustToken(t, s, tok, digest(1))

	if err := s.RevokeToken(ctx, "tok-1", entryAt("tok-1", at(5))); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	tok.RevokedAt = at(5)
	equalToken(t, mustGet(t, s, "tok-1"), tok)

	wantErr(t, "second revoke", s.RevokeToken(ctx, "tok-1", entryAt("tok-1", at(6))), accesstoken.ErrEnded)
	equalToken(t, mustGet(t, s, "tok-1"), tok)
}

func testRotate(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPrincipal(t, s, "prn-1", "alice", principal.KindService)
	old := newToken("tok-1", "prn-1", 1)
	mustToken(t, s, old, digest(1))

	next := newToken("tok-2", "prn-1", 5)
	r := accesstoken.Replacement{OldEndsAt: at(30), OldID: "tok-1", Next: next, Digest: digest(2)}
	if err := s.RotateToken(ctx, r, entryAt("tok-1", at(5))); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	old.ExpiresAt = at(30)
	old.ReplacedBy = "tok-2"
	equalToken(t, mustGet(t, s, "tok-1"), old)
	equalToken(t, mustGet(t, s, "tok-2"), next)

	// An end time after the expiry keeps the expiry.
	third := newToken("tok-3", "prn-1", 6)
	late := next.ExpiresAt.Add(time.Hour)

	r = accesstoken.Replacement{OldEndsAt: late, OldID: "tok-2", Next: third, Digest: digest(3)}
	if err := s.RotateToken(ctx, r, entryAt("tok-2", at(6))); err != nil {
		t.Fatalf("rotate with a late end: %v", err)
	}

	next.ReplacedBy = "tok-3"
	equalToken(t, mustGet(t, s, "tok-2"), next)
}

func testRotateEnded(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPrincipal(t, s, "prn-1", "alice", principal.KindService)

	mustToken(t, s, newToken("tok-rev", "prn-1", 1), digest(1))

	if err := s.RevokeToken(ctx, "tok-rev", entryAt("tok-rev", at(2))); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	mustToken(t, s, newToken("tok-done", "prn-1", 1), digest(2))

	err := s.RotateToken(
		ctx,
		accesstoken.Replacement{
			OldEndsAt: at(10),
			OldID:     "tok-done",
			Next:      newToken("tok-a", "prn-1", 3),
			Digest:    digest(3),
		},
		entryAt("tok-done", at(3)),
	)
	if err != nil {
		t.Fatalf("first rotation: %v", err)
	}

	expired := newToken("tok-exp", "prn-1", 1)
	expired.ExpiresAt = at(4)
	mustToken(t, s, expired, digest(4))

	cases := []struct{ id, next string }{{"tok-rev", "tok-b"}, {"tok-done", "tok-c"}, {"tok-exp", "tok-d"}}
	for i, tc := range cases {
		err := s.RotateToken(
			ctx,
			accesstoken.Replacement{
				OldEndsAt: at(20),
				OldID:     tc.id,
				Next:      newToken(tc.next, "prn-1", 5),
				Digest:    digest(10 + i),
			},
			entryAt(tc.id, at(5)),
		)
		wantErr(t, "rotate "+tc.id, err, accesstoken.ErrEnded)

		_, err = s.GetToken(ctx, tc.next)
		wantErr(t, "token of a refused rotation", err, accesstoken.ErrNotFound)
	}
}

func testRotateRollsBack(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPrincipal(t, s, "prn-1", "alice", principal.KindService)
	old := newToken("tok-1", "prn-1", 1)
	mustToken(t, s, old, digest(1))
	mustToken(t, s, newToken("tok-taken", "prn-1", 1), digest(2))

	err := s.RotateToken(
		ctx,
		accesstoken.Replacement{
			OldEndsAt: at(10),
			OldID:     "tok-1",
			Next:      newToken("tok-2", "prn-1", 5),
			Digest:    digest(2),
		},
		entryAt("tok-1", at(5)),
	)
	wantErr(t, "rotate to a taken digest", err, accesstoken.ErrAlreadyExists)

	equalToken(t, mustGet(t, s, "tok-1"), old)

	err = s.RotateToken(
		ctx,
		accesstoken.Replacement{
			OldEndsAt: at(10),
			OldID:     "tok-1",
			Next:      newToken("tok-2", "prn-1", 5),
			Digest:    digest(3),
		},
		refused("tok-1"),
	)
	wantErr(t, "rotate with a refused entry", err, audit.ErrInvalidEntry)

	equalToken(t, mustGet(t, s, "tok-1"), old)

	_, err = s.GetToken(ctx, "tok-2")
	wantErr(t, "new token of a failed rotation", err, accesstoken.ErrNotFound)
}

func testTouch(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPrincipal(t, s, "prn-1", "alice", principal.KindService)
	tok := newToken("tok-1", "prn-1", 1)
	mustToken(t, s, tok, digest(1))

	steps := []struct {
		touch time.Time
		want  time.Time
	}{
		{at(2), at(2)},
		{at(2).Add(30 * time.Second), at(2)},
		{at(3), at(3)},
		{at(2), at(3)},
	}
	for _, step := range steps {
		if err := s.TouchToken(ctx, "tok-1", step.touch, time.Minute); err != nil {
			t.Fatalf("touch at %s: %v", step.touch, err)
		}

		tok.LastUsedAt = step.want
		equalToken(t, mustGet(t, s, "tok-1"), tok)
	}
}

// listQuery returns a query for every row of principalID.
func listQuery(principalID string) accesstoken.Query {
	return accesstoken.Query{
		At: at(10), AfterCreatedAt: time.Time{}, PrincipalID: principalID, AfterID: "",
		Filter: accesstoken.FilterAll, Limit: accesstoken.MaxLimit + 1, HasAfter: false,
	}
}

func mustList(t *testing.T, s Store, q accesstoken.Query) ([]string, int64) {
	t.Helper()

	rows, total, err := s.ListTokens(t.Context(), q)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	return ids(rows), total
}

func wantList(t *testing.T, s Store, q accesstoken.Query, want []string, wantTotal int64) {
	t.Helper()

	got, total := mustList(t, s, q)
	if !slices.Equal(got, want) || total != wantTotal {
		t.Fatalf("list %+v = %q, total %d; want %q, total %d", q, got, total, want, wantTotal)
	}
}

func testList(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPrincipal(t, s, "prn-1", "alice", principal.KindService)
	mustPrincipal(t, s, "prn-2", "bob", principal.KindService)

	mustToken(t, s, newToken("tok-a", "prn-1", 1), digest(1))
	mustToken(t, s, newToken("tok-b", "prn-2", 2), digest(2))
	long := newToken("tok-c", "prn-1", 3)
	long.ExpiresAt = at(3).Add(30 * 24 * time.Hour)
	mustToken(t, s, long, digest(3))
	mustToken(t, s, newToken("tok-d", "prn-1", 3), digest(4))

	expired := newToken("tok-e", "prn-1", 4)
	expired.ExpiresAt = at(5)
	mustToken(t, s, expired, digest(5))

	if err := s.RevokeToken(ctx, "tok-a", entryAt("tok-a", at(6))); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	wantList(t, s, listQuery(""), []string{"tok-e", "tok-d", "tok-c", "tok-b", "tok-a"}, 5)
	wantList(t, s, listQuery("prn-1"), []string{"tok-e", "tok-d", "tok-c", "tok-a"}, 4)

	// At minute 10: tok-a is revoked and tok-e expired. tok-d expires in a
	// day, so it is expiring, and tok-c in 30 days.
	for _, tc := range []struct {
		filter accesstoken.Filter
		want   []string
	}{
		{accesstoken.FilterActive, []string{"tok-d", "tok-c"}},
		{accesstoken.FilterExpiring, []string{"tok-d"}},
		{accesstoken.FilterEnded, []string{"tok-e", "tok-a"}},
	} {
		q := listQuery("prn-1")
		q.Filter = tc.filter
		wantList(t, s, q, tc.want, int64(len(tc.want)))
	}

	wantList(t, s, listQuery("prn-none"), []string{}, 0)
}

func testListPages(t *testing.T, s Store) {
	t.Helper()

	mustPrincipal(t, s, "prn-1", "alice", principal.KindService)

	for i, id := range []string{"tok-a", "tok-b", "tok-c", "tok-d", "tok-e"} {
		mustToken(t, s, newToken(id, "prn-1", 1+i/2), digest(i+1))
	}

	// Newest first: tok-e at minute 3; tok-d and tok-c at minute 2, larger ID
	// first; tok-b and tok-a at minute 1.
	q := listQuery("")
	q.Limit = 2
	wantList(t, s, q, []string{"tok-e", "tok-d"}, 5)

	q.HasAfter, q.AfterCreatedAt, q.AfterID = true, at(2), "tok-d"
	wantList(t, s, q, []string{"tok-c", "tok-b"}, 5)

	q.AfterCreatedAt, q.AfterID = at(1), "tok-b"
	wantList(t, s, q, []string{"tok-a"}, 5)
}

// auditLister reads the audit log of a store that keeps one.
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

	mustPrincipal(t, s, "prn-1", "alice", principal.KindService)
	mustToken(t, s, newToken("tok-1", "prn-1", 1), digest(1))

	r := accesstoken.Replacement{
		OldEndsAt: at(20),
		OldID:     "tok-1",
		Next:      newToken("tok-2", "prn-1", 2),
		Digest:    digest(2),
	}
	if err := s.RotateToken(ctx, r, entryAt("tok-1", at(2))); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	if err := s.RevokeToken(ctx, "tok-2", entryAt("tok-2", at(3))); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if err := s.TouchToken(ctx, "tok-1", at(4), time.Minute); err != nil {
		t.Fatalf("touch: %v", err)
	}

	listing, err := entries.List(ctx, audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	objects := make([]string, 0, len(listing.Entries))
	for i := range listing.Entries {
		objects = append(objects, listing.Entries[i].Object)
	}

	slices.Sort(objects)

	// The principal, the create, the rotation and the revoke. A touch writes
	// no entry.
	want := []string{"principal:prn-1", "token:tok-1", "token:tok-1", "token:tok-2"}
	if !slices.Equal(objects, want) {
		t.Fatalf("entry objects = %q, want %q", objects, want)
	}
}

func testRefusedEntryChangesNothing(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPrincipal(t, s, "prn-1", "alice", principal.KindService)

	err := s.CreateToken(ctx, newToken("tok-x", "prn-1", 1), digest(9), refused("tok-x"))
	wantErr(t, "create with a refused entry", err, audit.ErrInvalidEntry)

	_, err = s.GetToken(ctx, "tok-x")
	wantErr(t, "token of a refused create", err, accesstoken.ErrNotFound)

	tok := newToken("tok-1", "prn-1", 1)
	mustToken(t, s, tok, digest(1))

	wantErr(t, "revoke with a refused entry", s.RevokeToken(ctx, "tok-1", refused("tok-1")), audit.ErrInvalidEntry)
	equalToken(t, mustGet(t, s, "tok-1"), tok)
}

func testConcurrentRotations(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPrincipal(t, s, "prn-1", "alice", principal.KindService)
	mustToken(t, s, newToken("tok-1", "prn-1", 1), digest(1))

	const writers = 4

	errs := make([]error, writers)

	var wg sync.WaitGroup

	for i := range writers {
		wg.Go(func() {
			next := newToken("tok-n"+strconv.Itoa(i), "prn-1", 2)
			errs[i] = s.RotateToken(
				ctx,
				accesstoken.Replacement{OldEndsAt: at(20), OldID: "tok-1", Next: next, Digest: digest(10 + i)},
				entryAt("tok-1", at(2)),
			)
		})
	}

	wg.Wait()

	won := 0

	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, accesstoken.ErrEnded):
		default:
			t.Fatalf("rotation %d: %v, want nil or ErrEnded", i, err)
		}
	}

	if won != 1 {
		t.Fatalf("%d rotations won, want 1", won)
	}
}

func testPolicy(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	got, err := s.GetPolicy(ctx)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}

	if got != accesstoken.DefaultPolicy() {
		t.Fatalf("seeded policy = %+v, want %+v", got, accesstoken.DefaultPolicy())
	}

	want := accesstoken.Policy{
		UpdatedAt: at(5), MaxLifetime: 30 * 24 * time.Hour, Inactivity: 0,
		DisableTokens: false, DisableAccounts: true, DisableBreakGlass: true,
	}

	stored, err := s.UpdatePolicy(ctx, func(old accesstoken.Policy) (accesstoken.Policy, audit.Entry, error) {
		if old != accesstoken.DefaultPolicy() {
			t.Errorf("update read %+v, want the seeded policy", old)
		}

		return want, entry("tok-x"), nil
	})
	if err != nil || stored != want {
		t.Fatalf("update policy = %+v, %v; want %+v", stored, err, want)
	}

	changed := want
	changed.Inactivity = time.Hour

	_, err = s.UpdatePolicy(ctx, func(accesstoken.Policy) (accesstoken.Policy, audit.Entry, error) {
		return changed, refused("tok-x"), nil
	})
	wantErr(t, "update with a refused entry", err, audit.ErrInvalidEntry)

	_, err = s.UpdatePolicy(ctx, func(accesstoken.Policy) (accesstoken.Policy, audit.Entry, error) {
		return changed, entry("tok-x"), accesstoken.ErrInvalid
	})
	wantErr(t, "update that refuses the change", err, accesstoken.ErrInvalid)

	got, err = s.GetPolicy(ctx)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}

	if got != want {
		t.Fatalf("policy = %+v, want %+v", got, want)
	}
}

func testConcurrentPolicyUpdates(t *testing.T, s Store) {
	t.Helper()

	const updates = 8

	var wg sync.WaitGroup

	errs := make(chan error, updates)

	for range updates {
		wg.Go(func() {
			_, err := s.UpdatePolicy(
				t.Context(),
				func(old accesstoken.Policy) (accesstoken.Policy, audit.Entry, error) {
					old.MaxLifetime += time.Hour

					return old, entry("tok-x"), nil
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

	got, err := s.GetPolicy(t.Context())
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}

	if want := accesstoken.DefaultMaxLifetime + updates*time.Hour; got.MaxLifetime != want {
		t.Fatalf("maximum lifetime = %s, want %s: an update did not see the one before it", got.MaxLifetime, want)
	}
}

// The sweep reads the time of the database clock, so its cases place each
// record relative to the time of the test, with an inactivity period of
// one hour: "old" is before the cutoff and "recent" is after it.
const sweepPeriod = time.Hour

// clockSkew bounds the difference between the clock of the test and the
// database clock.
const clockSkew = time.Minute

// testNow returns the time of the test to the millisecond.
func testNow() time.Time {
	return time.Now().UTC().Truncate(time.Millisecond)
}

// sweepTimes is the time of a sweep test, a time well before the cutoff and
// a time well after it.
type sweepTimes struct {
	now, old, recent time.Time
}

func newSweepTimes() sweepTimes {
	now := testNow()

	return sweepTimes{now: now, old: now.Add(-3 * sweepPeriod), recent: now.Add(-sweepPeriod / 4)}
}

// relToken returns a service token of principalID made at created, that
// expires a day after the test.
func relToken(id, principalID string, created time.Time, ts sweepTimes) accesstoken.Token {
	tok := newToken(id, principalID, 0)
	tok.CreatedAt, tok.ExpiresAt = created, ts.now.Add(24*time.Hour)

	return tok
}

// mustPrincipalAt creates the principal id, made at created.
func mustPrincipalAt(t *testing.T, s Store, id string, kind principal.Kind, created time.Time) {
	t.Helper()

	p := principal.Principal{
		CreatedAt: created, UpdatedAt: created, DisabledAt: time.Time{}, ID: id, Subject: id,
		DisplayName: "Name of " + id, Kind: kind, Source: principal.SourceLocal, Namespace: "", GroupCount: 0,
	}

	e := entryAt("tok-x", created)
	e.Object = principal.Ref(id)

	if err := s.CreatePrincipal(t.Context(), p, e); err != nil {
		t.Fatalf("create principal %q: %v", id, err)
	}
}

func sweep(tokens, accounts bool) accesstoken.Sweep {
	return accesstoken.Sweep{Inactivity: sweepPeriod, Tokens: tokens, Accounts: accounts}
}

// sweepEntry is the entry function of a sweep.
func sweepEntry(at time.Time, object string) audit.Entry {
	e := entryAt("tok-x", at)
	e.Object = object

	return e
}

func mustSweep(t *testing.T, s Store, sw accesstoken.Sweep) accesstoken.SweepResult {
	t.Helper()

	res, err := s.SweepInactive(t.Context(), sw, sweepEntry)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	slices.SortFunc(res.Tokens, func(a, b accesstoken.SweptToken) int { return strings.Compare(a.ID, b.ID) })
	slices.Sort(res.Principals)

	return res
}

func sweptIDs(res accesstoken.SweepResult) []string {
	out := make([]string, 0, len(res.Tokens))
	for _, tok := range res.Tokens {
		out = append(out, tok.ID)
	}

	return out
}

// wantNear fails when got is not within clockSkew of want.
func wantNear(t *testing.T, what string, got, want time.Time) {
	t.Helper()

	if d := got.Sub(want); d < -clockSkew || d > clockSkew {
		t.Fatalf("%s = %s, want the time of the database clock, near %s", what, got, want)
	}
}

func testSweepTokens(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()
	ts := newSweepTimes()

	mustPrincipalAt(t, s, "prn-1", principal.KindService, ts.old)

	mustToken(t, s, relToken("tok-unused", "prn-1", ts.old, ts), digest(1))
	mustToken(t, s, relToken("tok-old-use", "prn-1", ts.old, ts), digest(2))
	mustToken(t, s, relToken("tok-new-use", "prn-1", ts.old, ts), digest(3))
	mustToken(t, s, relToken("tok-revoked", "prn-1", ts.old, ts), digest(4))
	mustToken(t, s, relToken("tok-young", "prn-1", ts.recent, ts), digest(5))

	expired := relToken("tok-expired", "prn-1", ts.old, ts)
	expired.ExpiresAt = ts.old.Add(time.Hour)
	mustToken(t, s, expired, digest(6))

	for id, used := range map[string]time.Time{"tok-old-use": ts.old.Add(time.Minute), "tok-new-use": ts.recent} {
		if err := s.TouchToken(ctx, id, used, time.Minute); err != nil {
			t.Fatalf("touch %s: %v", id, err)
		}
	}

	if err := s.RevokeToken(ctx, "tok-revoked", entryAt("tok-revoked", ts.old.Add(time.Minute))); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	res := mustSweep(t, s, sweep(true, false))

	want := []string{"tok-old-use", "tok-unused"}
	if !slices.Equal(sweptIDs(res), want) || len(res.Principals) != 0 {
		t.Fatalf("sweep = %+v, want the tokens %q and no principal", res, want)
	}

	if res.Tokens[0].PrincipalID != "prn-1" {
		t.Fatalf("swept token = %+v, want its principal prn-1", res.Tokens[0])
	}

	wantNear(t, "sweep time", res.At, ts.now)

	if got := mustGet(t, s, "tok-unused"); !got.DisabledAt.Equal(res.At) {
		t.Fatalf("disabled at = %s, want the sweep time %s", got.DisabledAt, res.At)
	}

	active := listQuery("prn-1")
	active.At, active.Filter = res.At, accesstoken.FilterActive
	wantList(t, s, active, []string{"tok-young", "tok-new-use"}, 2)

	// A disabled token counts as ended.
	ended := listQuery("prn-1")
	ended.At, ended.Filter = res.At, accesstoken.FilterEnded
	wantList(t, s, ended, []string{"tok-unused", "tok-revoked", "tok-old-use", "tok-expired"}, 4)

	r := accesstoken.Replacement{
		OldEndsAt: res.At.Add(time.Hour),
		OldID:     "tok-unused",
		Next:      relToken("tok-n", "prn-1", res.At, ts),
		Digest:    digest(9),
	}
	wantErr(t, "rotate a disabled token", s.RotateToken(ctx, r, entryAt("tok-unused", res.At)), accesstoken.ErrEnded)

	again := mustSweep(t, s, sweep(true, false))
	if len(again.Tokens) != 0 {
		t.Fatalf("second sweep disabled %+v, want nothing", again.Tokens)
	}
}

func testSweepAccounts(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()
	ts := newSweepTimes()

	for _, id := range []string{"prn-idle", "prn-busy", "prn-new-token", "prn-gone"} {
		mustPrincipalAt(t, s, id, principal.KindPerson, ts.old)
	}

	mustPrincipalAt(t, s, "prn-young", principal.KindPerson, ts.recent)
	mustPrincipalAt(t, s, "prn-svc", principal.KindService, ts.old)

	mustToken(t, s, relToken("tok-busy", "prn-busy", ts.old, ts), digest(1))
	mustToken(t, s, relToken("tok-idle", "prn-idle", ts.old, ts), digest(2))
	// A token made after the cutoff and never used counts as activity.
	mustToken(t, s, relToken("tok-new", "prn-new-token", ts.recent, ts), digest(3))

	if err := s.TouchToken(ctx, "tok-busy", ts.recent, time.Minute); err != nil {
		t.Fatalf("touch: %v", err)
	}

	if err := s.TouchToken(ctx, "tok-idle", ts.old.Add(time.Minute), time.Minute); err != nil {
		t.Fatalf("touch: %v", err)
	}

	if err := s.DeletePrincipal(ctx, "prn-gone", entry("tok-x")); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	res := mustSweep(t, s, sweep(false, true))
	if !slices.Equal(res.Principals, []string{"prn-idle"}) || len(res.Tokens) != 0 {
		t.Fatalf("sweep = %+v, want only the principal prn-idle", res)
	}

	got, err := s.GetPrincipal(ctx, "prn-idle")
	if err != nil {
		t.Fatalf("get principal: %v", err)
	}

	if !got.DisabledAt.Equal(res.At) {
		t.Fatalf("principal disabled at = %s, want the sweep time %s", got.DisabledAt, res.At)
	}

	m, err := s.GetTokenByDigest(ctx, digest(2))
	if err != nil {
		t.Fatalf("get by digest: %v", err)
	}

	if !m.Owner.DisabledAt.Equal(res.At) || !m.Token.DisabledAt.IsZero() {
		t.Fatalf("lookup = %+v, want a disabled owner and a token that the account sweep did not disable", m)
	}
}

func testSweepRefused(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()
	ts := newSweepTimes()

	mustPrincipalAt(t, s, "prn-1", principal.KindPerson, ts.old)
	tok := relToken("tok-1", "prn-1", ts.old, ts)
	mustToken(t, s, tok, digest(1))

	refuse := func(_ time.Time, object string) audit.Entry {
		e := refused("tok-x")
		e.Object = object

		return e
	}

	_, err := s.SweepInactive(ctx, sweep(true, true), refuse)
	wantErr(t, "sweep with a refused entry", err, audit.ErrInvalidEntry)

	equalToken(t, mustGet(t, s, "tok-1"), tok)

	p, err := s.GetPrincipal(ctx, "prn-1")
	if err != nil || !p.DisabledAt.IsZero() {
		t.Fatalf("principal after a refused sweep = %+v, %v; want it enabled", p, err)
	}
}

func testEnablePrincipal(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()
	ts := newSweepTimes()

	mustPrincipalAt(t, s, "prn-1", principal.KindPerson, ts.old)

	wantErr(t, "enable of an enabled principal", s.EnablePrincipal(ctx, "prn-1", entry("tok-x")),
		principal.ErrNotDisabled)
	wantErr(t, "enable of a missing principal", s.EnablePrincipal(ctx, "prn-none", entry("tok-x")),
		principal.ErrNotFound)

	if res := mustSweep(t, s, sweep(false, true)); !slices.Equal(res.Principals, []string{"prn-1"}) {
		t.Fatalf("sweep = %+v, want the principal prn-1", res)
	}

	enabledAt := testNow()
	if err := s.EnablePrincipal(ctx, "prn-1", entryAt("tok-x", enabledAt)); err != nil {
		t.Fatalf("enable: %v", err)
	}

	p, err := s.GetPrincipal(ctx, "prn-1")
	if err != nil || !p.DisabledAt.IsZero() || !p.UpdatedAt.Equal(enabledAt) {
		t.Fatalf("enabled principal = %+v, %v; want no disable time and the update time %s", p, err, enabledAt)
	}

	// The enable sets the activity baseline, so the next sweep keeps the
	// principal enabled.
	if res := mustSweep(t, s, sweep(false, true)); len(res.Principals) != 0 {
		t.Fatalf("sweep after the enable disabled %q, want nothing", res.Principals)
	}

	p, err = s.GetPrincipal(ctx, "prn-1")
	if err != nil || !p.DisabledAt.IsZero() {
		t.Fatalf("principal after the enable and a sweep = %+v, %v; want it enabled", p, err)
	}
}
