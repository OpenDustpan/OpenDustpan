package dustpan_test

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
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
// The key is the ServeMux pattern. The value is the reason. A route on the
// list must have the public scope in its row, and a row with the public scope
// must be on the list.
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
	"POST /api/dustpan/v1/sign-in/password":                            "the user name and the password in the body are the credential",
	"POST /api/dustpan/v1/sign-in/providers/{provider}/password":       "the user name and the password in the body are the credential, checked at the directory of the provider",
	"POST /api/dustpan/v1/sign-in/dev-code":                            "the one-time code of a dev installation is the credential",
	"POST /api/dustpan/v1/sign-in/passkey/begin":                       "start of a passkey sign-in: the person has no session, and the options name no person",
	"POST /api/dustpan/v1/sign-in/passkey/finish":                      "the signed response of the passkey in the body is the credential",
	"POST /api/dustpan/v1/local-users/redeem":                          "the one-use code in the body is the credential",
	"POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange": "the workload token in the body is the credential",
	"GET /api/server/sign-in/{provider}/callback":                      "end of an identity provider sign-in",
	"GET /oidc/{nsid}/.well-known/openid-configuration":                "issuer discovery for a cloud trust policy",
	"GET /oidc/{nsid}/jwks":                                            "issuer keys for a cloud trust policy",
	"/":                                                                "the files of the web console, with WithConsoleDir",
	"/api/dustpan/v1/":                                                 "the 404 answer for a path under the prefix that no handler serves",
}

// rootPrefixes maps each path-only pattern of the root mux, other than the
// patterns on the public list, to the adapter that it reaches. A pattern that
// is on neither list fails the check, and so does a prefix under which the
// adapter lists no route.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var rootPrefixes = map[string]string{
	"/api/inventory/": "inventory",
	"/api/audit/":     "audit",
	"/runner/v1/":     "runners",
	"/api/runners/":   "runners",
	"/api/v2/":        "tfe",
	"/tofu/state/":    "tofuhttp",
	"/api/server/":    "server",
	"/api/":           "pulumi",
	"/oidc/":          "issuer",
	"/v1/providers/":  "tfe",
	"/v1/modules/":    "tfe",
}

// rootMethods lists the patterns with a method that code may register
// directly on the root mux without being on the public list, and the adapter
// that owns each. A process without the api component serves the metrics of
// the server adapter this way. Any other pattern with a method on the root mux
// must be on the public list.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var rootMethods = map[string]string{
	"GET /api/server/metrics": "server",
}

// routeFacts is what the check reads about the routes of one server.
type routeFacts struct {
	// sources holds the patterns that each adapter lists.
	sources []dustpan.RouteSource
	// operations holds the operations of the tool-neutral REST API.
	operations []apidesc.Operation
	// declarations holds the rows of the route tables.
	declarations []dustpan.RouteDeclaration
	// root holds the patterns registered directly on the root mux.
	root []string
	// mounted holds the paths that the root mux took from the patterns of an
	// adapter.
	mounted []string
}

// factsOf reads the route facts of a server.
func factsOf(t *testing.T, srv *dustpan.Server) routeFacts {
	t.Helper()

	declarations, err := srv.RouteDeclarations()
	if err != nil {
		t.Fatalf("read the route tables: %v", err)
	}

	return routeFacts{
		sources:      srv.RouteSources(),
		operations:   srv.Operations(),
		declarations: declarations,
		root:         srv.RootPatterns().Direct,
		mounted:      srv.RootPatterns().Mounted,
	}
}

// checkRouteActions returns one text for each route that breaks the rule, and
// for each entry of the public list that no server serves.
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

	for _, source := range f.sources {
		for _, pattern := range source.Patterns {
			if _, isPublic := public[pattern]; isPublic {
				served[pattern] = true
			}

			problems = append(problems, checkPattern(pattern, public, ops, rows)...)
		}
	}

	problems = append(problems, checkRoot(f, public, rows, served)...)

	return append(problems, checkMounted(f)...)
}

// checkPattern checks one route of an adapter against the public list, the
// scope of its row, and its actions. A route of the REST API reads its
// actions and their objects from its operation. Another route reads its
// actions from the enforced fields of its row.
func checkPattern(
	pattern string, public map[string]string,
	ops map[string]apidesc.Operation, rows map[string]dustpan.RouteDeclaration,
) []string {
	row, ok := rows[pattern]
	if !ok {
		return []string{pattern + ": the route has no row in a route table"}
	}

	_, isPublic := public[pattern]

	problems := checkScope(pattern, isPublic, row)

	if op, isREST := ops[pattern]; isREST {
		return append(problems, checkOperation(pattern, public, op)...)
	}

	switch {
	case isPublic && len(row.EnforcedFields) > 0:
		problems = append(problems, fmt.Sprintf(
			"%s: on the public list, and %s declares actions in %v", pattern, row.Adapter, row.EnforcedFields,
		))
	case !isPublic && len(row.EnforcedFields) == 0:
		problems = append(problems, fmt.Sprintf(
			"%s: %s declares no enforced action and the route is not on the public list", pattern, row.Adapter,
		))
	default:
	}

	return problems
}

// checkScope checks that a row has the public scope exactly when its route is
// on the public list.
func checkScope(pattern string, isPublic bool, row dustpan.RouteDeclaration) []string {
	switch {
	case isPublic && row.Scope != dustpan.RouteScopePublic:
		return []string{fmt.Sprintf(
			"%s: on the public list, and the row of %s has the scope %s", pattern, row.Adapter, row.Scope,
		)}
	case !isPublic && row.Scope == dustpan.RouteScopePublic:
		return []string{fmt.Sprintf(
			"%s: the row of %s has the public scope and the route is not on the public list", pattern, row.Adapter,
		)}
	default:
		return nil
	}
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

// checkRoot checks each pattern registered directly on the root mux. A
// pattern with a method is on the public list or is a row of an adapter. A
// path-only pattern is on the public list or is a prefix in rootPrefixes, and
// its adapter lists at least one route under it.
func checkRoot(
	f routeFacts, public map[string]string, rows map[string]dustpan.RouteDeclaration, served map[string]bool,
) []string {
	var problems []string

	for _, pattern := range f.root {
		if _, isPublic := public[pattern]; isPublic {
			served[pattern] = true

			continue
		}

		method, _, hasMethod := strings.Cut(pattern, " ")
		if hasMethod && method != "" {
			problems = append(problems, checkRootMethod(pattern, rows)...)

			continue
		}

		adapter, known := rootPrefixes[pattern]
		if !known {
			problems = append(problems, fmt.Sprintf(
				"root mux: %q is neither on the public list nor a prefix of a known adapter", pattern,
			))

			continue
		}

		if !listsUnder(f.sources, adapter, pattern) {
			problems = append(problems, fmt.Sprintf(
				"root mux: %q reaches the adapter %s, which lists no route under it", pattern, adapter,
			))
		}
	}

	return problems
}

// checkRootMethod checks a pattern with a method that code registered on the
// root mux and that is not on the public list. The pattern must be on
// rootMethods, and the row with the same pattern must belong to the adapter
// that owns it. A row of another adapter with the same pattern proves nothing:
// the root mux serves the pattern before the prefix of that adapter, with the
// handler that code registered.
func checkRootMethod(pattern string, rows map[string]dustpan.RouteDeclaration) []string {
	owner, allowed := rootMethods[pattern]
	if !allowed {
		return []string{fmt.Sprintf(
			"root mux: %q is neither on the public list nor on the list of root methods", pattern,
		)}
	}

	if row, isRow := rows[pattern]; !isRow || row.Adapter != owner {
		return []string{fmt.Sprintf("root mux: %q is not a row of its owner %s", pattern, owner)}
	}

	return nil
}

// checkMounted checks that each path that the root mux took from an adapter is
// the path of a route that an adapter lists.
func checkMounted(f routeFacts) []string {
	listed := map[string]bool{}

	for _, source := range f.sources {
		for _, pattern := range source.Patterns {
			if _, path, found := strings.Cut(pattern, " "); found {
				listed[path] = true
			}
		}
	}

	var problems []string

	for _, path := range f.mounted {
		if !listed[path] {
			problems = append(problems, fmt.Sprintf("root mux: the mounted path %q is listed by no adapter", path))
		}
	}

	return problems
}

// wildcardPattern matches a wildcard of a path.
var wildcardPattern = regexp.MustCompile(`\{[^}]*\}`)

// probeUnauthenticated sends one request with no credential to each route of
// the adapters that is not on the public list, through the real handler, and
// returns one text for each route that answers with a success status. The
// check does not rely on the rows or the root mux: it asks the served handler.
func probeUnauthenticated(handler http.Handler, f routeFacts, public map[string]string) []string {
	var problems []string

	for _, source := range f.sources {
		for _, pattern := range source.Patterns {
			if _, isPublic := public[pattern]; isPublic {
				continue
			}

			method, path, found := strings.Cut(pattern, " ")
			if !found {
				continue
			}

			req := httptest.NewRequestWithContext(
				context.Background(), method, wildcardPattern.ReplaceAllString(path, "zz"), http.NoBody,
			)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code >= 200 && rec.Code < 300 {
				problems = append(problems, fmt.Sprintf(
					"%s: a request with no credential got the status %d", pattern, rec.Code,
				))
			}
		}
	}

	return problems
}

// listsUnder reports whether the adapter lists a route whose path starts with
// the prefix.
func listsUnder(sources []dustpan.RouteSource, adapter, prefix string) bool {
	for _, source := range sources {
		if source.Name != adapter {
			continue
		}

		for _, pattern := range source.Patterns {
			_, path, found := strings.Cut(pattern, " ")
			if found && strings.HasPrefix(path, prefix) {
				return true
			}
		}
	}

	return false
}

// consoleDir returns a directory that holds a web console.
func consoleDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o600); err != nil {
		t.Fatalf("write the console index: %v", err)
	}

	return dir
}

// allShapes reads the route facts of the server shapes that differ in their
// routes: every module on, the installation kind dev, the web console on, and
// a process without the api component.
func allShapes(t *testing.T) []routeFacts {
	t.Helper()

	console := consoleDir(t)

	return []routeFacts{
		factsOf(t, newEveryModuleServer(t)),
		factsOf(t, newDevModuleServer(t)),
		factsOf(t, newModuleServer(t, dustpan.WithConsoleDir(console))),
		factsOf(t, newModuleServer(t, dustpan.WithComponents(mustParse(t, "worker")))),
	}
}

// TestEveryRouteDeclaresActionsOrIsPublic proves for every route of every
// adapter and of the root mux that it declares its authorization actions and
// the object of each action, or that it is on the one server-wide public
// list. It proves too that a route has the public scope exactly when it is on
// the list, so a route that the wrapper serves with no sign-in cannot hide
// behind its actions.
func TestEveryRouteDeclaresActionsOrIsPublic(t *testing.T) {
	t.Parallel()

	shapes := allShapes(t)

	if len(shapes[0].sources) == 0 || len(shapes[0].operations) == 0 || len(shapes[0].declarations) == 0 {
		t.Fatal("the server lists no route, no operation or no route table row")
	}

	if problems := checkRouteActions(publicRoutes, shapes...); len(problems) > 0 {
		t.Fatalf("routes that break the rule:\n%s", strings.Join(problems, "\n"))
	}
}

// TestNoRouteAnswersWithoutACredential proves with the served handler that a
// route which is not on the public list refuses a request with no
// credential. It sends the request through the whole handler chain, so it sees
// a handler that code registered on the root mux in place of an adapter, and a
// wrapper that answers for a route by itself.
func TestNoRouteAnswersWithoutACredential(t *testing.T) {
	t.Parallel()

	for name, srv := range map[string]*dustpan.Server{
		"every module": newEveryModuleServer(t),
		"console":      newModuleServer(t, dustpan.WithConsoleDir(consoleDir(t))),
	} {
		if problems := probeUnauthenticated(srv.UngatedHandler(), factsOf(t, srv), publicRoutes); len(problems) > 0 {
			t.Fatalf("%s: routes that answer with no credential:\n%s", name, strings.Join(problems, "\n"))
		}
	}
}

// TestRouteActionsCheckFailsOnAMutation proves that the check fails for each
// way that a route could be served with no sign-in, or with no action, while
// it is on no list.
func TestRouteActionsCheckFailsOnAMutation(t *testing.T) {
	t.Parallel()

	shapes := allShapes(t)
	base := shapes[0]

	if problems := checkRouteActions(publicRoutes, shapes...); len(problems) != 0 {
		t.Fatalf("the unmodified servers fail the check:\n%s", strings.Join(problems, "\n"))
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
				addRoute(f, "pulumi", "GET /api/dustpan/v1/zz-no-action", dustpan.RouteScopeSignedIn)
				f.operations = append(f.operations, operationOf("zzNoAction", "/api/dustpan/v1/zz-no-action", nil))
			}),
			want: "zzNoAction declares no action",
		},
		{
			name:   "a self-service write route whose operation drops its action",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				dropOperationActions(f, http.MethodPut, selfServiceDraftPath, "writeBlueprintDraft")
			}),
			want: "writeBlueprintDraft declares no action",
		},
		{
			name:   "a tool route with a row that has no enforced action",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				addRoute(f, "pulumi", "GET /api/zz-no-action", dustpan.RouteScopeSignedIn)
			}),
			want: "GET /api/zz-no-action: pulumi declares no enforced action",
		},
		{
			name:   "a route with no row",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				f.sources = append(f.sources, dustpan.RouteSource{
					Name: "pulumi", Patterns: []string{"GET /api/zz-no-row"},
				})
			}),
			want: "GET /api/zz-no-row: the route has no row",
		},
		{
			name:   "an action with no object",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				addRoute(f, "pulumi", "GET /api/dustpan/v1/zz-no-object", dustpan.RouteScopeSignedIn)
				f.operations = append(f.operations, operationOf(
					"zzNoObject", "/api/dustpan/v1/zz-no-object", []string{"unit:read"},
				))
			}),
			want: "names no object for the action unit:read",
		},
		{
			name:   "a tool route with the public scope that keeps its actions",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				setScope(f, "GET /api/user", dustpan.RouteScopePublic)
			}),
			want: "GET /api/user: the row of pulumi has the public scope and the route is not on the public list",
		},
		{
			name:   "a REST route with the public scope that keeps its actions",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				setScope(f, restPattern(base), dustpan.RouteScopePublic)
			}),
			want: "has the public scope and the route is not on the public list",
		},
		{
			name:   "a listed route whose row is not public",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				setScope(f, "GET /api/capabilities", dustpan.RouteScopeSignedIn)
			}),
			want: "GET /api/capabilities: on the public list, and the row of pulumi has the scope signed-in",
		},
		{
			name:   "a root subtree that no adapter reaches",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				f.root = append(f.root, "/zz-open/")
			}),
			want: `root mux: "/zz-open/" is neither on the public list nor a prefix of a known adapter`,
		},
		{
			name:   "a root subtree whose adapter lists no route under it",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				f.root = append(f.root, "/api/")
				f.sources = slices.DeleteFunc(
					slices.Clone(f.sources),
					func(s dustpan.RouteSource) bool { return s.Name == "pulumi" },
				)
			}),
			want: `root mux: "/api/" reaches the adapter pulumi, which lists no route under it`,
		},
		{
			name:   "a root route with a method that is no row",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				f.root = append(f.root, "GET /zz-root")
			}),
			want: `root mux: "GET /zz-root" is neither on the public list nor on the list of root methods`,
		},
		{
			name:   "a root handler in place of a row of an adapter",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				f.root = append(f.root, "GET /api/user")
			}),
			want: `root mux: "GET /api/user" is neither on the public list nor on the list of root methods`,
		},
		{
			name:   "a root method that another adapter owns",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				f.root = append(f.root, "GET /api/server/metrics")
				f.declarations = slices.Clone(f.declarations)
				for i := range f.declarations {
					if f.declarations[i].Pattern == "GET /api/server/metrics" {
						f.declarations[i].Adapter = "pulumi"
					}
				}
			}),
			want: `root mux: "GET /api/server/metrics" is not a row of its owner server`,
		},
		{
			name:   "a mounted path that no adapter lists",
			public: publicRoutes,
			facts: mutate(base, func(f *routeFacts) {
				f.mounted = append(f.mounted, "/api/dustpan/v1/zz-mounted")
			}),
			want: `the mounted path "/api/dustpan/v1/zz-mounted" is listed by no adapter`,
		},
		{
			name:   "the console files removed from the list",
			public: without(publicRoutes, "/"),
			facts:  shapes[2],
			want:   `root mux: "/" is neither on the public list nor a prefix of a known adapter`,
		},
		{
			name:   "a public route removed from the list",
			public: without(publicRoutes, "GET /api/capabilities"),
			facts:  base,
			want:   "GET /api/capabilities: the row of pulumi has the public scope",
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

// addRoute adds a route to the adapter with a row that declares no action.
func addRoute(f *routeFacts, adapter, pattern string, scope dustpan.RouteScope) {
	f.sources = append(f.sources, dustpan.RouteSource{Name: adapter, Patterns: []string{pattern}})
	f.declarations = append(f.declarations, dustpan.RouteDeclaration{
		Adapter: adapter, Pattern: pattern, Scope: scope, EnforcedFields: nil,
	})
}

// restPattern returns the pattern of an operation of the REST API that is not
// public.
func restPattern(f routeFacts) string {
	for i := range f.operations {
		if !f.operations[i].Public {
			return f.operations[i].Method + " " + f.operations[i].Path
		}
	}

	panic("no operation that needs a sign-in")
}

// setScope sets the scope of the row of one pattern.
func setScope(f *routeFacts, pattern string, scope dustpan.RouteScope) {
	for i := range f.declarations {
		if f.declarations[i].Pattern == pattern {
			f.declarations[i].Scope = scope

			return
		}
	}

	panic("no row for " + pattern)
}

// operationOf returns a GET operation that declares actions and names no
// object for any of them.
func operationOf(id, path string, actions []string) apidesc.Operation {
	var op apidesc.Operation

	op.ID, op.Method, op.Path, op.Actions = id, "GET", path, actions

	return op
}

// mutate returns a copy of f that change has edited.
// selfServiceDraftPath is the path of the draft route of the blueprint API.
const selfServiceDraftPath = "/api/dustpan/v1/self-service/blueprints/{namespace}/{blueprint}/draft"

// dropOperationActions replaces the operation of method and path by one that
// declares no action, as a description that lost its action would.
func dropOperationActions(f *routeFacts, method, path, id string) {
	for i := range f.operations {
		if f.operations[i].Method == method && f.operations[i].Path == path {
			f.operations[i] = operationOf(id, path, nil)
		}
	}
}

func mutate(f routeFacts, change func(*routeFacts)) routeFacts {
	out := routeFacts{
		sources:      slices.Clone(f.sources),
		operations:   slices.Clone(f.operations),
		declarations: slices.Clone(f.declarations),
		root:         slices.Clone(f.root),
		mounted:      slices.Clone(f.mounted),
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
	out := maps.Clone(list)
	delete(out, pattern)

	return out
}
