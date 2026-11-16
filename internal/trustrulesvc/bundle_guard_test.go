package trustrulesvc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// guardBundleStore records the bundle writes that reach it.
type guardBundleStore struct {
	saved   int
	rotated int
	deleted []int64
	entries []audit.Entry
}

func (g *guardBundleStore) SaveBundle(
	_ context.Context, _ namespace.Name, _ trustrule.BundleFields, _ namespace.Fence, e audit.Entry,
) (trustrule.Bundle, error) {
	g.saved++
	g.entries = append(g.entries, e)

	return trustrule.Bundle{ID: "", Namespace: "", Active: 0, Versions: nil, Revision: 0, CreatedAt: time.Time{}}, nil
}

func (g *guardBundleStore) RotateBundle(
	_ context.Context, _ namespace.Name, _ trustrule.BundleFields, _ namespace.Fence, e audit.Entry,
) (trustrule.Bundle, error) {
	g.rotated++
	g.entries = append(g.entries, e)

	return trustrule.Bundle{ID: "", Namespace: "", Active: 0, Versions: nil, Revision: 0, CreatedAt: time.Time{}}, nil
}

func (g *guardBundleStore) GetBundle(_ context.Context, _ namespace.Name) (trustrule.Bundle, error) {
	return trustrule.Bundle{ID: "", Namespace: "", Active: 0, Versions: nil, Revision: 0, CreatedAt: time.Time{}},
		trustrule.ErrNotFound
}

func (g *guardBundleStore) DeleteBundleVersion(
	_ context.Context, _ namespace.Name, version int64, _ namespace.Fence, e audit.Entry,
) error {
	g.deleted = append(g.deleted, version)
	g.entries = append(g.entries, e)

	return nil
}

// guardSessionPolicy answers the token freshness window of the tests.
type guardSessionPolicy struct {
	window time.Duration
}

func (g guardSessionPolicy) Policy(_ context.Context) (session.Policy, error) {
	return session.Policy{
		UpdatedAt: time.Time{}, Limits: session.Limits{Idle: 0, MaxLength: 0},
		TokenFreshness: g.window, TokenFreshnessDefault: false,
	}, nil
}

// guardFixture returns a service over a bundle store that records writes,
// with the session window of the tests and the clock exchangeNow.
func guardFixture(t *testing.T, bundles *guardBundleStore) *trustrulesvc.Service {
	t.Helper()

	authz := &grants{allow: nil, checked: nil, err: nil}
	reach := &fakeReach{checked: nil, err: nil}
	people := fakePrincipals{"prn-worker": principalOf("prn-worker", principal.KindService, "acme")}

	svc, err := trustrulesvc.NewService(trustrulesvc.Config{
		Store: &fakeStore{created: nil, entries: nil, deleted: nil}, Namespaces: fakeNamespaces{},
		Principals: people, Authorizer: authz, Reach: reach,
		InstallationURLs: []string{"https://dustpan.example.com"}, OwnIssuer: nil, CheckURL: nil,
		Bundles: bundles, Sessions: guardSessionPolicy{window: session.DefaultTokenFreshness},
		Now: exchangeNow,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// bundleGuardCase is one row of the fresh sign-in table: the bundle write
// op, the session kind, the caller kind and the error it must give.
type bundleGuardCase struct {
	op      string
	session string
	caller  string
	want    error
}

// bundleGuardCases returns the rows of the table: each write with no
// console session, with a sign-in older than the window, with a stored
// access token, and with a trust token.
func bundleGuardCases() []bundleGuardCase {
	ops := []string{"save", "rotate", "delete"}

	sessions := []struct {
		session string
		caller  string
		want    error
	}{
		{session: "none", caller: "person", want: trustrulesvc.ErrFreshRequired},
		{session: "stale", caller: "person", want: trustrulesvc.ErrStaleToken},
		{session: "fresh", caller: "token", want: trustrulesvc.ErrSignInRequired},
		{session: "fresh", caller: "trust", want: trustrulesvc.ErrSignInRequired},
	}

	var out []bundleGuardCase

	out = make([]bundleGuardCase, 0, len(ops)*len(sessions))

	for _, op := range ops {
		for _, s := range sessions {
			out = append(out, bundleGuardCase{op: op, session: s.session, caller: s.caller, want: s.want})
		}
	}

	return out
}

// contextOf returns the request context of the row: background for no
// console session, a stale sign-in, or a fresh console session.
func (c bundleGuardCase) contextOf(t *testing.T) context.Context {
	t.Helper()

	switch c.session {
	case "stale":
		at := exchangeNow().Add(-session.DefaultTokenFreshness - time.Minute)
		ctx := sessionauth.WithSession(t.Context())

		return sessionauth.WithSignIn(ctx, sessionauth.SignIn{At: at, Provider: "password"})
	case "fresh":
		return freshSessionContext(t)
	default:
		return context.Background()
	}
}

// callerOf returns the caller of the row: a person, a stored access token,
// or a trust token.
func (c bundleGuardCase) callerOf() identity.Principal {
	switch c.caller {
	case "token":
		caller := person()
		caller.TokenID = "tok-1"

		return caller
	case "trust":
		caller := person()
		caller.Credential = identity.CredentialTrustToken
		caller.Kind = identity.KindService

		return caller
	default:
		return person()
	}
}

// run calls the bundle write of the row.
func (c bundleGuardCase) run(
	ctx context.Context, svc *trustrulesvc.Service, caller identity.Principal,
) error {
	fields := trustrule.BundleFields{PEM: "", Overlap: 0, Revision: 0}

	switch c.op {
	case "rotate":
		_, err := svc.RotateBundle(ctx, caller, emptyEntry(), "acme", fields)

		return err
	case "delete":
		return svc.DeleteBundleVersion(ctx, caller, emptyEntry(), "acme", 1)
	default:
		_, err := svc.SaveBundle(ctx, caller, emptyEntry(), "acme", fields)

		return err
	}
}

// checkBundleGuardCase sends one row as if every other step had failed:
// nothing may reach the bundle store and no audit entry is written.
func checkBundleGuardCase(t *testing.T, c bundleGuardCase) {
	t.Helper()

	bundles := &guardBundleStore{saved: 0, rotated: 0, deleted: nil, entries: nil}
	svc := guardFixture(t, bundles)

	err := c.run(c.contextOf(t), svc, c.callerOf())
	if !errors.Is(err, c.want) {
		t.Fatalf("%s/%s-%s = %v, want %v", c.op, c.session, c.caller, err, c.want)
	}

	if bundles.saved != 0 || bundles.rotated != 0 || len(bundles.deleted) != 0 {
		t.Fatalf("%s/%s-%s reached the bundle store", c.op, c.session, c.caller)
	}

	if len(bundles.entries) != 0 {
		t.Fatalf("%s/%s-%s wrote %d audit entries", c.op, c.session, c.caller, len(bundles.entries))
	}
}

// TestBundleWritesNeedAFreshSignIn sends each bundle write with no console
// session, with a stale sign-in, with a stored access token, and with a
// trust token. No check of the bundle runs, and nothing reaches the store.
func TestBundleWritesNeedAFreshSignIn(t *testing.T) {
	t.Parallel()

	for _, c := range bundleGuardCases() {
		t.Run(c.op+"/"+c.session+"-"+c.caller, func(t *testing.T) {
			t.Parallel()

			checkBundleGuardCase(t, c)
		})
	}
}
