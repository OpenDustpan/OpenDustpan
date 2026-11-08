package access_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// onlyGrantReads holds the methods of the GrantReads port and no other: it
// hides the principal read of the store.
type onlyGrantReads struct {
	access.GrantReads
}

// hiddenPrincipalStore runs the check of a grant with reads that cannot read
// a principal.
type hiddenPrincipalStore struct {
	*sqlite.Store
}

func (s hiddenPrincipalStore) CreateGrant(
	ctx context.Context, g access.Grant, fence namespace.Fence, e audit.Entry, check access.GrantCheck,
) error {
	return s.Store.CreateGrant(ctx, g, fence, e, func(ctx context.Context, r access.GrantReads) error {
		return check(ctx, onlyGrantReads{GrantReads: r})
	})
}

// TestGrantCheckReadsPrincipalsOnlyInTheWrite checks that the check of a
// grant reads the namespace of a grouped granter in its own write, and that
// it refuses the grant when the reads of the write cannot read a principal.
func TestGrantCheckReadsPrincipalsOnlyInTheWrite(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		hidden bool
	}{
		{name: "reads that read a principal", hidden: false},
		{name: "reads that cannot read a principal", hidden: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			acme := identity.NamespaceObject("acme")
			bob := f.person(t, "bob")
			f.person(t, "carol")
			ops := f.group(t, "ops", "bob")
			f.grant(t, ops, access.RoleAdmin, acme)

			var st access.Store = f.store
			if tc.hidden {
				st = hiddenPrincipalStore{Store: f.store}
			}

			svc := serviceOver(t, f, st)

			_, err := svc.Grant(t.Context(), testEntry(), bob, access.GrantInput{
				Object: acme, Role: access.RoleViewer, Subject: prn("carol"),
			})

			if !tc.hidden {
				if err != nil {
					t.Fatalf("grant by a grouped granter: %v", err)
				}

				return
			}

			if err == nil || errors.Is(err, access.ErrNotFound) {
				t.Fatalf("grant with no principal read in the write: err = %v, want a refusal", err)
			}

			assertNoGrants(t, f, prn("carol"), acme)
		})
	}
}

// serviceOver returns a Service over st, with the other ports of f.
func serviceOver(t *testing.T, f *fixture, st access.Store) *access.Service {
	t.Helper()

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: st, Callers: f.store, Collections: f.units, Units: f.store, Principals: f.store,
	})
	if err != nil {
		t.Fatalf("new authorizer: %v", err)
	}

	svc, err := access.NewService(access.ServiceConfig{
		Store: st, Directory: f.store, Namespaces: f.store, Authorizer: authz, Now: nil, NewID: nil,
		BootstrapAdmins: []string{"admin"}, LimitGroups: nil,
		Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return svc
}
