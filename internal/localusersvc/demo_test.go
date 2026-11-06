package localusersvc_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// demoSeedFor builds a demo seed over the store of f. The store claims kind.
func demoSeedFor(t *testing.T, f *fixture, kind installation.Kind) (*localusersvc.DemoSeed, error) {
	t.Helper()

	if _, err := f.store.ClaimKind(t.Context(), kind); err != nil {
		t.Fatalf("claim kind: %v", err)
	}

	return localusersvc.NewDemoSeed(localusersvc.DemoConfig{
		Passwords: f.store, Principals: f.principals, Grants: f.store, Namespaces: f.store, Hasher: f.hasher,
		Kind: kind, Now: nil, Random: nil,
	})
}

// makeNamespaces makes the namespaces in the store and answers their names.
func makeNamespaces(t *testing.T, f *fixture, names ...string) []namespace.Name {
	t.Helper()

	svc, err := namespace.NewService(namespace.Config{
		Store: f.store, Now: nil, Installation: namespace.InstallationName("local"),
	})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	out := make([]namespace.Name, 0, len(names))

	for _, n := range names {
		if _, err := svc.Create(t.Context(), namespace.Name(n)); err != nil {
			t.Fatalf("create namespace %s: %v", n, err)
		}

		out = append(out, namespace.Name(n))
	}

	return out
}

// demoGrants answers the current grants of the demo person as
// "role object" texts, sorted.
func demoGrants(ctx context.Context, t *testing.T, f *fixture) (string, []string) {
	t.Helper()

	person, err := f.store.GetPrincipalBySubject(ctx, localuser.DemoSubject)
	if err != nil {
		t.Fatalf("demo principal: %v", err)
	}

	grants, err := f.store.GrantsOf(ctx, access.Subject{Kind: access.SubjectPrincipal, ID: person.ID})
	if err != nil {
		t.Fatalf("grants of the demo person: %v", err)
	}

	got := make([]string, 0, len(grants))

	for i := range grants {
		if grants[i].CreatedBy != localusersvc.DemoCreator {
			t.Errorf(
				"grant %s has the creator %q, want %q",
				grants[i].ID,
				grants[i].CreatedBy,
				localusersvc.DemoCreator,
			)
		}

		got = append(got, grants[i].Role+" "+grants[i].Object.String())
	}

	slices.Sort(got)

	return person.ID, got
}

// TestDemoSeedGrantsExactlyTheTwoRoles proves the grants of the demo person:
// the admin role on each named namespace and the viewer role on the
// installation, and never the site administrator role.
func TestDemoSeedGrantsExactlyTheTwoRoles(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	seed, err := demoSeedFor(t, f, installation.KindDev)
	if err != nil {
		t.Fatalf("new demo seed: %v", err)
	}

	names := makeNamespaces(t, f, "dev", "demo-platform", "demo-data")

	password, err := seed.Run(t.Context(), names)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if password == "" {
		t.Fatal("run answers no password")
	}

	_, got := demoGrants(t.Context(), t, f)

	want := []string{
		"admin " + identity.NamespaceObject("demo-data").String(),
		"admin " + identity.NamespaceObject("demo-platform").String(),
		"admin " + identity.NamespaceObject("dev").String(),
		"viewer " + identity.ServerObject().String(),
	}
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Fatalf("grants of the demo person = %v, want %v", got, want)
	}

	for _, g := range got {
		if strings.HasPrefix(g, access.RoleSiteAdministrator+" ") {
			t.Errorf("the demo person holds the role %s", access.RoleSiteAdministrator)
		}
	}

	if !personCanSignIn(t, f, password) {
		t.Fatal("the demo person cannot verify the printed password")
	}
}

// personCanSignIn reports whether the stored hash of the demo person matches
// password.
func personCanSignIn(t *testing.T, f *fixture, password string) bool {
	t.Helper()

	cred, err := f.store.UserByName(t.Context(), localuser.DemoUserName)
	if err != nil || !cred.User.Demo {
		t.Fatalf("demo user = %+v, %v", cred.User, err)
	}

	if _, verr := f.hasher.Verify(t.Context(), password, cred.Hash, localuser.DefaultHashParams()); verr != nil {
		return false
	}

	return true
}

// TestDemoSeedSecondStartResetsThePassword proves that a second run sets a new
// password, keeps one person and one set of grants.
func TestDemoSeedSecondStartResetsThePassword(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	seed, err := demoSeedFor(t, f, installation.KindDev)
	if err != nil {
		t.Fatalf("new demo seed: %v", err)
	}

	names := makeNamespaces(t, f, "dev")

	first, err := seed.Run(t.Context(), names)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	id1, grants1 := demoGrants(t.Context(), t, f)

	second, err := seed.Run(t.Context(), names)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	id2, grants2 := demoGrants(t.Context(), t, f)

	if first == second {
		t.Error("the second run keeps the password")
	}

	if id1 != id2 || !slices.Equal(grants1, grants2) {
		t.Errorf("second run: person %s grants %v, want person %s grants %v", id2, grants2, id1, grants1)
	}

	if personCanSignIn(t, f, first) || !personCanSignIn(t, f, second) {
		t.Error("only the password of the second run signs in")
	}
}

// TestDemoSeedRefusesAProductionKind proves that the seed refuses an
// installation of another kind at both layers: the constructor, and the store
// when a seed that was built for dev meets a database of kind production.
func TestDemoSeedRefusesAProductionKind(t *testing.T) {
	t.Parallel()

	t.Run("constructor", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)

		if _, err := demoSeedFor(t, f, installation.KindProduction); !errors.Is(err, localuser.ErrDemoOnly) {
			t.Fatalf("new demo seed on production = %v, want %v", err, localuser.ErrDemoOnly)
		}
	})

	t.Run("store", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)

		// Defense in depth: the seed is built for dev, and the database
		// belongs to production. The store reads the kind again.
		if _, err := f.store.ClaimKind(t.Context(), installation.KindProduction); err != nil {
			t.Fatalf("claim kind: %v", err)
		}

		seed, err := localusersvc.NewDemoSeed(localusersvc.DemoConfig{
			Passwords: f.store, Principals: f.principals, Grants: f.store, Namespaces: f.store, Hasher: f.hasher,
			Kind: installation.KindDev, Now: nil, Random: nil,
		})
		if err != nil {
			t.Fatalf("new demo seed: %v", err)
		}

		if _, err := seed.Run(t.Context(), nil); !errors.Is(err, localuser.ErrDemoOnly) {
			t.Fatalf("run on a production database = %v, want %v", err, localuser.ErrDemoOnly)
		}
	})
}

// TestDemoPersonSubjectsMatch proves that the subject of the principal package
// and the one of the local user package are the same value, and that the
// principal service makes no other dev subject through the demo path.
func TestDemoPersonSubjectsMatch(t *testing.T) {
	t.Parallel()

	if principal.DemoSubject != localuser.DemoSubject {
		t.Fatalf("principal.DemoSubject = %q, localuser.DemoSubject = %q", principal.DemoSubject, localuser.DemoSubject)
	}
}
