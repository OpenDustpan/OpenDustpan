package access_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// lastAccessCounter counts the LastAccess lookups of its store.
type lastAccessCounter struct {
	*sqlite.Store

	calls *int
}

func (s lastAccessCounter) LastAccess(ctx context.Context, ids []string) (map[string]time.Time, error) {
	*s.calls++

	return s.Store.LastAccess(ctx, ids)
}

// seedToken stores tok with the digest of secret.
func seedToken(t *testing.T, f *fixture, tok accesstoken.Token, secret string) {
	t.Helper()

	e := storetest.ServerChange(tok.CreatedAt, "token:"+tok.ID)

	if err := f.store.CreateToken(t.Context(), tok, sha256.Sum256([]byte(secret)), e); err != nil {
		t.Fatalf("create token %q: %v", tok.ID, err)
	}
}

// newLastAccessToken returns a token of principalID that expires a day
// after used.
func newLastAccessToken(id, principalID string, used time.Time) accesstoken.Token {
	return accesstoken.Token{
		CreatedAt:   time.Unix(0, 0),
		ExpiresAt:   used.Add(24 * time.Hour),
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

// seedLastAccess stores a token of alice used at used, and an unused token
// of bob.
func seedLastAccess(t *testing.T, f *fixture, used time.Time) {
	t.Helper()

	seedToken(t, f, newLastAccessToken("tok-alice", "prn-alice", used), "alice")
	seedToken(t, f, newLastAccessToken("tok-bob", "prn-bob", used), "bob")

	if err := f.store.TouchToken(t.Context(), "tok-alice", used, time.Minute); err != nil {
		t.Fatalf("touch token: %v", err)
	}
}

// lastAccessOf returns the last access of the row of subject in entries:
// tracked and the time, or not tracked with no time.
func lastAccessOf(t *testing.T, entries []access.ReviewEntry, subject string) (bool, *time.Time) {
	t.Helper()

	for i := range entries {
		if entries[i].Row.Grant.Subject.String() == subject {
			return entries[i].Row.LastAccessTracked, entries[i].Row.LastAccessAt
		}
	}

	t.Fatalf("no row of %s in %d entries", subject, len(entries))

	return false, nil
}

// checkInstallationLastAccess checks the rows of an export of every grant:
// alice is tracked with her use, bob with no time, and the group row
// carries neither field.
func checkInstallationLastAccess(t *testing.T, entries []access.ReviewEntry, used time.Time) {
	t.Helper()

	if tracked, at := lastAccessOf(t, entries, "principal:prn-alice"); !tracked || at == nil || !at.Equal(used) {
		t.Fatalf("last access of alice = %v, %v; want tracked at %s", tracked, at, used)
	}

	if tracked, at := lastAccessOf(t, entries, "principal:prn-bob"); !tracked || at != nil {
		t.Fatalf("last access of bob = %v, %v; want tracked with no time", tracked, at)
	}

	if tracked, at := lastAccessOf(t, entries, "group:grp-ops#member"); tracked || at != nil {
		t.Fatalf("last access of the group = %v, %v; want neither field", tracked, at)
	}
}

// viaRowOf returns the row of entries read through a group, or fails when
// there is none.
func viaRowOf(t *testing.T, entries []access.ReviewEntry) *access.ReviewEntry {
	t.Helper()

	for i := range entries {
		if entries[i].Row.Via != nil {
			return &entries[i]
		}
	}

	t.Fatal("the export has no row through the group")

	return nil
}

// checkViaLastAccess checks the row read through a group: it uses the
// member, with the use of alice.
func checkViaLastAccess(t *testing.T, entries []access.ReviewEntry, used time.Time) {
	t.Helper()

	via := viaRowOf(t, entries)

	if !via.Row.LastAccessTracked || via.Row.LastAccessAt == nil || !via.Row.LastAccessAt.Equal(used) {
		t.Fatalf("last access through the group = %v, %v; want the use of alice at %s",
			via.Row.LastAccessTracked, via.Row.LastAccessAt, used)
	}
}

// checkCallerLastAccess checks the rows of the grants of alice: the own row
// and the row through the group, both with the use of alice.
func checkCallerLastAccess(t *testing.T, entries []access.ReviewEntry, used time.Time) {
	t.Helper()

	if len(entries) != 2 {
		t.Fatalf("caller grants = %d rows, want the own row and the row through the group", len(entries))
	}

	for i := range entries {
		row := &entries[i].Row
		if !row.LastAccessTracked || row.LastAccessAt == nil || !row.LastAccessAt.Equal(used) {
			t.Fatalf("caller row %+v: want the use of alice at %s", row, used)
		}
	}
}

// TestReviewLastAccess proves that an export answers the last access of
// each principal row: the latest use of its tokens, or no time when no
// token was ever used. A row read through a group uses the member, and a
// group row with no Via carries neither field.
func TestReviewLastAccess(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	used := time.Unix(3600, 0).UTC()

	alice := f.person(t, "alice")
	f.person(t, "bob")
	ops := f.group(t, "ops", "alice")

	acme := identity.NamespaceObject("acme")
	f.grant(t, prn("alice"), access.RoleViewer, acme)
	f.grant(t, ops, access.RoleWriter, acme)
	f.grant(t, prn("bob"), access.RoleViewer, acme)

	seedLastAccess(t, f, used)

	page, err := f.svc.Review(ctx, identity.ServerObject(), access.ReviewRequest{
		From: nil, To: nil, Subject: "", Cursor: "", Limit: 0, Expand: false,
	})
	if err != nil {
		t.Fatalf("review: %v", err)
	}

	checkInstallationLastAccess(t, page.Entries, used)

	filtered, err := f.svc.Review(ctx, identity.ServerObject(), access.ReviewRequest{
		From: nil, To: nil, Subject: "principal:prn-alice", Cursor: "", Limit: 0, Expand: false,
	})
	if err != nil {
		t.Fatalf("review of alice: %v", err)
	}

	checkViaLastAccess(t, filtered.Entries, used)

	caller, err := f.svc.CallerGrants(ctx, alice, access.CallerGrantsRequest{Cursor: "", Limit: 0})
	if err != nil {
		t.Fatalf("caller grants: %v", err)
	}

	checkCallerLastAccess(t, caller.Entries, used)
}

// TestReviewLastAccessReadsOncePerPage proves that an export reads the last
// access with one lookup for each page.
func TestReviewLastAccessReadsOncePerPage(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	f.person(t, "alice")
	f.grant(t, prn("alice"), access.RoleViewer, identity.NamespaceObject("acme"))
	f.grant(t, prn("alice"), access.RoleWriter, identity.NamespaceObject("beta"))

	var calls int

	counted := lastAccessCounter{Store: f.store, calls: &calls}

	svc, err := access.NewService(access.ServiceConfig{
		Store: counted, Directory: counted, Namespaces: counted, Authorizer: f.authz,
		Reach: access.ReachConfig{
			Grants: f.store, Roles: f.store, Callers: f.store, Principals: f.store,
			Collections: f.units, Units: f.store,
		},
		Now: nil, NewID: nil,
		BootstrapAdmins: []string{"admin"}, Settings: nil, LimitGroups: nil,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	first, err := svc.Review(ctx, identity.ServerObject(), access.ReviewRequest{
		From: nil, To: nil, Subject: "", Cursor: "", Limit: 2, Expand: false,
	})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}

	if first.NextCursor == "" {
		t.Fatal("first page has no next cursor")
	}

	second, err := svc.Review(ctx, identity.ServerObject(), access.ReviewRequest{
		From: nil, To: nil, Subject: "", Cursor: first.NextCursor, Limit: 2, Expand: false,
	})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}

	if len(second.Entries) == 0 {
		t.Fatal("second page has no rows")
	}

	if calls != 2 {
		t.Fatalf("last access lookups = %d, want one for each of the 2 pages", calls)
	}
}
