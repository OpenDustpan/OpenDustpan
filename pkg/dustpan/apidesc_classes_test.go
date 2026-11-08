package dustpan_test

import (
	"fmt"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// outsideByDesign lists the paths that stay outside the prefix of the
// tool-neutral REST API for good. An entry that ends in a slash is a prefix:
// the tool-compatible APIs and the runner protocol, which are whole APIs with
// their own routes. Another entry is one exact path: the discovery and
// identity paths that an outside system stores, the MCP endpoint, the metrics
// and the chat actions. A new route below an exact path is in no class and
// fails the check. A route here is not in the description. It is the one list
// of these paths: the check of each route reads it, and no other test keeps a
// second copy.
//
// The probes under /health/ are not on this list. The server mounts them on
// its root mux, and they are not in the route sources that the check reads.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant slices.
var outsideByDesign = []string{
	// The Pulumi Cloud-compatible API.
	"/api/capabilities", "/api/stacks/", "/api/user", "/api/user/",
	// The TFE-compatible API and its discovery document.
	"/api/v2/", "/.well-known/terraform.json",
	// The OpenTofu HTTP backend.
	"/tofu/state/",
	// The provider and module registry protocols of OpenTofu. Service discovery
	// names the paths, so they cannot move under the prefix.
	"/v1/providers/", "/v1/modules/",
	// The runner protocol.
	"/runner/v1/",
	// The paths that an identity provider, a cloud trust policy, an MCP
	// client, a scrape configuration or a chat app stores.
	"/sign-in/{id}/callback",
	"/oidc/{nsid}/.well-known/openid-configuration", "/oidc/{nsid}/jwks",
	"/mcp", "/metrics",
	"/chat/{tool}/actions",
}

// notMoved lists the routes of the tool-neutral REST API that still sit
// outside the prefix. The key is the pattern now served. The value is
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
	"POST /api/server/restores":                                                      "/api/dustpan/v1/restores",
	"GET /api/server/restores":                                                       "/api/dustpan/v1/restores",
	"GET /api/server/restores/{id}":                                                  "/api/dustpan/v1/restores/{id}",
	"POST /api/server/restores/{id}/cancel":                                          "/api/dustpan/v1/restores/{id}/cancel",
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
	"GET /api/server/token-policy":                                                   "/api/dustpan/v1/settings/token-policy",
	"PATCH /api/server/token-policy":                                                 "/api/dustpan/v1/settings/token-policy",
	"POST /api/server/token-policy/sweep":                                            "/api/dustpan/v1/settings/token-policy/sweep",
	"GET /api/server/session-policy":                                                 "/api/dustpan/v1/settings/session-policy",
	"PATCH /api/server/session-policy":                                               "/api/dustpan/v1/settings/session-policy",
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
	"GET /api/audit/export":                                                          "/api/dustpan/v1/audit/export",
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

// routeClass names the classes of a route of the joined table.
type routeClass string

const (
	// classDescribed is a route under the prefix that is in the operations of
	// the server and in the checked-in description.
	classDescribed routeClass = "described"
	// classUndescribed is a route under the prefix that is on the list
	// apidesc.Undescribed, with a reason.
	classUndescribed routeClass = "undescribed"
	// classOutside is a route on the list outsideByDesign.
	classOutside routeClass = "outside by design"
	// classNotMoved is a route on the list notMoved.
	classNotMoved routeClass = "not moved"
)

// classInput is what the class of a route depends on, besides its pattern.
type classInput struct {
	// moved is the list notMoved.
	moved map[string]string
	// described holds the keys of the operations that are in the operations
	// of the server and in the checked-in description.
	described map[string]bool
	// undescribed is the list apidesc.Undescribed: pattern and reason.
	undescribed map[string]string
}

// newClassInput returns a classInput. A nil list is an empty list.
func newClassInput(moved map[string]string, described map[string]bool, undescribed map[string]string) classInput {
	return classInput{moved: moved, described: described, undescribed: undescribed}
}

// keyOfPattern returns the key that apidesc gives to a pattern: the method
// and the path with the names of the wildcards removed.
func keyOfPattern(pattern string) string {
	method, path, found := strings.Cut(pattern, " ")
	if !found {
		method, path = "", pattern
	}

	return apidesc.Operation{
		ID:              "",
		Method:          method,
		Path:            path,
		Tags:            nil,
		Actions:         nil,
		ActionObjects:   nil,
		Public:          false,
		IdempotencyKey:  false,
		IfMatch:         false,
		IfMatchRequired: false,
		ETag:            false,
		Query:           nil,
		Statuses:        []int{200},
		Bodies:          nil,
	}.Key()
}

// classesOf returns every class that the pattern ("METHOD /path") belongs to.
// A route that belongs to one class only is in order. A route that belongs to
// none or to two or more fails the check that calls this function. A route
// is described only when its key is in both lists of in.described. A path
// under the prefix is not enough.
func classesOf(pattern string, in classInput) []routeClass {
	_, path, found := strings.Cut(pattern, " ")
	if !found {
		path = pattern
	}

	var out []routeClass

	if strings.HasPrefix(path, apidesc.Prefix) && in.described[keyOfPattern(pattern)] {
		out = append(out, classDescribed)
	}

	if _, ok := in.undescribed[pattern]; ok {
		out = append(out, classUndescribed)
	}

	if isOutsideByDesign(path) {
		out = append(out, classOutside)
	}

	if _, ok := in.moved[pattern]; ok {
		out = append(out, classNotMoved)
	}

	return out
}

// isOutsideByDesign reports whether path is on the list outsideByDesign. An
// entry that ends in a slash is a prefix. Another entry is one exact path.
func isOutsideByDesign(path string) bool {
	for _, entry := range outsideByDesign {
		if strings.HasSuffix(entry, "/") {
			if strings.HasPrefix(path, entry) {
				return true
			}

			continue
		}

		if path == entry {
			return true
		}
	}

	return false
}

// checkClasses returns one error text for each pattern that does not belong to
// exactly one class, and for each entry of the lists notMoved and
// undescribed that no pattern serves.
func checkClasses(patterns []string, in classInput) []string {
	var problems []string

	for _, pattern := range patterns {
		classes := classesOf(pattern, in)

		switch len(classes) {
		case 1:
		case 0:
			problems = append(problems, pattern+" is in no class")
		default:
			problems = append(problems, fmt.Sprintf("%s is in %d classes: %v", pattern, len(classes), classes))
		}
	}

	for old := range in.moved {
		if !slices.Contains(patterns, old) {
			problems = append(problems, old+" is on the not-moved list but no adapter serves it")
		}
	}

	for pattern := range in.undescribed {
		if !slices.Contains(patterns, pattern) {
			problems = append(problems, pattern+" is on the undescribed list but no adapter serves it")
		}
	}

	slices.Sort(problems)

	return problems
}

// rootMounts lists the patterns that code registers directly on the root mux.
// Each entry is one exact pattern, and it is the only list of them. A pattern
// under the prefix of the tool-neutral REST API is allowed only as the
// catch-all v1Prefix: an adapter serves each route of that API, and the
// server adds its path from the patterns of the adapter. A pattern that is in
// no entry fails the check, so a root route cannot skip the description.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant slices.
var rootMounts = []string{
	// The catch-all under the prefix: it answers 404 for a path no adapter serves.
	apidesc.Prefix,
	// The tool APIs, the installation APIs and the runner protocol.
	"/api/inventory/", "/api/audit/", "/api/runners/", "/runner/v1/",
	"/api/v2/", "/api/server/", "/api/", "/tofu/state/",
	// The provider registry protocol of OpenTofu. Service discovery names its
	// path, and the client cannot move it under the prefix of the REST API. The
	// TFE adapter serves it and lists the route table in its own tests. The
	// module registry protocol follows the same rule.
	"/v1/providers/", "/v1/modules/",
	// The protocol routes of the OIDC issuers. The path is part of the issuer
	// URL that clouds hold, so it cannot move under the prefix.
	"/oidc/",
	// Service discovery, the metrics route and the probes.
	"GET /.well-known/terraform.json", "GET /metrics",
	"GET /health/live", "GET /health/ready", "GET /health/ready/{component}",
	// The console files.
	"/",
}

// rootPatternRaw returns the part of a ServeMux pattern after the method. A
// pattern with no method is all of it.
func rootPatternRaw(pattern string) string {
	if _, rest, found := strings.Cut(pattern, " "); found {
		return rest
	}

	return pattern
}

// rootPatternPath returns the path of a ServeMux pattern. A host part is not
// part of the path: it starts at the first slash.
func rootPatternPath(pattern string) string {
	raw := rootPatternRaw(pattern)
	if i := strings.Index(raw, "/"); i > 0 {
		return raw[i:]
	}

	return raw
}

// checkRootPatterns returns one error text for each pattern of the root mux
// that is not on the list rootMounts, and for each path that the server adds
// from an adapter and that no adapter pattern serves. A pattern under the
// prefix other than the catch-all is never allowed, whatever the list says.
func checkRootPatterns(direct, mounted, adapterPatterns []string) []string {
	var problems []string

	for _, pattern := range direct {
		path := rootPatternPath(pattern)

		switch {
		case !strings.HasPrefix(rootPatternRaw(pattern), "/"):
			problems = append(problems, pattern+" is on the root mux with a host part")
		case strings.HasPrefix(path, apidesc.Prefix) && pattern != apidesc.Prefix:
			problems = append(problems, pattern+" is on the root mux under the prefix and is not the catch-all")
		case !slices.Contains(rootMounts, pattern):
			problems = append(problems, pattern+" is on the root mux and is not on the list rootMounts")
		default:
		}
	}

	served := map[string]bool{}

	for _, pattern := range adapterPatterns {
		if _, path, found := strings.Cut(pattern, " "); found {
			served[path] = true
		}
	}

	for _, path := range mounted {
		if !served[path] {
			problems = append(problems, path+" is mounted from an adapter that does not list it")
		}
	}

	slices.Sort(problems)

	return problems
}
