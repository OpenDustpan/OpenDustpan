package dustpan_test

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// publicRoutes is the one server-wide list of the routes that need no
// sign-in. Every other route of every adapter declares its actions. A new
// public route needs a reason, so it needs this list to change.
//
// The key is the ServeMux pattern. The value is the reason.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var publicRoutes = map[string]string{
	"GET /api/capabilities":                                            "Pulumi CLI service discovery",
	"GET /.well-known/terraform.json":                                  "OpenTofu service discovery",
	"GET /api/v2/ping":                                                 "OpenTofu reachability probe",
	"GET /api/dustpan/v1/capabilities":                                 "capability document of the installation",
	"GET /api/dustpan/v1/openapi.json":                                 "API description",
	"GET /health/live":                                                 "liveness probe",
	"GET /health/ready":                                                "readiness probe",
	"GET /health/ready/{component}":                                    "readiness probe of one component",
	"GET /api/server/sign-in/{provider}":                               "start of an identity provider sign-in",
	"GET /api/server/sign-in/{provider}/callback":                      "end of an identity provider sign-in",
	"POST /api/dustpan/v1/sign-in/password":                            "the user name and the password in the body are the credential",
	"POST /api/dustpan/v1/sign-in/dev-code":                            "the one-time code of a dev installation is the credential",
	"POST /api/dustpan/v1/local-users/redeem":                          "the one-use code in the body is the credential",
	"POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange": "the workload token in the body is the credential",
	"GET /oidc/{nsid}/.well-known/openid-configuration":                "issuer discovery for a cloud trust policy",
	"GET /oidc/{nsid}/jwks":                                            "issuer keys for a cloud trust policy",
}

// routeFacts is what the check reads about the routes of one server.
type routeFacts struct {
	// patterns holds the pattern of each route that an adapter lists.
	patterns []string
	// operations holds the operations of the tool-neutral REST API.
	operations []apidesc.Operation
	// declarations holds the rows of the route tables.
	declarations []dustpan.RouteDeclaration
	// root holds the patterns registered directly on the root mux.
	root []string
}

// factsOf reads the route facts of a server.
func factsOf(srv *dustpan.Server) routeFacts {
	var f routeFacts

	for _, source := range srv.RouteSources() {
		f.patterns = append(f.patterns, source.Patterns...)
	}

	f.operations = srv.Operations()
	f.declarations = srv.RouteDeclarations()
	f.root = srv.RootPatterns().Direct

	return f
}

// checkRouteActions returns one text for each route that is neither on the
// public list nor declared with actions, and for each entry of the public
// list that is stale or that declares actions.
func checkRouteActions(public map[string]string, facts ...routeFacts) []string {
	var problems []string

	served := map[string]bool{}

	for _, f := range facts {
		problems = append(problems, checkFacts(public, f, served)...)
	}

	for pattern := range public {
		if !served[pattern] {
			problems = append(problems, fmt.Sprintf("public list: %q is served by no server", pattern))
		}
	}

	slices.Sort(problems)

	return slices.Compact(problems)
}

// checkFacts checks the routes of one server and records the public patterns
// that it serves.
func checkFacts(public map[string]string, f routeFacts, served map[string]bool) []string {
	var problems []string

	ops := map[string]apidesc.Operation{}
	for i := range f.operations {
		ops[f.operations[i].Method+" "+f.operations[i].Path] = f.operations[i]
	}

	rows := map[string]dustpan.RouteDeclaration{}
	for _, d := range f.declarations {
		rows[d.Pattern] = d
	}

	for _, pattern := range f.patterns {
		if _, isPublic := public[pattern]; isPublic {
			served[pattern] = true
		}

		problems = append(problems, checkPattern(pattern, public, ops, rows)...)
	}

	for _, pattern := range f.root {
		method, _, hasMethod := strings.Cut(pattern, " ")
		if !hasMethod || method == "" {
			continue // A path-only pattern is the prefix of an adapter; the adapter routes are checked above.
		}

		if _, isPublic := public[pattern]; isPublic {
			served[pattern] = true

			continue
		}

		problems = append(problems, fmt.Sprintf("root mux: %q is not on the public list", pattern))
	}

	return problems
}

// checkPattern checks one route of an adapter. A route of the REST API is
// read from its operation, which names the object of each action. Another
// route is read from its row in the route table.
func checkPattern(
	pattern string, public map[string]string,
	ops map[string]apidesc.Operation, rows map[string]dustpan.RouteDeclaration,
) []string {
	if op, ok := ops[pattern]; ok {
		return checkOperation(pattern, public, op)
	}

	row, ok := rows[pattern]
	if !ok {
		return []string{pattern + ": the route has no operation and no row in a route table"}
	}

	_, isPublic := public[pattern]

	switch {
	case isPublic && row.Actions > 0:
		return []string{fmt.Sprintf("%s: on the public list, and the row of %s declares actions", pattern, row.Adapter)}
	case !isPublic && row.Actions == 0:
		return []string{
			fmt.Sprintf("%s: %s declares no action and the route is not on the public list", pattern, row.Adapter),
		}
	}

	return nil
}

// checkOperation checks one operation of the REST API.
func checkOperation(pattern string, public map[string]string, op apidesc.Operation) []string {
	var problems []string

	_, isPublic := public[pattern]

	if isPublic != op.Public {
		problems = append(problems, fmt.Sprintf("%s: public list says %v, the operation %s says %v",
			pattern, isPublic, op.ID, op.Public))
	}

	if isPublic {
		if len(op.Actions) > 0 {
			problems = append(problems, fmt.Sprintf(
				"%s: on the public list, and the operation %s declares actions", pattern, op.ID,
			))
		}

		return problems
	}

	if len(op.Actions) == 0 {
		return append(problems, fmt.Sprintf(
			"%s: the operation %s declares no action and the route is not on the public list", pattern, op.ID,
		))
	}

	for _, action := range op.Actions {
		if op.ActionObjects[action] == "" {
			problems = append(problems, fmt.Sprintf(
				"%s: the operation %s names no object for the action %s", pattern, op.ID, action,
			))
		}
	}

	return problems
}

// TestEveryRouteDeclaresActionsOrIsPublic proves for every route of every
// adapter that it declares its authorization actions and the object of each
// action, or that it is on the one server-wide public list. The check runs on
// a server of the installation kind dev too, because that server adds a
// public route.
func TestEveryRouteDeclaresActionsOrIsPublic(t *testing.T) {
	t.Parallel()

	every, dev := factsOf(newEveryModuleServer(t)), factsOf(newDevModuleServer(t))

	if len(every.patterns) == 0 || len(every.operations) == 0 || len(every.declarations) == 0 {
		t.Fatal("the server lists no route, no operation or no route table row")
	}

	if problems := checkRouteActions(publicRoutes, every, dev); len(problems) > 0 {
		t.Fatalf("routes with no action that are not public:\n%s", strings.Join(problems, "\n"))
	}
}

// TestRouteActionsCheckFailsOnAMutation proves that the check fails for a
// route with no action that is not on the list, for a route that leaves the
// list while it needs no sign-in, and for a list entry that no server serves.
func TestRouteActionsCheckFailsOnAMutation(t *testing.T) {
	t.Parallel()

	base := factsOf(newEveryModuleServer(t))

	if problems := checkRouteActions(publicRoutes, base, factsOf(newDevModuleServer(t))); len(problems) != 0 {
		t.Fatalf("the unmodified server fails the check:\n%s", strings.Join(problems, "\n"))
	}

	tests := []struct {
		name   string
		public map[string]string
		facts  routeFacts
		want   string
	}{
		{
			name:   "a REST route with no action",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				f.patterns = append(f.patterns, "GET /api/dustpan/v1/zz-no-action")
				f.operations = append(f.operations, operationOf("zzNoAction", "/api/dustpan/v1/zz-no-action", nil))
			}),
			want: "zzNoAction declares no action",
		},
		{
			name:   "a tool route with a row that has no action",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				f.patterns = append(f.patterns, "GET /api/zz-no-action")
				f.declarations = append(f.declarations, dustpan.RouteDeclaration{
					Adapter: "pulumi", Pattern: "GET /api/zz-no-action", Actions: 0,
				})
			}),
			want: "GET /api/zz-no-action: pulumi declares no action",
		},
		{
			name:   "a route with no row",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				f.patterns = append(f.patterns, "GET /api/zz-no-row")
			}),
			want: "GET /api/zz-no-row: the route has no operation and no row",
		},
		{
			name:   "an action with no object",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				f.patterns = append(f.patterns, "GET /api/dustpan/v1/zz-no-object")
				f.operations = append(f.operations, operationOf(
					"zzNoObject", "/api/dustpan/v1/zz-no-object", []string{"unit:read"},
				))
			}),
			want: "names no object for the action unit:read",
		},
		{
			name:   "a root route that is not public",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				f.root = append(f.root, "GET /zz-root")
			}),
			want: `root mux: "GET /zz-root" is not on the public list`,
		},
		{
			name:   "a public route removed from the list",
			public: without(publicRoutes, "GET /api/capabilities"),
			facts:  base,
			want:   "GET /api/capabilities",
		},
		{
			name:   "a REST public route removed from the list",
			public: without(publicRoutes, "POST /api/dustpan/v1/sign-in/password"),
			facts:  base,
			want:   "public list says false, the operation signInWithPassword says true",
		},
		{
			name:   "a stale entry on the list",
			public: with(publicRoutes, "GET /zz-stale"),
			facts:  base,
			want:   `public list: "GET /zz-stale" is served by no server`,
		},
		{
			name:   "an authorized route put on the list",
			public: with(publicRoutes, "GET /api/user"),
			facts:  base,
			want:   "GET /api/user: on the public list, and the row of pulumi declares actions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			problems := strings.Join(checkRouteActions(tt.public, tt.facts), "\n")
			if !strings.Contains(problems, tt.want) {
				t.Fatalf("want a problem that holds %q, got:\n%s", tt.want, problems)
			}
		})
	}
}

// operationOf returns a GET operation that declares actions and names no
// object for any of them.
func operationOf(id, path string, actions []string) apidesc.Operation {
	var op apidesc.Operation

	op.ID, op.Method, op.Path, op.Actions = id, "GET", path, actions

	return op
}

// mutate returns a copy of f that change has edited.
func mutate(f routeFacts, change func(*routeFacts)) routeFacts {
	out := routeFacts{
		patterns:     slices.Clone(f.patterns),
		operations:   slices.Clone(f.operations),
		declarations: slices.Clone(f.declarations),
		root:         slices.Clone(f.root),
	}
	change(&out)

	return out
}

// with returns a copy of the list with one more entry.
func with(list map[string]string, pattern string) map[string]string {
	out := map[string]string{pattern: "test"}
	maps.Copy(out, list)

	return out
}

// without returns a copy of the list without one entry.
func without(list map[string]string, pattern string) map[string]string {
	out := map[string]string{}

	for k, v := range list {
		if k != pattern {
			out[k] = v
		}
	}

	return out
}
