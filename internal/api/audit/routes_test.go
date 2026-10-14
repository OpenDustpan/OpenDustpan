package audit_test

import (
	"context"
	"embed"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apitaudit "opendustpan.org/opendustpan/internal/api/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// packageSources holds every Go file in this package, so TestNoMuxBypass can
// prove that no production file reaches the mux without recording.
//
//go:embed *.go
var packageSources embed.FS

// publicPatterns lists the routes that need no sign-in. The audit API serves
// no public route: every route needs a signed-in principal.
func publicPatterns() []string {
	return nil
}

// TestAuditRoutes declares the contract of the audit API: exactly the entry
// list and the chain check, each needing namespace:read-audit on the
// namespace in the organization query value.
func TestAuditRoutes(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	want := map[string][]identity.Action{
		"GET /api/audit/entries": {identity.ActionNamespaceReadAudit},
		"GET /api/audit/check":   {identity.ActionNamespaceReadAudit},
	}

	wantServer := map[string][]identity.Action{
		"GET /api/audit/entries": {identity.ActionServerReadAudit},
		"GET /api/audit/check":   {identity.ActionServerReadAudit},
	}

	routes := handler.Routes()
	if len(routes) != len(want) {
		t.Fatalf("routes = %d, want %d", len(routes), len(want))
	}

	for _, route := range routes {
		pattern := route.Method + " " + route.Path

		actions, ok := want[pattern]
		if !ok {
			t.Fatalf("unexpected route %q", pattern)
		}

		checkRouteActions(t, route, actions, wantServer[pattern])
	}
}

// checkRouteActions fails when a route does not declare the actions of its
// object and the actions of the installation, or is not an authorized route
// with a resolver.
func checkRouteActions(t *testing.T, route apitaudit.Route, actions, serverActions []identity.Action) {
	t.Helper()

	pattern := route.Method + " " + route.Path

	if !sameActions(route.Actions, actions) {
		t.Fatalf("route %q actions = %v, want %v", pattern, route.Actions, actions)
	}

	if !sameActions(route.ServerActions, serverActions) {
		t.Fatalf("route %q server actions = %v, want %v", pattern, route.ServerActions, serverActions)
	}

	if route.Scope != apitaudit.ScopeAuthorized {
		t.Fatalf("route %q is not an authorized route", pattern)
	}

	if route.Resolve == nil {
		t.Fatalf("route %q declares actions but no resolver", pattern)
	}
}

// sameActions reports whether a and b hold the same actions in the same order.
func sameActions(a, b []identity.Action) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
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

	byPattern := map[string]apitaudit.Route{}
	for _, route := range handler.Routes() {
		pattern := route.Method + " " + route.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("duplicate route %q", pattern)
		}

		byPattern[pattern] = route

		checkRouteScope(t, route, public)
		checkServerActions(t, route)
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
func checkRouteScope(t *testing.T, route apitaudit.Route, public map[string]bool) {
	t.Helper()

	pattern := route.Method + " " + route.Path

	switch route.Scope {
	case apitaudit.ScopePublic:
		checkPublicRoute(t, route, public)
	case apitaudit.ScopeAuthorized:
		checkAuthorizedRoute(t, route, public)
	default:
		t.Fatalf("route %q declares no scope", pattern)
	}
}

// checkPublicRoute fails for a public route that is not on the public list
// or that declares actions or a resolver.
func checkPublicRoute(t *testing.T, route apitaudit.Route, public map[string]bool) {
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
func checkAuthorizedRoute(t *testing.T, route apitaudit.Route, public map[string]bool) {
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
func probeRoute(t *testing.T, handler *apitaudit.Handler, route apitaudit.Route) {
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
func routeRequest(t *testing.T, route apitaudit.Route, token string) *http.Request {
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
func checkRouteEnforcement(t *testing.T, route apitaudit.Route) {
	t.Helper()

	if route.Scope == apitaudit.ScopePublic {
		allowed, _ := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

		rec := httptest.NewRecorder()
		allowed.ServeHTTP(rec, routeRequest(t, route, ""))

		if rec.Code != http.StatusOK {
			t.Fatalf("public route answers %d, want %d", rec.Code, http.StatusOK)
		}

		return
	}

	allowed, _ := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	rec := httptest.NewRecorder()
	allowed.ServeHTTP(rec, routeRequest(t, route, "test-token"))

	if rec.Code != http.StatusOK {
		t.Fatalf("allowed request answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	deny := map[identity.Action]bool{}
	for _, action := range route.Actions {
		deny[action] = true
	}

	denied, _ := newEnforcingHandler(t, selectiveAuthorizer{deny: deny})

	rec = httptest.NewRecorder()
	denied.ServeHTTP(rec, routeRequest(t, route, "test-token"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("denied request answers %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// TestResolveAuditNamespace proves the route resolves the namespace in the
// organization value, and fails on a missing or invalid value.
func TestResolveAuditNamespace(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	var resolve apitaudit.ObjectResolver

	for _, route := range handler.Routes() {
		if route.Method+" "+route.Path == "GET /api/audit/entries" {
			resolve = route.Resolve
		}
	}

	if resolve == nil {
		t.Fatal("GET /api/audit/entries declares no resolver")
	}

	resolveOK(t, resolve, "/api/audit/entries?organization=acme", "acme")

	for _, target := range []string{"/api/audit/entries", "/api/audit/entries?organization="} {
		resolveFails(t, resolve, target)
	}

	resolveFails(t, resolve, "/api/audit/entries?organization=../x")
}

// resolveOK proves resolve maps target to the namespace ns.
func resolveOK(t *testing.T, resolve apitaudit.ObjectResolver, target, ns string) {
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
func resolveFails(t *testing.T, resolve apitaudit.ObjectResolver, target string) {
	t.Helper()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)

	if _, err := resolve(rec, req); err == nil {
		t.Fatalf("resolve %q succeeded, want an error", target)
	}
}

// TestRoutesAuthorizeTheNamespace proves each route authorizes
// namespace:read-audit on the namespace in the organization value.
func TestRoutesAuthorizeTheNamespace(t *testing.T) {
	t.Parallel()

	for _, route := range auditRoutes(t) {
		recorder := &recordingAuthorizer{calls: nil}
		handler, _ := newEnforcingHandler(t, recorder)

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, routeRequest(t, route, "test-token"))

		if rec.Code != http.StatusOK {
			t.Fatalf("route %q answers %d, want %d", route.Method+" "+route.Path, rec.Code, http.StatusOK)
		}

		want := fmt.Sprintf("%s on %s", identity.ActionNamespaceReadAudit, "acme")
		found := false

		for _, call := range recorder.calls {
			if fmt.Sprintf("%s on %s", call.action, call.object) == want {
				found = true
			}
		}

		if !found {
			t.Fatalf(
				"route %q authorized %v, want %q",
				route.Method+" "+route.Path,
				recorder.calls,
				want,
			)
		}
	}
}

// auditRoutes returns the route table for the authorization test.
func auditRoutes(t *testing.T) []apitaudit.Route {
	t.Helper()

	handler := newTestHandler(t)

	return handler.Routes()
}

// recordedCall is one Authorize call: the action on the object wire form.
type recordedCall struct {
	action identity.Action
	object string
}

// recordingAuthorizer allows every action and records every call, so tests
// prove which object a route authorizes.
type recordingAuthorizer struct {
	calls []recordedCall
}

func (r *recordingAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, obj identity.Object,
) error {
	r.calls = append(r.calls, recordedCall{action: action, object: obj.String()})

	return nil
}

// checkServerActions fails when a route can resolve to the installation object
// and declares no ServerActions. The check asks the resolver itself, with the
// organization @server, so a new route cannot skip it.
func checkServerActions(t *testing.T, route apitaudit.Route) {
	t.Helper()

	if route.Resolve == nil || len(route.ServerActions) > 0 {
		return
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), route.Method, route.Path+"?organization=@server", http.NoBody)

	obj, err := route.Resolve(rec, req)
	if err == nil && obj.IsServer() {
		t.Fatalf("route %q resolves @server but declares no server actions", route.Method+" "+route.Path)
	}
}
