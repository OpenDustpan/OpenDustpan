package dustpan_test

import (
	"fmt"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// outsideByDesign lists the path prefixes that stay outside the prefix of the
// tool-neutral REST API for good: the tool-compatible APIs, the probes, the
// runner protocol, the discovery and identity paths that an outside system
// stores, the MCP endpoint, the metrics and the chat actions. A route here is
// not in the description. It is the one list of these paths: the check of each
// route reads it, and no other test keeps a second copy.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant slices.
var outsideByDesign = []string{
	// The Pulumi Cloud-compatible API.
	"/api/capabilities", "/api/stacks/", "/api/user",
	// The TFE-compatible API and its discovery document.
	"/api/v2/", "/.well-known/terraform.json",
	// The OpenTofu HTTP backend.
	"/tofu/state/",
	// The probes.
	"/health/",
	// The runner protocol.
	"/runner/v1/",
	// Paths that an identity provider, a cloud trust policy, an MCP client, a
	// scrape configuration or a chat app stores.
	"/oidc/", "/sign-in/", "/mcp", "/metrics", "/chat/",
}

// notMoved lists the routes of the tool-neutral REST API that still sit
// outside the prefix, or that sit under it with a wildcard name that the
// description cannot hold. The key is the pattern now served. The value is
// the path that the route will have. The list shrinks to zero when the move
// ends: a route that moves leaves the list in the same change.
//
// The new path of a row that the move table gives is that path. The new path
// of another row follows the rules of the move table.
//
//nolint:gochecknoglobals,gosec // A fixed table; Go has no constant maps. The paths name tokens and hold no secret.
var notMoved = map[string]string{
	"POST /api/server/namespaces":                                                    "/api/dustpan/v1/namespaces",
	"GET /api/server/namespaces":                                                     "/api/dustpan/v1/namespaces",
	"GET /api/server/metrics":                                                        "/metrics",
	"POST /api/server/backups":                                                       "/api/dustpan/v1/backups",
	"GET /api/server/backups":                                                        "/api/dustpan/v1/backups",
	"GET /api/server/backups/{id}":                                                   "/api/dustpan/v1/backups/{id}",
	"GET /api/server/backups/{id}/check":                                             "/api/dustpan/v1/backups/{id}/check",
	"GET /api/server/upgrade-check":                                                  "/api/dustpan/v1/upgrade-check",
	"GET /api/server/components":                                                     "/api/dustpan/v1/components",
	"GET /api/server/worker":                                                         "/api/dustpan/v1/worker",
	"POST /api/server/restores":                                                      "/api/dustpan/v1/restores",
	"GET /api/server/restores":                                                       "/api/dustpan/v1/restores",
	"GET /api/server/restores/{id}":                                                  "/api/dustpan/v1/restores/{id}",
	"GET /api/server/maintenance":                                                    "/api/dustpan/v1/maintenance",
	"POST /api/server/maintenance/enter":                                             "/api/dustpan/v1/maintenance/enter",
	"POST /api/server/maintenance/leave":                                             "/api/dustpan/v1/maintenance/leave",
	"GET /api/server/audit/chain":                                                    "/api/dustpan/v1/audit/chain",
	"POST /api/server/audit/chain":                                                   "/api/dustpan/v1/audit/chain",
	"POST /api/server/deleted-units/sweep":                                           "/api/dustpan/v1/deleted-units/sweep",
	"GET /api/server/namespaces/{namespace}/override-switches":                       "/api/dustpan/v1/namespaces/{namespace}/override-switches",
	"PUT /api/server/namespaces/{namespace}/override-switches":                       "/api/dustpan/v1/namespaces/{namespace}/override-switches",
	"POST /api/server/principals":                                                    "/api/dustpan/v1/principals",
	"GET /api/server/principals":                                                     "/api/dustpan/v1/principals",
	"GET /api/server/principals/{id}":                                                "/api/dustpan/v1/principals/{id}",
	"PATCH /api/server/principals/{id}":                                              "/api/dustpan/v1/principals/{id}",
	"DELETE /api/server/principals/{id}":                                             "/api/dustpan/v1/principals/{id}",
	"GET /api/server/principals/{id}/groups":                                         "/api/dustpan/v1/principals/{id}/groups",
	"POST /api/server/principals/{id}/enable":                                        "/api/dustpan/v1/principals/{id}/enable",
	"POST /api/server/principals/{id}/disable":                                       "/api/dustpan/v1/principals/{id}/disable",
	"POST /api/server/namespaces/{namespace}/principals":                             "/api/dustpan/v1/namespaces/{namespace}/principals",
	"GET /api/server/namespaces/{namespace}/principals":                              "/api/dustpan/v1/namespaces/{namespace}/principals",
	"GET /api/server/namespaces/{namespace}/principals/{id}":                         "/api/dustpan/v1/namespaces/{namespace}/principals/{id}",
	"PATCH /api/server/namespaces/{namespace}/principals/{id}":                       "/api/dustpan/v1/namespaces/{namespace}/principals/{id}",
	"DELETE /api/server/namespaces/{namespace}/principals/{id}":                      "/api/dustpan/v1/namespaces/{namespace}/principals/{id}",
	"POST /api/server/groups":                                                        "/api/dustpan/v1/groups",
	"GET /api/server/groups":                                                         "/api/dustpan/v1/groups",
	"GET /api/server/groups/{id}":                                                    "/api/dustpan/v1/groups/{id}",
	"PATCH /api/server/groups/{id}":                                                  "/api/dustpan/v1/groups/{id}",
	"DELETE /api/server/groups/{id}":                                                 "/api/dustpan/v1/groups/{id}",
	"GET /api/server/groups/{id}/members":                                            "/api/dustpan/v1/groups/{id}/members",
	"POST /api/server/groups/{id}/members":                                           "/api/dustpan/v1/groups/{id}/members",
	"DELETE /api/server/groups/{id}/members/{principal}":                             "/api/dustpan/v1/groups/{id}/members/{principal}",
	"GET /api/server/roles":                                                          "/api/dustpan/v1/roles",
	"GET /api/server/roles/{id}":                                                     "/api/dustpan/v1/roles/{id}",
	"POST /api/server/roles":                                                         "/api/dustpan/v1/roles",
	"PATCH /api/server/roles/{id}":                                                   "/api/dustpan/v1/roles/{id}",
	"DELETE /api/server/roles/{id}":                                                  "/api/dustpan/v1/roles/{id}",
	"GET /api/server/grants":                                                         "/api/dustpan/v1/grants",
	"POST /api/server/grants":                                                        "/api/dustpan/v1/grants",
	"GET /api/server/namespaces/{namespace}/grants":                                  "/api/dustpan/v1/namespaces/{namespace}/grants",
	"POST /api/server/namespaces/{namespace}/grants":                                 "/api/dustpan/v1/namespaces/{namespace}/grants",
	"DELETE /api/server/namespaces/{namespace}/grants/{id}":                          "/api/dustpan/v1/namespaces/{namespace}/grants/{id}",
	"GET /api/server/namespaces/{namespace}/collections/{collection}/grants":         "/api/dustpan/v1/namespaces/{namespace}/collections/{collection}/grants",
	"POST /api/server/namespaces/{namespace}/collections/{collection}/grants":        "/api/dustpan/v1/namespaces/{namespace}/collections/{collection}/grants",
	"DELETE /api/server/namespaces/{namespace}/collections/{collection}/grants/{id}": "/api/dustpan/v1/namespaces/{namespace}/collections/{collection}/grants/{id}",
	"GET /api/server/namespaces/{namespace}/unit-grants":                             "/api/dustpan/v1/namespaces/{namespace}/unit-grants",
	"POST /api/server/namespaces/{namespace}/unit-grants":                            "/api/dustpan/v1/namespaces/{namespace}/unit-grants",
	"DELETE /api/server/namespaces/{namespace}/unit-grants/{id}":                     "/api/dustpan/v1/namespaces/{namespace}/unit-grants/{id}",
	"GET /api/server/principals/{principal}/grants":                                  "/api/dustpan/v1/principals/{principal}/grants",
	"POST /api/server/principals/{principal}/grants":                                 "/api/dustpan/v1/principals/{principal}/grants",
	"DELETE /api/server/principals/{principal}/grants/{id}":                          "/api/dustpan/v1/principals/{principal}/grants/{id}",
	"DELETE /api/server/grants/{id}":                                                 "/api/dustpan/v1/grants/{id}",
	"GET /api/server/access-review":                                                  "/api/dustpan/v1/access-review",
	"GET /api/server/namespaces/{namespace}/access-review":                           "/api/dustpan/v1/namespaces/{namespace}/access-review",
	"GET /api/server/namespaces/{namespace}/collections/{collection}/access-review":  "/api/dustpan/v1/namespaces/{namespace}/collections/{collection}/access-review",
	"GET /api/server/namespaces/{namespace}/unit-access-review":                      "/api/dustpan/v1/namespaces/{namespace}/unit-access-review",
	"GET /api/server/principals/{principal}/access-review":                           "/api/dustpan/v1/principals/{principal}/access-review",
	"GET /api/server/caller/access":                                                  "/api/dustpan/v1/me/access",
	"GET /api/server/caller/grants":                                                  "/api/dustpan/v1/me/grants",
	"GET /api/server/tokens":                                                         "/api/dustpan/v1/tokens",
	"GET /api/server/tokens/{id}":                                                    "/api/dustpan/v1/tokens/{id}",
	"DELETE /api/server/tokens/{id}":                                                 "/api/dustpan/v1/tokens/{id}",
	"POST /api/server/service-tokens":                                                "/api/dustpan/v1/service-tokens",
	"POST /api/server/service-tokens/{id}/rotate":                                    "/api/dustpan/v1/service-tokens/{id}/rotate",
	"POST /api/server/principals/{principal}/tokens":                                 "/api/dustpan/v1/principals/{principal}/tokens",
	"GET /api/server/principals/{principal}/tokens":                                  "/api/dustpan/v1/principals/{principal}/tokens",
	"POST /api/server/principals/{principal}/tokens/{id}/rotate":                     "/api/dustpan/v1/principals/{principal}/tokens/{id}/rotate",
	"DELETE /api/server/principals/{principal}/tokens/{id}":                          "/api/dustpan/v1/principals/{principal}/tokens/{id}",
	"GET /api/server/caller/tokens":                                                  "/api/dustpan/v1/me/tokens",
	"POST /api/server/caller/tokens":                                                 "/api/dustpan/v1/me/tokens",
	"DELETE /api/server/caller/tokens/{id}":                                          "/api/dustpan/v1/me/tokens/{id}",
	"POST /api/server/caller/tokens/{id}/rotate":                                     "/api/dustpan/v1/me/tokens/{id}/rotate",
	"GET /api/server/token-policy":                                                   "/api/dustpan/v1/token-policy",
	"PATCH /api/server/token-policy":                                                 "/api/dustpan/v1/token-policy",
	"POST /api/server/token-policy/sweep":                                            "/api/dustpan/v1/token-policy/sweep",
	"GET /api/server/session-policy":                                                 "/api/dustpan/v1/session-policy",
	"PATCH /api/server/session-policy":                                               "/api/dustpan/v1/session-policy",
	"GET /api/server/audit/sinks":                                                    "/api/dustpan/v1/audit/sinks",
	"POST /api/server/audit/checkpoint-epochs":                                       "/api/dustpan/v1/audit/checkpoint-epochs",
	"GET /api/server/sign-in/{provider}":                                             "/api/dustpan/v1/sign-in/{id}",
	"GET /api/server/sign-in/{provider}/callback":                                    "/sign-in/{id}/callback",
	"GET /api/server/session":                                                        "/api/dustpan/v1/session",
	"POST /api/server/sign-out":                                                      "/api/dustpan/v1/sign-out",
	"GET /api/server/sign-in-providers":                                              "/api/dustpan/v1/sign-in-providers",
	"POST /api/server/sign-in-providers":                                             "/api/dustpan/v1/sign-in-providers",
	"GET /api/server/sign-in-providers/{id}":                                         "/api/dustpan/v1/sign-in-providers/{id}",
	"PATCH /api/server/sign-in-providers/{id}":                                       "/api/dustpan/v1/sign-in-providers/{id}",
	"DELETE /api/server/sign-in-providers/{id}":                                      "/api/dustpan/v1/sign-in-providers/{id}",
	"GET /api/dustpan/v1/namespaces/-/units/{unitID}":                                "/api/dustpan/v1/namespaces/-/units/{unit_id}",
	"POST /api/inventory/deleted-units/{tool}/{unitID}/restore":                      "/api/dustpan/v1/namespaces/{namespace}/deleted-units/{unit_id}/restore",
	"DELETE /api/inventory/deleted-units/{tool}/{unitID}":                            "/api/dustpan/v1/namespaces/{namespace}/deleted-units/{unit_id}",
	"PUT /api/inventory/unit-collection":                                             "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/collection",
	"PUT /api/inventory/unit-lock":                                                   "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/lock",
	"GET /api/inventory/unit-lock":                                                   "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/lock",
	"DELETE /api/inventory/unit-lock":                                                "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/lock",
	"POST /api/inventory/unit-lock/force-unlock":                                     "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/lock/force-unlock",
	"GET /api/audit/entries":                                                         "/api/dustpan/v1/audit/entries",
	"GET /api/audit/entries/{sequence}":                                              "/api/dustpan/v1/audit/entries/{sequence}",
	"GET /api/audit/check":                                                           "/api/dustpan/v1/audit/check",
	"GET /api/runners/{namespace}/pools":                                             "/api/dustpan/v1/namespaces/{namespace}/runner-pools",
	"POST /api/runners/{namespace}/pools":                                            "/api/dustpan/v1/namespaces/{namespace}/runner-pools",
	"GET /api/runners/{namespace}/pools/{pool}":                                      "/api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}",
	"PATCH /api/runners/{namespace}/pools/{pool}":                                    "/api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}",
	"DELETE /api/runners/{namespace}/pools/{pool}":                                   "/api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}",
	"GET /api/runners/{namespace}/pools/{pool}/join-tokens":                          "/api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}/join-tokens",
	"POST /api/runners/{namespace}/pools/{pool}/join-tokens":                         "/api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}/join-tokens",
	"POST /api/runners/{namespace}/pools/{pool}/join-tokens/{id}/revoke":             "/api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}/join-tokens/{id}/revoke",
	"GET /api/runners/{namespace}/pools/{pool}/runners":                              "/api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}/runners",
	"POST /api/runners/{namespace}/runners/{runner}/drain":                           "/api/dustpan/v1/namespaces/{namespace}/runners/{runner}/drain",
	"POST /api/runners/{namespace}/runners/{runner}/retire":                          "/api/dustpan/v1/namespaces/{namespace}/runners/{runner}/retire",
}

// routeClass names the three classes of a route of the joined table.
type routeClass string

const (
	// classDescribed is a route under the prefix that the description holds.
	classDescribed routeClass = "described"
	// classOutside is a route on the list outsideByDesign.
	classOutside routeClass = "outside by design"
	// classNotMoved is a route on the list notMoved.
	classNotMoved routeClass = "not moved"
)

// classesOf returns every class that the pattern ("METHOD /path") belongs to,
// with notMoved as the not-moved list.
// A route that belongs to one class only is in order. A route that belongs to
// none or to two or more fails the check that calls this function.
func classesOf(pattern string, notMoved map[string]string) []routeClass {
	_, path, _ := strings.Cut(pattern, " ")

	var out []routeClass

	_, moved := notMoved[pattern]
	underPrefix := strings.HasPrefix(path, apidesc.Prefix)

	if underPrefix && !moved {
		out = append(out, classDescribed)
	}

	if isOutsideByDesign(path) {
		out = append(out, classOutside)
	}

	if moved {
		out = append(out, classNotMoved)
	}

	return out
}

// isOutsideByDesign reports whether path is on the list outsideByDesign. An
// entry that ends in a slash is a prefix. Another entry is a path, and the
// paths below it.
func isOutsideByDesign(path string) bool {
	for _, entry := range outsideByDesign {
		if strings.HasSuffix(entry, "/") {
			if strings.HasPrefix(path, entry) {
				return true
			}

			continue
		}

		if path == entry || strings.HasPrefix(path, entry+"/") {
			return true
		}
	}

	return false
}

// checkClasses returns one error text for each pattern that does not belong to
// exactly one class, and for each entry of notMoved that no pattern serves.
func checkClasses(patterns []string, moved map[string]string) []string {
	var problems []string

	for _, pattern := range patterns {
		classes := classesOf(pattern, moved)

		switch len(classes) {
		case 1:
		case 0:
			problems = append(problems, pattern+" is in no class")
		default:
			problems = append(problems, fmt.Sprintf("%s is in %d classes: %v", pattern, len(classes), classes))
		}
	}

	for old := range moved {
		if !slices.Contains(patterns, old) {
			problems = append(problems, old+" is on the not-moved list but no adapter serves it")
		}
	}

	slices.Sort(problems)

	return problems
}
