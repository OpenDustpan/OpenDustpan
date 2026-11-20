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
func makeRole(t *testing.T, f accessFixture, name, actions string) string {
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

// wantRevokedByChange sends call as caller through the handler chain with the
// request cache on, and fails unless the token of the maker ended before the
// answer, with an entry whose cause starts with cause.
func wantRevokedByChange(t *testing.T, f accessFixture, caller string, call apiCall, cause string) {
	t.Helper()

	if lostMakerRevocation(t, f) != nil {
		t.Fatal("the token is revoked before the change")
	}

	f.withRequestCache().as(t, caller, call, nil)

	e := lostMakerRevocation(t, f)
	if e == nil {
		t.Fatal("the token of the maker who lost her path is still active after the answer")
	}

	if !strings.HasPrefix(causeOf(e), cause) || e.Reason != accesstoken.ReasonMakerLostPath {
		t.Fatalf("revocation entry = %+v, want the cause %q and the reason of a change", *e, cause)
	}
}

// groupManager lets tina manage groups, and so remove herself from one. The
// removal of a member of a group that holds a principal action also needs
// server:manage-access.
func groupManager(t *testing.T, f accessFixture) {
	t.Helper()

	role := makeRole(t, f, "group-manager", `"server:manage-groups","server:manage-access"`)
	grantRole(t, f, "tina", role)
}

// TestMakerWhoRemovesHerselfFromTheGroupLosesTheToken sends the issue case: tina
// removes herself from grp-deployers through the real handler chain with the
// request cache on. The route check caches her groups before the removal.
// Her token for prn-ci ends before the answer, and the entry names the group
// change.
func TestMakerWhoRemovesHerselfFromTheGroupLosesTheToken(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	seedLostMaker(t, f, pathGroup)
	groupManager(t, f)

	wantRevokedByChange(t, f, "tina", apiCall{
		http.MethodDelete, "/api/server/groups/grp-deployers/members/prn-tina", "", http.StatusNoContent,
	}, "server:manage-groups group:grp-deployers")
}

// TestMakerWhoDeletesTheGroupLosesTheToken sends the delete of the group
// that gives the path, by the maker herself, with the request cache on.
func TestMakerWhoDeletesTheGroupLosesTheToken(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	seedLostMaker(t, f, pathGroup)
	groupManager(t, f)

	wantRevokedByChange(t, f, "tina", apiCall{
		http.MethodDelete, "/api/server/groups/grp-deployers", "", http.StatusNoContent,
	}, "server:manage-groups group:grp-deployers")
}

// TestRoleChangeWithTheRequestCacheRevokesTheToken sends the second case: the
// last path of tina is the custom role "ops" on the installation. Rita holds
// the same role, so the route check of her request reads the role into the
// request cache. She removes principal:manage-tokens from the role. The
// token of tina ends before the answer.
func TestRoleChangeWithTheRequestCacheRevokesTheToken(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	seedLostMaker(t, f, pathGroup)

	role := makeRole(t, f, "ops", `"principal:manage-tokens","server:read-principals"`)
	grantRole(t, f, "tina", role)

	f.seed(t, "rita", "", identity.Object{})
	grantRole(t, f, "rita", makeRole(t, f, "role-manager", `"server:manage-roles","server:manage-access"`))
	grantRole(t, f, "rita", role)

	// End the path of the group, so that the role is the last path.
	f.as(t, "admin", apiCall{http.MethodDelete, "/api/server/grants/grt-deployers", "", http.StatusNoContent}, nil)
	f.as(t, "admin", apiCall{http.MethodDelete, "/api/server/groups/grp-deployers", "", http.StatusNoContent}, nil)

	wantRevokedByChange(t, f, "rita", apiCall{
		http.MethodPatch, "/api/server/roles/" + role, `{"actions":["server:read-principals"]}`, http.StatusOK,
	}, "server:manage-roles role:")
}

// TestMakerKeepsTheTokenWhenTheChangeLeavesAPathWithTheRequestCache proves
// that a fresh read does not revoke more than it must: the maker keeps a
// second path, so the token stays.
func TestMakerKeepsTheTokenWhenTheChangeLeavesAPathWithTheRequestCache(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	seedLostMaker(t, f, pathGroup)

	groupManager(t, f)

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
