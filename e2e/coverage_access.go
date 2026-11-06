//go:build e2e

package e2e

import (
	"net/http"
)

// delegationCoveringTest names the test that proves the routes of the
// tokens, the grants and the export of one service principal instead of a
// CLI: no CLI calls them.
const delegationCoveringTest = "TestServicePrincipalTokens"

// delegationAllowEntries returns the operator and console routes of one
// service principal: its tokens, the grants on it and its export. The
// harness steps of delegationCoveringTest drive each one through the real
// binary on each store.
func delegationAllowEntries() []AllowEntry {
	reason := "Operator and console route: no CLI manages the tokens of a service principal or the grants on it. " +
		"The harness step calls it through the real binary and checks the answer."

	const (
		root   = "/api/server/principals/{principal}"
		tokens = root + "/tokens"
		grants = root + "/grants"
	)

	routes := []struct{ method, path string }{
		{http.MethodPost, tokens},
		{http.MethodGet, tokens},
		{http.MethodPost, tokens + "/{id}/rotate"},
		{http.MethodDelete, tokens + "/{id}"},
		{http.MethodGet, grants},
		{http.MethodPost, grants},
		{http.MethodDelete, grants + "/{id}"},
		{http.MethodGet, root + "/access-review"},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: delegationCoveringTest, Reason: reason,
		})
	}

	return out
}

// principalCoveringTest names the test that proves the principal and group
// routes instead of a CLI: no CLI calls them (ADR 52).
const principalCoveringTest = "TestPrincipalsAndGroups"

// principalAllowEntries returns the operator and console routes of the
// principals and groups of the installation. The harness steps of
// principalCoveringTest drive each one through the real binary on each store.
func principalAllowEntries() []AllowEntry {
	reason := "Operator and console route (ADR 52): no CLI manages principals or groups. " +
		"The harness step calls it through the real binary and checks the answer."

	const (
		principals = "/api/server/principals"
		principal  = principals + "/{id}"
		groups     = "/api/server/groups"
		group      = groups + "/{id}"
		members    = group + "/members"
	)

	routes := []struct{ method, path string }{
		{http.MethodPost, principals},
		{http.MethodGet, principals},
		{http.MethodGet, principal},
		{http.MethodPatch, principal},
		{http.MethodDelete, principal},
		{http.MethodGet, principal + "/groups"},
		{http.MethodPost, groups},
		{http.MethodGet, groups},
		{http.MethodGet, group},
		{http.MethodPatch, group},
		{http.MethodDelete, group},
		{http.MethodGet, members},
		{http.MethodPost, members},
		{http.MethodDelete, members + "/{principal}"},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: principalCoveringTest, Reason: reason,
		})
	}

	return out
}

// accessCoveringTest names the test that proves the role and grant routes
// instead of a CLI: no CLI calls them (ADR 52).
const accessCoveringTest = "TestRolesAndGrants"

// accessAllowEntries returns the operator and console routes of the roles
// and grants of the installation. The harness steps of accessCoveringTest
// drive each one through the real binary on each store.
func accessAllowEntries() []AllowEntry {
	reason := "Operator and console route (ADR 52): no CLI manages roles or grants. " +
		"The harness step calls it through the real binary and checks the answer."

	const (
		roles = "/api/server/roles"
		role  = roles + "/{id}"
	)

	grantPaths := []string{
		"/api/server/grants",
		"/api/server/namespaces/{namespace}/grants",
		"/api/server/namespaces/{namespace}/collections/{collection}/grants",
		"/api/server/namespaces/{namespace}/unit-grants",
	}

	roleRoutes := []struct{ method, path string }{
		{http.MethodGet, roles},
		{http.MethodGet, role},
		{http.MethodPost, roles},
		{http.MethodPatch, role},
		{http.MethodDelete, role},
	}

	const perGrantPath = 3 // a list, a grant and a revoke

	routes := make([]struct{ method, path string }, 0, len(roleRoutes)+perGrantPath*len(grantPaths))
	routes = append(routes, roleRoutes...)

	for _, grants := range grantPaths {
		routes = append(routes,
			struct{ method, path string }{http.MethodGet, grants},
			struct{ method, path string }{http.MethodPost, grants},
			struct{ method, path string }{http.MethodDelete, grants + "/{id}"},
		)
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: accessCoveringTest, Reason: reason,
		})
	}

	return out
}

// tokenCoveringTest names the test that proves the access token routes
// instead of a CLI: no CLI calls them (ADR 53).
const tokenCoveringTest = "TestAccessTokens"

// tokenAllowEntries returns the operator and console routes of the access
// tokens. The harness steps of tokenCoveringTest drive each one through the
// real binary on each store, and sign in with the tokens they issue.
func tokenAllowEntries() []AllowEntry {
	reason := "Operator and console route (ADR 53): no CLI manages access tokens. " +
		"The harness step calls it through the real binary and signs in with the token it issues."

	const (
		list = "/api/server/tokens"
		one  = list + "/{id}"
	)

	routes := []struct{ method, path string }{
		{http.MethodGet, list},
		{http.MethodGet, one},
		{http.MethodDelete, one},
		{http.MethodPost, "/api/server/service-tokens"},
		{http.MethodPost, "/api/server/service-tokens/{id}/rotate"},
		{http.MethodGet, "/api/server/caller/tokens"},
		{http.MethodPost, "/api/server/caller/tokens"},
		{http.MethodDelete, "/api/server/caller/tokens/{id}"},
		{http.MethodPost, "/api/server/caller/tokens/{id}/rotate"},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: tokenCoveringTest, Reason: reason,
		})
	}

	return out
}

// tokenPolicyAllowEntries returns the operator and console routes of the
// token policy and the principal enable and disable (ADR 53). The harness steps of
// TestTokenPolicy drive each one through the real binary on each store.
func tokenPolicyAllowEntries() []AllowEntry {
	reason := "Operator and console route (ADR 53): no CLI changes the token policy or enables or disables a principal. " +
		"The harness step calls it through the real binary and checks the answer."

	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/server/token-policy"},
		{http.MethodPatch, "/api/server/token-policy"},
		{http.MethodPost, "/api/server/token-policy/sweep"},
		{http.MethodPost, "/api/server/principals/{id}/enable"},
		{http.MethodPost, "/api/server/principals/{id}/disable"},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: "TestTokenPolicy", Reason: reason,
		})
	}

	return out
}

// reviewCoveringTest names the test that proves the access-review and
// caller-access routes instead of a CLI: no CLI calls them (ADR 52).
const reviewCoveringTest = "TestAccessReview"

// reviewAllowEntries returns the operator and console routes of the access
// review and the access of the caller. The harness steps of
// reviewCoveringTest drive each one through the real binary on each store.
func reviewAllowEntries() []AllowEntry {
	reason := "Operator and console route (ADR 52): no CLI reads the access review or the caller access. " +
		"The harness step calls it through the real binary and checks the answer."

	paths := []string{
		"/api/server/access-review",
		"/api/server/namespaces/{namespace}/access-review",
		"/api/server/namespaces/{namespace}/collections/{collection}/access-review",
		"/api/server/namespaces/{namespace}/unit-access-review",
		"/api/server/caller/access",
		"/api/server/caller/grants",
	}

	out := make([]AllowEntry, 0, len(paths))
	for _, path := range paths {
		out = append(out, AllowEntry{
			Method: http.MethodGet, Path: path, CoveringTest: reviewCoveringTest, Reason: reason,
		})
	}

	return out
}
