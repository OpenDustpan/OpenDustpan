package inventory_test

import (
	"context"
	"embed"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiinventory "opendustpan.org/opendustpan/api/inventory"
	"opendustpan.org/opendustpan/identity"
	coreinventory "opendustpan.org/opendustpan/inventory"
	"opendustpan.org/opendustpan/namespace"
)

// packageSources holds every Go file in this package, so TestNoMuxBypass can
// prove that no production file reaches the mux without recording.
//
//go:embed *.go
var packageSources embed.FS

// publicPatterns lists the routes that need no sign-in. The inventory API
// serves no public route: every route needs a signed-in principal.
func publicPatterns() []string {
	return nil
}

// homedNamespaceStore answers every namespace homed in the test region, so
// the enforcement tests reach the reader instead of failing on the region.
type homedNamespaceStore struct{}

func (homedNamespaceStore) CreateNamespace(_ context.Context, _ namespace.Namespace) error {
	return nil
}

func (homedNamespaceStore) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	var out namespace.Namespace

	out.Name = name
	out.HomeRegion = "test-region"
	out.FailoverVersion = 1

	return out, nil
}

func (homedNamespaceStore) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	ns := namespace.Namespace{
		CreatedAt:       changeTime(),
		Name:            "acme",
		HomeRegion:      "test-region",
		FailoverVersion: 1,
	}

	return []namespace.Namespace{ns}, nil
}

func newTestHandler(t *testing.T) *apiinventory.Handler {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{
			Store:  &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}},
			Now:    nil,
			Region: "test-region",
		},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := apiinventory.NewHandler(apiinventory.Config{
		Namespaces:    namespaces,
		Units:         &memReader{entries: map[namespace.Name][]coreinventory.Entry{}, err: nil},
		Authenticator: stubAuthenticator{},
		Authorizer:    stubAuthorizer{},
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// newEnforcingHandler builds a Handler whose namespace reads succeed, so the
// enforcement tests can tell authorization apart from backend failures.
func newEnforcingHandler(t *testing.T, authz identity.Authorizer) *apiinventory.Handler {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: homedNamespaceStore{}, Now: nil, Region: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := apiinventory.NewHandler(apiinventory.Config{
		Namespaces:    namespaces,
		Units:         &memReader{entries: map[namespace.Name][]coreinventory.Entry{}, err: nil},
		Authenticator: tokenAuthenticator{},
		Authorizer:    authz,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// TestRoutesDeclareActions walks every registered route and fails for a
// route with no declared action. It checks both directions: every served
// pattern has a table row that passes the scope check, and every table row
// is served.
func TestRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	public := map[string]bool{}
	for _, pattern := range publicPatterns() {
		public[pattern] = true
	}

	byPattern := map[string]apiinventory.Route{}
	for _, route := range handler.Routes() {
		pattern := route.Method + " " + route.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("duplicate route %q", pattern)
		}

		byPattern[pattern] = route

		checkRouteScope(t, route, public)
	}

	for _, pattern := range handler.RegisteredPatterns() {
		if _, ok := byPattern[pattern]; !ok {
			t.Fatalf("served route %q has no declared action", pattern)
		}
	}

	for _, route := range byPattern {
		probeRoute(t, handler, route)
	}
}

// TestNoMuxBypass proves every served route goes through the recorder: it
// fails if any production file in this package builds a ServeMux or touches
// the inner mux of patternMux outside routes.go, where Handle and HandleFunc
// both record. Test files are skipped; they never serve production routes.
func TestNoMuxBypass(t *testing.T) {
	t.Parallel()

	entries, err := packageSources.ReadDir(".")
	if err != nil {
		t.Fatalf("read package sources: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == "routes.go" || strings.HasSuffix(name, "_test.go") {
			continue
		}

		content, err := packageSources.ReadFile(name)
		if err != nil {
			t.Fatalf("read file %s: %v", name, err)
		}

		for _, needle := range []string{"NewServeMux", "servemux"} {
			if strings.Contains(string(content), needle) {
				t.Fatalf("file %s reaches the mux without recording (contains %q)", name, needle)
			}
		}
	}
}

// checkRouteScope fails for a route that is neither public nor authorized
// with actions and a resolver.
func checkRouteScope(t *testing.T, route apiinventory.Route, public map[string]bool) {
	t.Helper()

	pattern := route.Method + " " + route.Path

	switch route.Scope {
	case apiinventory.ScopePublic:
		checkPublicRoute(t, route, public)
	case apiinventory.ScopeAuthorized:
		checkAuthorizedRoute(t, route, public)
	default:
		t.Fatalf("route %q declares no scope", pattern)
	}
}

// checkPublicRoute fails for a public route that is not on the public list
// or that declares actions or a resolver.
func checkPublicRoute(t *testing.T, route apiinventory.Route, public map[string]bool) {
	t.Helper()

	pattern := route.Method + " " + route.Path

	if !public[pattern] {
		t.Fatalf("route %q is public but not on the public list", pattern)
	}

	if len(route.Actions) != 0 {
		t.Fatalf("public route %q declares actions", pattern)
	}

	if route.Resolve != nil {
		t.Fatalf("public route %q declares a resolver", pattern)
	}
}

// checkAuthorizedRoute fails for an authorized route that is on the public
// list or that declares no action or no resolver.
func checkAuthorizedRoute(t *testing.T, route apiinventory.Route, public map[string]bool) {
	t.Helper()

	pattern := route.Method + " " + route.Path

	if public[pattern] {
		t.Fatalf("route %q is on the public list but declares actions", pattern)
	}

	if len(route.Actions) == 0 {
		t.Fatalf("route %q declares no action", pattern)
	}

	if route.Resolve == nil {
		t.Fatalf("route %q declares actions but no resolver", pattern)
	}
}

// probeRoute checks that a declared route is registered: a request without a
// token must fail closed, never with 404.
func probeRoute(t *testing.T, handler *apiinventory.Handler, route apiinventory.Route) {
	t.Helper()

	req := routeRequest(t, route, "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("route %q is declared but not served", route.Method+" "+route.Path)
	}
}

// routeRequest builds a valid request for route: the organization query
// value where the route reads it. token is sent as the Authorization header
// when not empty.
func routeRequest(t *testing.T, route apiinventory.Route, token string) *http.Request {
	t.Helper()

	target := route.Path + "?organization=acme"

	req := httptest.NewRequestWithContext(t.Context(), route.Method, target, http.NoBody)

	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	return req
}

// TestRoutesEnforceAuthorization proves the table is enforced: with an
// Authorizer that denies a declared action the route answers 403, and it
// answers 200 when allowed. Table-driven over all routes.
func TestRoutesEnforceAuthorization(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	for _, route := range handler.Routes() {
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			t.Parallel()
			checkRouteEnforcement(t, route)
		})
	}
}

// checkRouteEnforcement proves one route enforces its row: it answers 200
// when allowed, and an authorized route answers 403 when its actions are
// denied.
func checkRouteEnforcement(t *testing.T, route apiinventory.Route) {
	t.Helper()

	if route.Scope == apiinventory.ScopePublic {
		allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

		rec := httptest.NewRecorder()
		allowed.ServeHTTP(rec, routeRequest(t, route, ""))

		if rec.Code != http.StatusOK {
			t.Fatalf("public route answers %d, want %d", rec.Code, http.StatusOK)
		}

		return
	}

	allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	rec := httptest.NewRecorder()
	allowed.ServeHTTP(rec, routeRequest(t, route, "test-token"))

	if rec.Code != http.StatusOK {
		t.Fatalf("allowed request answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	deny := map[identity.Action]bool{}
	for _, action := range route.Actions {
		deny[action] = true
	}

	denied := newEnforcingHandler(t, selectiveAuthorizer{deny: deny})

	rec = httptest.NewRecorder()
	denied.ServeHTTP(rec, routeRequest(t, route, "test-token"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("denied request answers %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// TestResolveListUnits proves the route resolves the namespace in the
// organization value, and fails on a missing or invalid value.
func TestResolveListUnits(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	var resolve apiinventory.ObjectResolver

	for _, route := range handler.Routes() {
		if route.Method+" "+route.Path == "GET /api/inventory/units" {
			resolve = route.Resolve
		}
	}

	if resolve == nil {
		t.Fatal("GET /api/inventory/units declares no resolver")
	}

	resolveOK(t, resolve, "/api/inventory/units?organization=acme", "acme")

	for _, target := range []string{"/api/inventory/units", "/api/inventory/units?organization="} {
		resolveFails(t, resolve, target)
	}

	resolveFails(t, resolve, "/api/inventory/units?organization=../x")
}

// resolveOK proves resolve maps target to the namespace ns.
func resolveOK(t *testing.T, resolve apiinventory.ObjectResolver, target, ns string) {
	t.Helper()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	obj, err := resolve(rec, req)
	if err != nil {
		t.Fatalf("resolve %q: %v", target, err)
	}

	if obj.String() != ns || obj.IsUnit() {
		t.Fatalf("object = %q (unit %v), want namespace %q", obj.String(), obj.IsUnit(), ns)
	}
}

// resolveFails proves resolve rejects target with an error.
func resolveFails(t *testing.T, resolve apiinventory.ObjectResolver, target string) {
	t.Helper()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)

	if _, err := resolve(rec, req); err == nil {
		t.Fatalf("resolve %q succeeded, want an error", target)
	}
}
