package accesstest

import (
	"context"
	"crypto/sha256"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
)

// tokenStore is what the last-access case needs next to access.Store: the
// token writes and the principal records that own the tokens.
type tokenStore interface {
	CreateToken(ctx context.Context, t accesstoken.Token, d accesstoken.Digest, e audit.Entry) error
	RevokeToken(ctx context.Context, id string, e audit.Entry) error
	TouchToken(ctx context.Context, id string, at time.Time, every time.Duration) error
	CreatePrincipal(ctx context.Context, p principal.Principal, e audit.Entry) error
}

// testLastAccess reads the latest last-use time of the access tokens of
// each principal: the maximum over its tokens, with revoked and expired
// tokens counted. A principal with no used token is absent.
func testLastAccess(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)

	ts, ok := s.(tokenStore)
	if !ok {
		t.Skip("the store keeps no access tokens")
	}

	ctx := t.Context()

	for _, p := range [][2]string{{"prn-a", "alice"}, {"prn-b", "bob"}, {"prn-never", "cara"}, {"prn-idle", "dan"}} {
		mustAccessPrincipal(t, ts, p[0], p[1])
	}

	mustAccessToken(t, ts, newAccessToken("tok-a1", "prn-a", 1), 1)
	mustAccessToken(t, ts, newAccessToken("tok-a2", "prn-a", 2), 2)
	mustAccessToken(t, ts, newAccessToken("tok-idle", "prn-idle", 1), 3)

	// An expired token that was used still counts.
	expired := newAccessToken("tok-ended", "prn-b", 1)
	expired.ExpiresAt = at(5)
	mustCreateAccessToken(t, ts, expired, 4)

	touchAccessToken(t, ts, "tok-a1", 5)
	touchAccessToken(t, ts, "tok-a2", 7)
	touchAccessToken(t, ts, "tok-ended", 4)

	// A use before the revoke still counts.
	mustAccessToken(t, ts, newAccessToken("tok-old", "prn-a", 5), 8)
	touchAccessToken(t, ts, "tok-old", 9)

	if err := ts.RevokeToken(ctx, "tok-old", tokenEntry("tok-old", at(10))); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	got, err := rs.LastAccess(ctx, []string{"prn-a", "prn-a", "prn-b", "prn-never", "prn-idle", "prn-none"})
	if err != nil {
		t.Fatalf("last access: %v", err)
	}

	want := map[string]time.Time{"prn-a": at(9), "prn-b": at(4)}

	if len(got) != len(want) {
		t.Fatalf("last access = %v, want %v", got, want)
	}

	for id, when := range want {
		if !got[id].Equal(when) {
			t.Fatalf("last access of %s = %s, want %s", id, got[id], when)
		}
	}

	empty, err := rs.LastAccess(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("last access of no principal = %v, %v; want none", empty, err)
	}
}

// mustAccessPrincipal stores the principal id with the subject subject.
func mustAccessPrincipal(t *testing.T, ts tokenStore, id, subject string) {
	t.Helper()

	p := principal.Principal{
		CreatedAt: at(0), UpdatedAt: at(0), DisabledAt: time.Time{}, ID: id, Subject: subject, DisplayName: "",
		Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
	}

	if err := ts.CreatePrincipal(t.Context(), p, entry()); err != nil {
		t.Fatalf("create principal %q: %v", id, err)
	}
}

// newAccessToken returns a token of principalID made at minute created.
func newAccessToken(id, principalID string, created int) accesstoken.Token {
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

// accessDigest returns a distinct digest for n.
func accessDigest(n int) accesstoken.Digest {
	return sha256.Sum256([]byte(strconv.Itoa(n)))
}

// tokenEntry returns a change entry in the scope @server for the token id
// at the time when.
func tokenEntry(id string, when time.Time) audit.Entry {
	e := entry()
	e.At = when
	e.Action = "server:manage-tokens"
	e.Object = accesstoken.Ref(id)

	return e
}

// mustCreateAccessToken stores tok with the digest number n.
func mustCreateAccessToken(t *testing.T, ts tokenStore, tok accesstoken.Token, n int) {
	t.Helper()

	if err := ts.CreateToken(t.Context(), tok, accessDigest(n), tokenEntry(tok.ID, tok.CreatedAt)); err != nil {
		t.Fatalf("create token %q: %v", tok.ID, err)
	}
}

// mustAccessToken stores tok with the digest number n.
func mustAccessToken(t *testing.T, ts tokenStore, tok accesstoken.Token, n int) {
	t.Helper()

	mustCreateAccessToken(t, ts, tok, n)
}

// touchAccessToken records a use of the token id at minute.
func touchAccessToken(t *testing.T, ts tokenStore, id string, minute int) {
	t.Helper()

	if err := ts.TouchToken(t.Context(), id, at(minute), time.Minute); err != nil {
		t.Fatalf("touch %q: %v", id, err)
	}
}
