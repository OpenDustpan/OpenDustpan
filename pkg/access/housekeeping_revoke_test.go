package access_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// housekeepingGrantsOf returns every current grant on obj that a
// housekeeping principal holds.
func housekeepingGrantsOf(t *testing.T, f *fixture, obj identity.Object) []access.Grant {
	t.Helper()

	list, _, err := f.store.ListGrantsOn(t.Context(), obj, access.GrantQuery{
		AfterSubject: "", AfterRole: "", Subject: "", Role: "", Limit: access.MaxLimit,
	})
	if err != nil {
		t.Fatalf("list grants on %q: %v", obj.String(), err)
	}

	var out []access.Grant

	for i := range list {
		if access.HousekeepingSubject(list[i].Grant.Subject) {
			out = append(out, list[i].Grant)
		}
	}

	return out
}

// revokeRuleEntries returns the count of the entries in scope of the revoke
// of g with the reason access.HousekeepingRevokeReason.
func revokeRuleEntries(t *testing.T, f *fixture, scope namespace.Name, g access.Grant) int {
	t.Helper()

	listing, err := f.store.List(t.Context(), scope, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list audit of %q: %v", scope, err)
	}

	n := 0

	for i := range listing.Entries {
		e := &listing.Entries[i]
		if e.Object == access.GrantRef(g) && e.Reason == access.HousekeepingRevokeReason {
			n++
		}
	}

	return n
}

// housekeepingRevokeFixture returns a fixture with the namespace gamma, made
// by a person, so gamma has the housekeeping grants. The person "site"
// holds the site-administrator role on @server through a stored grant, and
// the person "owner" holds the namespace-admin role on gamma.
func housekeepingRevokeFixture(t *testing.T) (*fixture, revokeCallers) {
	t.Helper()

	f := newFixture(t)
	site := f.person(t, "site")
	f.grant(t, access.Subject{Kind: access.SubjectPrincipal, ID: "prn-site"}, access.RoleSiteAdministrator,
		identity.ServerObject())

	owner := f.person(t, "owner")
	if _, err := f.svc.CreateNamespace(t.Context(), testEntry(), owner, housekeepingNamespace("gamma")); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return f, revokeCallers{site: site, owner: owner}
}

// revokeCallers holds the site administrator and the namespace administrator
// of housekeepingRevokeFixture.
type revokeCallers struct {
	site  identity.Principal
	owner identity.Principal
}

// revokeEachHousekeepingGrant revokes, as caller, each housekeeping grant on
// obj, checks that the entry of each revoke in scope names the rule, and
// returns the count of the revoked grants.
func revokeEachHousekeepingGrant(
	t *testing.T, f *fixture, caller identity.Principal, obj identity.Object, scope namespace.Name,
) int {
	t.Helper()

	grants := housekeepingGrantsOf(t, f, obj)

	for i := range grants {
		g := &grants[i]

		if _, err := f.svc.Revoke(t.Context(), testEntry(), caller, obj, g.ID); err != nil {
			t.Fatalf("site administrator revokes %s (%s on %s): %v", g.ID, g.Role, obj.String(), err)
		}

		if n := revokeRuleEntries(t, f, scope, *g); n != 1 {
			t.Fatalf("entries of the revoke of %s with the rule reason = %d, want 1", g.ID, n)
		}
	}

	if left := housekeepingGrantsOf(t, f, obj); len(left) != 0 {
		t.Fatalf("housekeeping grants on %s after the revokes = %+v, want none", obj.String(), left)
	}

	return len(grants)
}

// TestStoredSiteAdministratorRevokesEachHousekeepingGrant checks that a site
// administrator with a stored grant, not the bootstrap administrator, can
// revoke each grant of a housekeeping principal, on @server and on a
// namespace, and that the entry of each revoke names the rule.
func TestStoredSiteAdministratorRevokesEachHousekeepingGrant(t *testing.T) {
	t.Parallel()

	f, callers := housekeepingRevokeFixture(t)
	site := callers.site

	count := revokeEachHousekeepingGrant(t, f, site, identity.ServerObject(), audit.ServerScope) +
		revokeEachHousekeepingGrant(t, f, site, identity.NamespaceObject("gamma"), "gamma")

	want := 0

	for _, p := range principal.HousekeepingPurposes() {
		if p.ActsOnInstallation() {
			want++
		}

		if p.ActsInsideNamespace() {
			want++
		}
	}

	if count != want {
		t.Fatalf("revoked housekeeping grants = %d, want %d", count, want)
	}
}

// TestHousekeepingRevokeCheckInTheWrite checks the check that a revoke under
// the housekeeping revoke rule runs in its write, with no test of the
// service before it: it refuses a grant of a principal that is not a
// housekeeping principal, and a caller without server:manage-access on
// @server.
func TestHousekeepingRevokeCheckInTheWrite(t *testing.T) {
	t.Parallel()

	gamma := identity.NamespaceObject("gamma")
	chain := []identity.Object{gamma, identity.ServerObject()}

	cases := []struct {
		grant  func(t *testing.T, f *fixture) access.Grant
		caller func(site, owner identity.Principal) identity.Principal
		want   error
		name   string
	}{
		{
			name:   "a site administrator revokes a housekeeping grant",
			grant:  jobReconcileGrant,
			caller: func(site, _ identity.Principal) identity.Principal { return site },
			want:   nil,
		},
		{
			name:   "a namespace administrator revokes a housekeeping grant",
			grant:  jobReconcileGrant,
			caller: func(_, owner identity.Principal) identity.Principal { return owner },
			want:   identity.ErrDenied,
		},
		{
			name: "a site administrator revokes the grant of a person",
			grant: func(t *testing.T, f *fixture) access.Grant {
				t.Helper()

				g := jobReconcileGrant(t, f)
				g.Subject = access.Subject{Kind: access.SubjectPrincipal, ID: "prn-owner"}

				return g
			},
			caller: func(site, _ identity.Principal) identity.Principal { return site },
			want:   access.ErrEscalation,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, callers := housekeepingRevokeFixture(t)
			site, owner := callers.site, callers.owner
			g := tc.grant(t, f)
			check := access.HousekeepingRevokeCheckForTest(f.svc, tc.caller(site, owner), g, chain)

			// The store runs the check in the transaction of the revoke.
			fence, e := storetest.GrantWrite(t, f.store, gamma, time.Unix(1, 0), access.GrantRef(g))

			err := f.store.RevokeGrant(t.Context(), g.ID, fence, e, check)
			if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("check = %v, want %v", err, tc.want)
			}
		})
	}
}

// jobReconcileGrant returns the grant of the job reconcile role on gamma.
func jobReconcileGrant(t *testing.T, f *fixture) access.Grant {
	t.Helper()

	g := housekeepingGrantsOn(t, f, "gamma")[access.RoleHousekeepingJobReconcile]
	if g.ID == "" {
		t.Fatal("no grant of the job reconcile role on gamma")
	}

	return g
}

// TestHousekeepingRevokeRuleHostileCases checks the limits of the
// housekeeping revoke rule in the service: a namespace administrator cannot
// use it, a site administrator cannot grant a housekeeping role with an
// action that only the housekeeping roles hold, and the rule does not apply
// to a grant of a housekeeping role that another principal holds.
func TestHousekeepingRevokeRuleHostileCases(t *testing.T) {
	t.Parallel()

	gamma := identity.NamespaceObject("gamma")

	cases := []struct {
		run  func(t *testing.T, f *fixture, site, owner identity.Principal) error
		name string
	}{
		{
			name: "a namespace administrator revokes a housekeeping grant on its namespace",
			run: func(t *testing.T, f *fixture, _, owner identity.Principal) error {
				t.Helper()

				g := housekeepingGrantsOn(t, f, "gamma")[access.RoleHousekeepingJobReconcile]
				_, err := f.svc.Revoke(t.Context(), testEntry(), owner, gamma, g.ID)

				return err
			},
		},
		{
			name: "a namespace administrator revokes a housekeeping grant on @server",
			run: func(t *testing.T, f *fixture, _, owner identity.Principal) error {
				t.Helper()

				g := housekeepingGrantsOf(t, f, identity.ServerObject())[0]
				_, err := f.svc.RevokeByID(t.Context(), testEntry(), owner, g.ID)

				return err
			},
		},
		{
			name: "a site administrator grants a housekeeping role on @server to a person",
			run: func(t *testing.T, f *fixture, site, _ identity.Principal) error {
				t.Helper()

				_, err := f.svc.Grant(t.Context(), testEntry(), site, access.GrantInput{
					Object: identity.ServerObject(), Role: access.RoleHousekeepingBlobSweep,
					Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-owner"},
				})

				return err
			},
		},
		{
			name: "a site administrator grants a housekeeping role on a namespace to a person",
			run: func(t *testing.T, f *fixture, site, _ identity.Principal) error {
				t.Helper()

				_, err := f.svc.Grant(t.Context(), testEntry(), site, access.GrantInput{
					Object: gamma, Role: access.RoleHousekeepingJobReconcile,
					Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-owner"},
				})

				return err
			},
		},
		{
			name: "a site administrator revokes a housekeeping role that a person holds",
			run: func(t *testing.T, f *fixture, site, _ identity.Principal) error {
				t.Helper()

				person := access.Subject{Kind: access.SubjectPrincipal, ID: "prn-owner"}
				f.grant(t, person, access.RoleHousekeepingBlobSweep, identity.ServerObject())

				list, _, err := f.store.ListGrantsOn(t.Context(), identity.ServerObject(), access.GrantQuery{
					AfterSubject: "", AfterRole: "", Subject: person.String(),
					Role: access.RoleHousekeepingBlobSweep, Limit: access.MaxLimit,
				})
				if err != nil || len(list) != 1 {
					t.Fatalf("grant of the person = %d, %v, want 1", len(list), err)
				}

				_, err = f.svc.RevokeByID(t.Context(), testEntry(), site, list[0].Grant.ID)

				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, callers := housekeepingRevokeFixture(t)
			site, owner := callers.site, callers.owner

			if err := tc.run(t, f, site, owner); !errors.Is(err, access.ErrEscalation) &&
				!errors.Is(err, identity.ErrDenied) {
				t.Fatalf("err = %v, want ErrEscalation or a denial", err)
			}
		})
	}
}
