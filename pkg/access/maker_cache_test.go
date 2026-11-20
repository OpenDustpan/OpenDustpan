package access_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestCanActAsIgnoresTheRequestCache sends the case of a request whose route
// check cached the access of the maker before a change. CanActAs must read the
// state after the change, with the same context and with a context that
// holds a request cache: a caller cannot make it decide on cached reads.
func TestCanActAsIgnoresTheRequestCache(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		setup  func(t *testing.T, f *fixture, role access.Role, ci identity.Object)
		change func(t *testing.T, f *fixture, role access.Role)
	}{
		{
			"the maker leaves the group that gives the path",
			func(t *testing.T, f *fixture, _ access.Role, ci identity.Object) {
				t.Helper()

				f.grant(t, f.group(t, "deployers", "tina"), access.RoleTokenManager, ci)
			},
			func(t *testing.T, f *fixture, _ access.Role) {
				t.Helper()

				err := f.store.RemoveMember(t.Context(), "grp-deployers", "prn-tina",
					storetest.ServerChange(time.Unix(1, 0), "group:grp-deployers"))
				if err != nil {
					t.Fatalf("remove tina: %v", err)
				}
			},
		},
		{
			"the custom role of the maker loses the action",
			func(t *testing.T, f *fixture, role access.Role, _ identity.Object) {
				t.Helper()

				f.grant(t, prn("tina"), role.ID, identity.ServerObject())
			},
			func(t *testing.T, f *fixture, role access.Role) {
				t.Helper()

				_, err := f.svc.UpdateRole(t.Context(), testEntry(), admin(), role.ID, access.RoleInput{
					Name: role.Name, Description: "", Actions: []identity.Action{identity.ActionServerReadPrincipals},
				})
				if err != nil {
					t.Fatalf("update the role: %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			f.installationService(t, "prn-ci", "ci-deploy")
			f.person(t, "tina")

			ci, err := identity.ParsePrincipalObject("prn-ci")
			if err != nil {
				t.Fatalf("parse the principal object: %v", err)
			}

			role := f.customRole(t, identity.ActionPrincipalManageTokens, identity.ActionServerReadPrincipals)
			tc.setup(t, f, role, ci)

			// The context of the request: its first decision reads the
			// groups of tina and her custom role into the cache.
			ctx := access.WithRequestCache(t.Context())

			can, err := f.svc.CanActAs(ctx, "tina", "prn-ci")
			if err != nil || !can {
				t.Fatalf("CanActAs before the change = %t, %v; want true", can, err)
			}

			tinaCaller := identity.Principal{
				TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "tina", DisplayName: "",
				Admin: false, TokenID: "", TokenExpiresAt: time.Time{}, Kind: identity.KindPerson,
				Credential: "", JobID: "",
			}
			if err := f.svc.Authorizer().Authorize(ctx, tinaCaller, identity.ActionPrincipalManageTokens,
				ci); err != nil {
				t.Fatalf("the route check before the change: %v", err)
			}

			tc.change(t, f, role)

			// The same context, with the cache that holds the old reads.
			if !access.HasRequestCache(ctx) {
				t.Fatal("the test context lost its cache")
			}

			got, err := f.svc.CanActAs(ctx, "tina", "prn-ci")
			if err != nil {
				t.Fatalf("CanActAs after the change: %v", err)
			}

			if got {
				t.Fatal("CanActAs answered true from the cache of the request after the change")
			}
		})
	}
}
