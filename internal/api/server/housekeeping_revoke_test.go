package server_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// housekeepingGrantsOnObject returns every current grant on obj that a
// housekeeping principal holds.
func housekeepingGrantsOnObject(t *testing.T, f accessFixture, obj identity.Object) []access.Grant {
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

// housekeepingRevokeAPIFixture returns the access fixture with the person
// sara, a site administrator through a stored grant on @server, the
// namespace gamma that sara made through the route, so gamma has the
// housekeeping grants, and the person nora, namespace administrator of gamma.
func housekeepingRevokeAPIFixture(t *testing.T) accessFixture {
	t.Helper()

	f := newAccessFixture(t)
	f.seed(t, "sara", access.RoleSiteAdministrator, identity.ServerObject())
	f.seed(t, "nora", access.RoleNamespaceAdmin, identity.NamespaceObject("acme"))

	f.as(t, "sara", apiCall{http.MethodPost, "/api/server/namespaces", `{"name":"gamma"}`, http.StatusCreated}, nil)

	if len(housekeepingGrantsOnObject(t, f, identity.NamespaceObject("gamma"))) == 0 {
		t.Fatal("gamma has no housekeeping grant")
	}

	return f
}

// TestHousekeepingRevokeRouteHostileCases checks the housekeeping revoke rule
// at the routes: a namespace administrator cannot revoke a housekeeping
// grant, and a site administrator cannot grant a housekeeping role with an
// action that only the housekeeping roles hold.
func TestHousekeepingRevokeRouteHostileCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		call func(t *testing.T, f accessFixture) (string, apiCall)
		name string
	}{
		{
			name: "a namespace administrator revokes a housekeeping grant on its namespace",
			call: func(t *testing.T, f accessFixture) (string, apiCall) {
				t.Helper()

				f.seed(t, "nora-gamma", access.RoleNamespaceAdmin, identity.NamespaceObject("gamma"))

				g := housekeepingGrantsOnObject(t, f, identity.NamespaceObject("gamma"))[0]

				return "nora-gamma", apiCall{
					http.MethodDelete, "/api/server/namespaces/gamma/grants/" + g.ID, "", http.StatusForbidden,
				}
			},
		},
		{
			name: "a namespace administrator revokes a housekeeping grant on @server by its ID",
			call: func(t *testing.T, f accessFixture) (string, apiCall) {
				t.Helper()

				g := housekeepingGrantsOnObject(t, f, identity.ServerObject())[0]

				return "nora", apiCall{http.MethodDelete, "/api/server/grants/" + g.ID, "", http.StatusNotFound}
			},
		},
		{
			name: "a site administrator grants a housekeeping role on @server",
			call: func(*testing.T, accessFixture) (string, apiCall) {
				return "sara", apiCall{
					http.MethodPost, "/api/server/grants",
					`{"subject":"principal:prn-nora","role":"` + access.RoleHousekeepingBlobSweep + `"}`,
					http.StatusForbidden,
				}
			},
		},
		{
			name: "a site administrator grants a housekeeping role on a namespace",
			call: func(*testing.T, accessFixture) (string, apiCall) {
				return "sara", apiCall{
					http.MethodPost, "/api/server/namespaces/gamma/grants",
					`{"subject":"principal:prn-nora","role":"` + access.RoleHousekeepingJobReconcile + `"}`,
					http.StatusForbidden,
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := housekeepingRevokeAPIFixture(t)
			before := len(housekeepingGrantsOnObject(t, f, identity.ServerObject())) +
				len(housekeepingGrantsOnObject(t, f, identity.NamespaceObject("gamma")))

			subject, c := tc.call(t, f)
			f.as(t, subject, c, nil)

			after := len(housekeepingGrantsOnObject(t, f, identity.ServerObject())) +
				len(housekeepingGrantsOnObject(t, f, identity.NamespaceObject("gamma")))
			if after != before {
				t.Fatalf("housekeeping grants = %d after the refused call, want %d", after, before)
			}
		})
	}
}

// TestStoredSiteAdministratorRevokesHousekeepingGrantsByRoute checks that a
// site administrator with a stored grant revokes each housekeeping grant
// through DELETE /api/server/grants/{id}, and that the entry of each revoke
// has the reason of the housekeeping revoke rule.
func TestStoredSiteAdministratorRevokesHousekeepingGrantsByRoute(t *testing.T) {
	t.Parallel()

	f := housekeepingRevokeAPIFixture(t)

	scopes := []struct {
		obj   identity.Object
		scope namespace.Name
	}{
		{obj: identity.ServerObject(), scope: audit.ServerScope},
		{obj: identity.NamespaceObject("gamma"), scope: "gamma"},
	}

	for _, s := range scopes {
		grants := housekeepingGrantsOnObject(t, f, s.obj)

		for i := range grants {
			g := &grants[i]
			f.as(t, "sara", apiCall{http.MethodDelete, "/api/server/grants/" + g.ID, "", http.StatusNoContent}, nil)

			if n := revokeRuleEntryCount(t, f, s.scope, *g); n != 1 {
				t.Fatalf("entries of the revoke of %s with the rule reason = %d, want 1", g.ID, n)
			}
		}

		if left := housekeepingGrantsOnObject(t, f, s.obj); len(left) != 0 {
			t.Fatalf("housekeeping grants on %s after the revokes = %d, want 0", s.obj.String(), len(left))
		}
	}
}

// revokeRuleEntryCount returns the count of the entries in scope of the
// revoke of g with the reason access.HousekeepingRevokeReason.
func revokeRuleEntryCount(t *testing.T, f accessFixture, scope namespace.Name, g access.Grant) int {
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
