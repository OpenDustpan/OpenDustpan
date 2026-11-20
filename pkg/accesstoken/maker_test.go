package accesstoken_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// fakeMakers answers the decision of the act-as path of a maker: can for
// each maker, or err for every maker when err is set.
type fakeMakers struct {
	can map[string]bool
	err error
}

func (f fakeMakers) CanActAs(_ context.Context, maker, _ string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}

	return f.can[maker], nil
}

// errDecide is the error of a checker that cannot decide.
var errDecide = errors.New("the grant store is not available")

// makerFixture is a fixture with one service token and one personal token,
// and a second token service over the same store with the checker makers.
type makerFixture struct {
	fixture

	checked  *accesstoken.Service
	token    accesstoken.Token
	secret   accesstoken.Secret
	personal accesstoken.Token
	own      accesstoken.Secret
}

func newMakerFixture(t *testing.T, makers accesstoken.MakerChecker) makerFixture {
	t.Helper()

	f := newFixture(t, nil)

	token, secret, err := f.createIn(t.Context(), accesstoken.Input{
		ExpiresAt: f.clock.Now().Add(accesstoken.DefaultLifetime), PrincipalID: f.service.ID, Name: "deploy",
	})
	if err != nil {
		t.Fatalf("create the service token: %v", err)
	}

	personal, own, err := f.createIn(t.Context(), accesstoken.Input{
		ExpiresAt: f.clock.Now().Add(accesstoken.DefaultLifetime), PrincipalID: f.person.ID, Name: "laptop",
	})
	if err != nil {
		t.Fatalf("create the personal token: %v", err)
	}

	checked, err := accesstoken.NewService(accesstoken.Config{
		Makers: makers, TempAdmins: nil,
		Store: f.store, Principals: f.store, Freshness: nil, Authorizer: nil, Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		Logger: nil, Now: f.clock.Now, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	return makerFixture{
		fixture: f, checked: checked, token: token, secret: secret, personal: personal, own: own,
	}
}

// revoked reports whether the token id is revoked.
func (f *makerFixture) revoked(t *testing.T, id string) bool {
	t.Helper()

	tok, err := f.svc.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("get token %q: %v", id, err)
	}

	return !tok.RevokedAt.IsZero()
}

// revocationEntries returns the audit entries of @server of the revocation
// of the token id with the subject accesstoken.MakerSubject.
func (f *makerFixture) revocationEntries(t *testing.T, id string) []audit.Entry {
	t.Helper()

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	var found []audit.Entry

	for i := range listing.Entries {
		e := &listing.Entries[i]
		if e.Subject == accesstoken.MakerSubject && e.Object == accesstoken.TokenRef(f.service.ID, id) {
			found = append(found, *e)
		}
	}

	return found
}

// revocationEntry returns the one audit entry of the revocation of the token
// id, and fails the test unless there is exactly one.
func (f *makerFixture) revocationEntry(t *testing.T, id string) audit.Entry {
	t.Helper()

	found := f.revocationEntries(t, id)
	if len(found) != 1 {
		t.Fatalf("revocation entries of %s = %+v, want one", id, found)
	}

	return found[0]
}

// changeOf returns the new value of the change key of e.
func changeOf(e audit.Entry, key string) string {
	for _, c := range e.Changes {
		if c.Key == key {
			return c.New
		}
	}

	return ""
}

// wantLostMakerEntry fails the test unless e is the entry of a revocation
// after the change server:manage-access grant:grt-1 of root.
func wantLostMakerEntry(t *testing.T, e audit.Entry) {
	t.Helper()

	if e.Reason != accesstoken.ReasonMakerLostPath || e.Outcome != audit.OutcomeSuccess ||
		e.Action != string(identity.ActionPrincipalManageTokens) ||
		changeOf(e, "maker") != "admin" || changeOf(e, "cause") != "server:manage-access grant:grt-1" ||
		changeOf(e, "causedBy") != "root" {
		t.Fatalf("revocation entry = %+v, want the reason, the maker, the cause and its caller", e)
	}
}

// TestRevokeLostMakersRevokesTheServiceToken proves that a change after which
// the maker has no act-as path revokes the service token, keeps the personal
// token, and writes one entry that names the maker, the change and its
// caller.
func TestRevokeLostMakersRevokesTheServiceToken(t *testing.T) {
	t.Parallel()

	f := newMakerFixture(t, fakeMakers{can: map[string]bool{}, err: nil})

	cause := accesstoken.Cause{Subject: "root", Action: "server:manage-access", Object: "grant:grt-1"}

	got, err := f.checked.RevokeLostMakers(t.Context(), cause)
	if err != nil {
		t.Fatalf("revoke lost makers: %v", err)
	}

	if len(got.Revoked) != 1 || got.Revoked[0] != f.token.ID {
		t.Fatalf("revoked = %v, want only the service token %s", got.Revoked, f.token.ID)
	}

	if !f.revoked(t, f.token.ID) || f.revoked(t, f.personal.ID) {
		t.Fatal("want the service token revoked and the personal token kept")
	}

	wantLostMakerEntry(t, f.revocationEntry(t, f.token.ID))

	// A second run finds no active token of the maker and writes nothing.
	again, err := f.checked.RevokeLostMakers(t.Context(), cause)
	if err != nil || len(again.Revoked) != 0 {
		t.Fatalf("second run = %v, %v; want nothing revoked", again.Revoked, err)
	}
}

// TestRevokeLostMakersKeepsATokenWithAPath proves that a maker that keeps an
// act-as path keeps its service token.
func TestRevokeLostMakersKeepsATokenWithAPath(t *testing.T) {
	t.Parallel()

	f := newMakerFixture(t, fakeMakers{can: map[string]bool{"admin": true}, err: nil})

	got, err := f.checked.RevokeLostMakers(t.Context(), accesstoken.Cause{
		Subject: "root", Action: "server:manage-access", Object: "grant:grt-1",
	})
	if err != nil || len(got.Revoked) != 0 {
		t.Fatalf("revoke lost makers = %v, %v; want nothing revoked", got.Revoked, err)
	}

	if f.revoked(t, f.token.ID) {
		t.Fatal("the service token of a maker with a path is revoked")
	}

	if _, err := f.checked.AuthenticateToken(t.Context(), f.secret.Reveal()); err != nil {
		t.Fatalf("authenticate the service token of a maker with a path: %v", err)
	}
}

// TestLostMakerCheckFailsClosed sends the error of a checker that cannot
// decide to both checks. The run after a change returns the error and keeps
// the token. The check at the use returns the same error, with no wrap of
// ErrUnauthenticated, and keeps the token, so a later use checks again.
func TestLostMakerCheckFailsClosed(t *testing.T) {
	t.Parallel()

	f := newMakerFixture(t, fakeMakers{can: nil, err: errDecide})

	_, err := f.checked.RevokeLostMakers(t.Context(), accesstoken.Cause{
		Subject: "root", Action: "server:manage-access", Object: "grant:grt-1",
	})
	if !errors.Is(err, errDecide) {
		t.Fatalf("revoke lost makers = %v, want the error of the checker", err)
	}

	_, authErr := f.checked.AuthenticateToken(t.Context(), f.secret.Reveal())
	if authErr == nil || errors.Is(authErr, identity.ErrUnauthenticated) || !errors.Is(authErr, errDecide) {
		t.Fatalf("authenticate with a checker that cannot decide = %v, want the error of the checker", authErr)
	}

	if f.revoked(t, f.token.ID) {
		t.Fatal("a checker that cannot decide revoked the token")
	}

	if _, err := f.checked.AuthenticateToken(t.Context(), f.own.Reveal()); err != nil {
		t.Fatalf("authenticate the personal token: %v", err)
	}
}

// TestLostMakerIsRevokedAtUse proves the check at the use: a service token
// whose maker lost the act-as path with no run after a change answers
// ErrUnauthenticated, is revoked, and its entry has the reason of a use.
func TestLostMakerIsRevokedAtUse(t *testing.T) {
	t.Parallel()

	f := newMakerFixture(t, fakeMakers{can: map[string]bool{}, err: nil})

	_, authErr := f.checked.AuthenticateToken(t.Context(), f.secret.Reveal())
	if !errors.Is(authErr, identity.ErrUnauthenticated) {
		t.Fatalf("authenticate = %v, want ErrUnauthenticated", authErr)
	}

	if !f.revoked(t, f.token.ID) {
		t.Fatal("the token of a maker with no path is not revoked at its use")
	}

	e := f.revocationEntry(t, f.token.ID)
	if e.Reason != accesstoken.ReasonMakerLostPathAtUse || changeOf(e, "maker") != "admin" {
		t.Fatalf("revocation entry = %+v, want the reason of a use and the maker", e)
	}

	// The personal token never needs a maker.
	if _, err := f.checked.AuthenticateToken(t.Context(), f.own.Reveal()); err != nil {
		t.Fatalf("authenticate the personal token: %v", err)
	}
}

// TestNoMakerCheckerKeepsTheTokens proves that a Service with no checker
// keeps each service token.
func TestNoMakerCheckerKeepsTheTokens(t *testing.T) {
	t.Parallel()

	f := newMakerFixture(t, nil)

	got, err := f.svc.RevokeLostMakers(t.Context(), accesstoken.Cause{
		Subject: "root", Action: "server:manage-access", Object: "grant:grt-1",
	})
	if err != nil || len(got.Revoked) != 0 {
		t.Fatalf("revoke lost makers = %v, %v; want nothing", got.Revoked, err)
	}

	if _, err := f.svc.AuthenticateToken(t.Context(), f.secret.Reveal()); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
}

// makerCheckPath runs one of the two checks of a service token and returns
// its error.
type makerCheckPath func(t *testing.T, f *makerFixture) error

// makerCheckPaths returns the check at the authentication and the check of a
// lease request.
func makerCheckPaths() map[string]makerCheckPath {
	return map[string]makerCheckPath{
		"authenticate": func(t *testing.T, f *makerFixture) error {
			t.Helper()

			_, err := f.checked.AuthenticateToken(t.Context(), f.secret.Reveal())

			return err
		},
		"lease check": func(t *testing.T, f *makerFixture) error {
			t.Helper()

			return f.checked.CheckActive(t.Context(), f.token.ID)
		},
	}
}

// TestMakerCheckErrorIsNoRefusalOfTheToken sends a checker that cannot decide
// to the two checks of a service token: the check at the authentication and
// the check of a lease request. Both answer the error of the checker, which
// does not wrap ErrUnauthenticated, so the server answers 5xx and no client
// ends a session as for a revoked token. The token is kept and no entry is
// written.
func TestMakerCheckErrorIsNoRefusalOfTheToken(t *testing.T) {
	t.Parallel()

	for name, check := range makerCheckPaths() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newMakerFixture(t, fakeMakers{can: nil, err: errDecide})

			for range 2 {
				err := check(t, &f)
				if err == nil || errors.Is(err, identity.ErrUnauthenticated) || !errors.Is(err, errDecide) {
					t.Fatalf("check = %v, want the error of the checker with no ErrUnauthenticated", err)
				}
			}

			if f.revoked(t, f.token.ID) {
				t.Fatal("a checker that cannot decide revoked the token")
			}

			if n := len(f.revocationEntries(t, f.token.ID)); n != 0 {
				t.Fatalf("%d revocation entries, want none", n)
			}
		})
	}
}

// TestMakerWithNoPathRefusesAndRevokesOnBothChecks sends a maker with no path
// to the same two checks. Both answer ErrUnauthenticated, revoke the token and
// write one entry.
func TestMakerWithNoPathRefusesAndRevokesOnBothChecks(t *testing.T) {
	t.Parallel()

	for name, check := range makerCheckPaths() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newMakerFixture(t, fakeMakers{can: map[string]bool{}, err: nil})

			if err := check(t, &f); !errors.Is(err, identity.ErrUnauthenticated) {
				t.Fatalf("check = %v, want ErrUnauthenticated", err)
			}

			if !f.revoked(t, f.token.ID) {
				t.Fatal("the token of a maker with no path is not revoked")
			}

			if n := len(f.revocationEntries(t, f.token.ID)); n != 1 {
				t.Fatalf("%d revocation entries, want 1", n)
			}
		})
	}
}
