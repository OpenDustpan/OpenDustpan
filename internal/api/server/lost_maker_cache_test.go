package server_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
)

// withRequestCache returns the fixture whose handler gives each request its
// own access cache, as the server does for each request.
func (f accessFixture) withRequestCache() accessFixture {
	inner := f.handler.served

	f.handler.served = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r.WithContext(access.WithRequestCache(r.Context())))
	})

	return f
}

// makeRole makes a custom role with actions as the bootstrap admin and
// returns its ID.
func makeRole(t *testing.T, f accessFixture, name string, actions string) string {
	t.Helper()

	var role struct {
		ID string `json:"id"`
	}

	f.as(t, "admin", apiCall{
		http.MethodPost, "/api/server/roles",
		`{"name":"` + name + `","description":"","actions":[` + actions + `]}`, http.StatusCreated,
	}, &role)

	return role.ID
}

// grantRole gives the custom role to the principal prn-<subject> on the
// installation as the bootstrap admin.
func grantRole(t *testing.T, f accessFixture, subject, role string) {
	t.Helper()

	f.as(t, "admin", apiCall{
		http.MethodPost, "/api/server/grants",
		`{"subject":"principal:prn-` + subject + `","role":"` + role + `"}`, http.StatusCreated,
	}, nil)
}

// TestMakerWhoChangesHerOwnPathLosesTheTokenInTheSameAnswer sends the change
// as the maker herself, through the real handler chain with the request
// cache on. The route check of the request reads the access of the maker
// before the change. The check of the makers after the change must read the
// access after it: the token ends before the answer, and its audit entry
// names the change, not a later use of the token.
func TestMakerWhoChangesHerOwnPathLosesTheTokenInTheSameAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		setup  func(t *testing.T, f accessFixture, role string)
		path   makerPath
		caller string
		call   func(role string) apiCall
		cause  string
	}{
		{
			name: "she removes herself from the group that gives her the path",
			setup: func(t *testing.T, f accessFixture, role string) {
				t.Helper()
				grantRole(t, f, "tina", makeRole(t, f, "group-manager", `"server:manage-groups","server:manage-access"`))
			},
			path: pathGroup,
			call: func(string) apiCall {
				return apiCall{http.MethodDelete, "/api/server/groups/grp-deployers/members/prn-tina", "", http.StatusNoContent}
			},
			cause: "server:manage-groups group:grp-deployers",
		},
		{
			name: "she deletes the group that gives her the path",
			setup: func(t *testing.T, f accessFixture, role string) {
				t.Helper()
				grantRole(t, f, "tina", makeRole(t, f, "group-manager", `"server:manage-groups","server:manage-access"`))
			},
			path: pathGroup,
			call: func(string) apiCall {
				return apiCall{http.MethodDelete, "/api/server/groups/grp-deployers", "", http.StatusNoContent}
			},
			cause: "server:manage-groups group:grp-deployers",
		},
		{
			name: "a role manager who holds the same role removes its action",
			setup: func(t *testing.T, f accessFixture, role string) {
				t.Helper()
				f.seed(t, "rita", "", identity.Object{})
				grantRole(t, f, "rita", makeRole(t, f, "role-manager", `"server:manage-roles","server:manage-access"`))
				// Rita holds the role "ops" too, so her route check reads
				// it into the request cache before the change.
				grantRole(t, f, "rita", role)
			},
			path:   pathDirect,
			caller: "rita",
			call: func(role string) apiCall {
				return apiCall{
					http.MethodPatch, "/api/server/roles/" + role,
					`{"actions":["server:read-principals"]}`, http.StatusOK,
				}
			},
			cause: "server:manage-roles role:",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)

			// Tina gets her act-as path through the custom role "minter"
			// in the direct case, so that the role is in her access, and
			// through the group in the group case.
			seedLostMaker(t, f, pathGroup)

			var role string

			if tc.path == pathDirect {
				// Give the path of the custom role first, then end the path of
				// the group, so that no change ends the last path early.
				role = makeRole(t, f, "ops", `"principal:manage-tokens","server:read-principals"`)
				grantRole(t, f, "tina", role)

				f.as(t, "admin", apiCall{http.MethodDelete, "/api/server/grants/grt-deployers", "", http.StatusNoContent}, nil)
				f.as(t, "admin", apiCall{http.MethodDelete, "/api/server/groups/grp-deployers", "", http.StatusNoContent}, nil)
			}

			tc.setup(t, f, role)

			if lostMakerRevocation(t, f) != nil {
				t.Fatal("the token is revoked before the change")
			}

			caller := tc.caller
			if caller == "" {
				caller = "tina"
			}

			f.withRequestCache().as(t, caller, tc.call(role), nil)

			e := lostMakerRevocation(t, f)
			if e == nil {
				t.Fatal("the token of the maker who removed her own path is still active after the answer")
			}

			if !strings.HasPrefix(causeOf(e), tc.cause) || e.Reason != accesstoken.ReasonMakerLostPath {
				t.Fatalf("revocation entry = %+v, want the cause %q and the reason of a change", *e, tc.cause)
			}
		})
	}
}

// TestMakerKeepsTheTokenWhenTheChangeLeavesAPathWithTheRequestCache proves
// that a fresh read does not revoke more than it must: the maker keeps a
// second path, so the token stays.
func TestMakerKeepsTheTokenWhenTheChangeLeavesAPathWithTheRequestCache(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	seedLostMaker(t, f, pathGroup)

	grantRole(t, f, "tina", makeRole(t, f, "group-manager", `"server:manage-groups","server:manage-access"`))

	obj, err := identity.ParsePrincipalObject("prn-ci")
	if err != nil {
		t.Fatalf("parse the principal object: %v", err)
	}

	f.grant(t, "tina", access.RoleTokenManager, obj)

	f.withRequestCache().as(t, "tina", apiCall{
		http.MethodDelete, "/api/server/groups/grp-deployers/members/prn-tina", "", http.StatusNoContent,
	}, nil)

	if lostMakerRevocation(t, f) != nil {
		t.Fatal("the token of a maker who keeps a direct path was revoked")
	}
}
