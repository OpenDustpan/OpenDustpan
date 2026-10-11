package server_test

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/api/server"
	"opendustpan.org/opendustpan/identity"
)

// packageSources holds every Go file in this package, so TestNoMuxBypass can
// prove that no production file reaches the mux without recording.
//
//go:embed *.go
var packageSources embed.FS

// publicPatterns lists the routes that need no sign-in. The installation API
// serves no public route: every route needs a signed-in principal.
func publicPatterns() []string {
	return nil
}

// stubAuthenticator denies every token. Tests that send no token never reach it.
type stubAuthenticator struct{}

func (stubAuthenticator) AuthenticateToken(_ context.Context, _ string) (identity.Principal, error) {
	return identity.Principal{}, identity.ErrUnauthenticated
}

// tokenAuthenticator signs in exactly the test token.
type tokenAuthenticator struct{}

func (tokenAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token != "test-token" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{Subject: "tester", DisplayName: "Tester", Admin: false}, nil
}

// stubAuthorizer denies every action.
type stubAuthorizer struct{}

func (stubAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

// selectiveAuthorizer denies exactly the actions in deny and allows the rest.
type selectiveAuthorizer struct {
	deny map[identity.Action]bool
}

func (s selectiveAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if s.deny[action] {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	return nil
}

func newTestHandler(t *testing.T) *apiserver.Handler {
	t.Helper()

	handler, err := apiserver.NewHandler(apiserver.Config{
		Authenticator: stubAuthenticator{},
		Authorizer:    stubAuthorizer{},
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// newEnforcingHandler builds a Handler whose authorization the caller
// controls, so the enforcement tests can tell authorization apart from
// backend failures.
func newEnforcingHandler(t *testing.T, authz identity.Authorizer) *apiserver.Handler {
	t.Helper()

	handler, err := apiserver.NewHandler(apiserver.Config{
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
// is served. The table is empty at first, so the test passes vacuously; a
// row without actions or a resolver still fails.
func TestRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	public := map[string]bool{}
	for _, pattern := range publicPatterns() {
		public[pattern] = true
	}

	byPattern := map[string]apiserver.Route{}
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
func checkRouteScope(t *testing.T, route apiserver.Route, public map[string]bool) {
	t.Helper()

	pattern := route.Method + " " + route.Path

	switch route.Scope {
	case apiserver.ScopePublic:
		checkPublicRoute(t, route, public)
	case apiserver.ScopeAuthorized:
		checkAuthorizedRoute(t, route, public)
	default:
		t.Fatalf("route %q declares no scope", pattern)
	}
}

// checkPublicRoute fails for a public route that is not on the public list
// or that declares actions or a resolver.
func checkPublicRoute(t *testing.T, route apiserver.Route, public map[string]bool) {
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
func checkAuthorizedRoute(t *testing.T, route apiserver.Route, public map[string]bool) {
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
func probeRoute(t *testing.T, handler *apiserver.Handler, route apiserver.Route) {
	t.Helper()

	req := routeRequest(t, route, "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("route %q is declared but not served", route.Method+" "+route.Path)
	}
}

// routeRequest builds a valid request for route. token is sent as the
// Authorization Bearer header when not empty.
func routeRequest(t *testing.T, route apiserver.Route, token string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), route.Method, route.Path, http.NoBody)

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	return req
}

// TestRoutesEnforceAuthorization proves the table is enforced: with an
// Authorizer that denies a declared action the route answers 403, and it
// answers 200 when allowed. Table-driven over all routes. The table is empty
// at first, so the test passes vacuously.
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
func checkRouteEnforcement(t *testing.T, route apiserver.Route) {
	t.Helper()

	if route.Scope == apiserver.ScopePublic {
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

// TestUnknownPathAnswersJSON404 proves a path the table does not hold answers
// 404 as a JSON error from this adapter, so callers can tell it reached the
// installation API.
func TestUnknownPathAnswersJSON404(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/no-such-route", http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path answers %d, want %d", rec.Code, http.StatusNotFound)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("media type = %q, want %q", got, "application/json")
	}

	if !strings.Contains(rec.Body.String(), `"code":404`) {
		t.Fatalf("unknown path body %s, want a JSON error with code 404", rec.Body.String())
	}
}

// TestNewHandlerRequiresDependencies proves NewHandler fails without each
// dependency.
func TestNewHandlerRequiresDependencies(t *testing.T) {
	t.Parallel()

	full := apiserver.Config{
		Authenticator: tokenAuthenticator{},
		Authorizer:    selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:        slog.New(slog.DiscardHandler),
	}

	cases := []struct {
		name string
		drop func(*apiserver.Config)
	}{
		{name: "authenticator", drop: func(c *apiserver.Config) { c.Authenticator = nil }},
		{name: "authorizer", drop: func(c *apiserver.Config) { c.Authorizer = nil }},
		{name: "logger", drop: func(c *apiserver.Config) { c.Logger = nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := full
			tc.drop(&cfg)

			if _, err := apiserver.NewHandler(cfg); err == nil {
				t.Fatalf("NewHandler without %s succeeded", tc.name)
			}
		})
	}
}
