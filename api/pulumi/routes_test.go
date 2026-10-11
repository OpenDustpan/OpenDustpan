package pulumi_test

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/api/pulumi"
	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/stack"
)

// packageSources holds every Go file in this package, so TestNoMuxBypass can
// prove that no production file reaches the mux without recording.
//
//go:embed *.go
var packageSources embed.FS

// publicPatterns lists the routes that need no sign-in: service discovery,
// capabilities and health. Every other route must declare its actions in the
// route table. GET /healthz is served by the root handler, not by the Pulumi
// CLI handler, and is listed here so the public surface stays in one place.
func publicPatterns() []string {
	return []string{"GET /api/capabilities", "GET /healthz"}
}

// stubNamespaceStore answers no namespace. It exists only to build a Handler.
type stubNamespaceStore struct{}

func (stubNamespaceStore) CreateNamespace(_ context.Context, _ namespace.Namespace) error {
	return nil
}

func (stubNamespaceStore) GetNamespace(_ context.Context, _ namespace.Name) (namespace.Namespace, error) {
	var zero namespace.Namespace

	return zero, namespace.ErrNotFound
}

// homedNamespaceStore answers every namespace homed in the test region, so
// stack writes pass the home-region check.
type homedNamespaceStore struct{}

func (homedNamespaceStore) CreateNamespace(_ context.Context, _ namespace.Namespace) error {
	return nil
}

func (homedNamespaceStore) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	var out namespace.Namespace

	out.Name = name
	out.HomeRegion = "test-region"

	return out, nil
}

// stubStackStore answers no stack. It exists only to build a Handler.
type stubStackStore struct{}

func (stubStackStore) CreateStack(_ context.Context, _ stack.Stack, _ namespace.Fence) error {
	return nil
}

func (stubStackStore) GetStack(_ context.Context, _ stack.Ref) (stack.Stack, error) {
	var zero stack.Stack

	return zero, stack.ErrNotFound
}

func (stubStackStore) ListStacks(_ context.Context, _ stack.Filter) ([]stack.Stack, error) {
	return nil, nil
}

// recordingStackStore answers every Get with a stack for the requested
// reference and records every Create. It lets the enforcement tests prove
// that allowed requests reach the service.
type recordingStackStore struct {
	created []stack.Stack
}

func (s *recordingStackStore) CreateStack(_ context.Context, st stack.Stack, _ namespace.Fence) error {
	s.created = append(s.created, st)

	return nil
}

func (s *recordingStackStore) GetStack(_ context.Context, ref stack.Ref) (stack.Stack, error) {
	var out stack.Stack

	out.Ref = ref
	out.ID = "stk-test"
	out.Tags = map[string]string{}

	return out, nil
}

func (s *recordingStackStore) ListStacks(_ context.Context, _ stack.Filter) ([]stack.Stack, error) {
	return nil, nil
}

// stubAuthenticator denies every token. The declaration and resolver tests
// send no token, so it never runs.
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

// stubAuthorizer denies every action. The declaration and resolver tests
// never reach it.
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

func newTestHandler(t *testing.T) *pulumi.Handler {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: stubNamespaceStore{}, Now: nil, Region: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stacks, err := stack.NewService(stack.Config{Store: stubStackStore{}, Namespaces: namespaces, Now: nil})
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	handler, err := pulumi.NewHandler(pulumi.Config{
		Stacks:        stacks,
		Updates:       newUnusedUpdates(t, namespaces),
		Authenticator: stubAuthenticator{},
		Authorizer:    stubAuthorizer{},
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// newEnforcingHandler builds a Handler whose stack writes succeed, so the
// enforcement tests can tell authorization apart from backend failures. It
// returns the handler and the store that records created stacks.
func newEnforcingHandler(t *testing.T, authz identity.Authorizer) (*pulumi.Handler, *recordingStackStore) {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: homedNamespaceStore{}, Now: nil, Region: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stores := &recordingStackStore{created: nil}

	stacks, err := stack.NewService(stack.Config{Store: stores, Namespaces: namespaces, Now: nil})
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	handler, err := pulumi.NewHandler(pulumi.Config{
		Stacks:        stacks,
		Updates:       newUnusedUpdates(t, namespaces),
		Authenticator: tokenAuthenticator{},
		Authorizer:    authz,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler, stores
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

	byPattern := map[string]pulumi.Route{}
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

	for pattern := range public {
		if pattern == "GET /healthz" {
			continue
		}

		if _, ok := byPattern[pattern]; !ok {
			t.Fatalf("public route %q is not declared", pattern)
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
func checkRouteScope(t *testing.T, route pulumi.Route, public map[string]bool) {
	t.Helper()

	pattern := route.Method + " " + route.Path

	switch route.Scope {
	case pulumi.ScopePublic:
		checkPublicRoute(t, route, public)
	case pulumi.ScopeAuthorized:
		checkAuthorizedRoute(t, route, public)
	default:
		t.Fatalf("route %q declares no scope", pattern)
	}
}

// checkPublicRoute fails for a public route that is not on the public list
// or that declares actions or a resolver.
func checkPublicRoute(t *testing.T, route pulumi.Route, public map[string]bool) {
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
func checkAuthorizedRoute(t *testing.T, route pulumi.Route, public map[string]bool) {
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
func probeRoute(t *testing.T, handler *pulumi.Handler, route pulumi.Route) {
	t.Helper()

	req := routeRequest(t, route, "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("route %q is declared but not served", route.Method+" "+route.Path)
	}
}

// routeRequest builds a valid request for route: path placeholders filled,
// the organization query value where the route reads it, and a JSON body
// where the route reads one. token is sent as the Authorization header when
// not empty.
func routeRequest(t *testing.T, route pulumi.Route, token string) *http.Request {
	t.Helper()

	path := strings.NewReplacer(
		"{namespace}", "acme",
		"{project}", "web",
		"{stack}", "prod",
	).Replace(route.Path)

	target := path

	var body *strings.Reader

	switch {
	case route.Method == http.MethodGet && route.Path == "/api/user/stacks":
		target += "?organization=acme"
	case route.Method == http.MethodPost:
		body = strings.NewReader(`{"stackName":"prod"}`)
	default:
		body = strings.NewReader("")
	}

	var req *http.Request
	if body == nil {
		req = httptest.NewRequestWithContext(t.Context(), route.Method, target, http.NoBody)
	} else {
		req = httptest.NewRequestWithContext(t.Context(), route.Method, target, body)
	}

	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	return req
}

// TestRoutesEnforceAuthorization proves the table is enforced: with an
// Authorizer that denies a declared action each authorized route answers
// 403, and each route answers 200 when allowed. Table-driven over all
// routes.
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
func checkRouteEnforcement(t *testing.T, route pulumi.Route) {
	t.Helper()

	if route.Scope == pulumi.ScopePublic {
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
		t.Fatalf("allowed request answers %d, want %d", rec.Code, http.StatusOK)
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

// TestCreateStackThroughWrapper proves a stack create still works end to end
// through the registration wrapper: the resolver reads the body for
// authorization and the handler reads it again to create the stack.
func TestCreateStackThroughWrapper(t *testing.T) {
	t.Parallel()

	handler, stores := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/stacks/acme/web",
		strings.NewReader(`{"stackName":"prod","tags":{"team":"web"}}`),
	)
	req.Header.Set("Authorization", "token test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("create answers %d, want %d", rec.Code, http.StatusOK)
	}

	if len(stores.created) != 1 {
		t.Fatalf("created %d stacks, want 1", len(stores.created))
	}

	got := stores.created[0].Ref
	if got.Namespace.String() != "acme" || got.Project.String() != "web" || got.Stack.String() != "prod" {
		t.Fatalf("created stack %v, want acme/web/prod", got)
	}

	if stores.created[0].Tags["team"] != "web" {
		t.Fatalf("created stack tags %v, want team=web", stores.created[0].Tags)
	}
}

// TestCreateStackThroughWrapperInvalidBody proves an invalid body still
// answers 400 through the wrapper, the same response the handler wrote
// before the wrapper resolved the body.
func TestCreateStackThroughWrapperInvalidBody(t *testing.T) {
	t.Parallel()

	handler, stores := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/stacks/acme/web",
		strings.NewReader(`{`),
	)
	req.Header.Set("Authorization", "token test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid body answers %d, want %d", rec.Code, http.StatusBadRequest)
	}

	if !strings.Contains(rec.Body.String(), "invalid JSON body") {
		t.Fatalf("invalid body answers %q, want invalid JSON body", rec.Body.String())
	}

	if len(stores.created) != 0 {
		t.Fatalf("created %d stacks, want 0", len(stores.created))
	}
}

// resolverFor returns the resolver that a route declares.
func resolverFor(t *testing.T, handler *pulumi.Handler, pattern string) pulumi.ObjectResolver {
	t.Helper()

	for _, route := range handler.Routes() {
		if route.Method+" "+route.Path == pattern {
			if route.Resolve == nil {
				t.Fatalf("route %q declares no resolver", pattern)
			}

			return route.Resolve
		}
	}

	t.Fatalf("route %q is not declared", pattern)

	return nil
}

func listRequest(t *testing.T, target string) *http.Request {
	t.Helper()

	return httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
}

// TestResolveCurrentUser proves the user route resolves the caller itself.
func TestResolveCurrentUser(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "GET /api/user")

	rec := httptest.NewRecorder()
	obj, err := resolve(rec, listRequest(t, "/api/user"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if obj.String() != "@caller" || obj.IsUnit() || !obj.IsCaller() {
		t.Fatalf("object = %q (unit %v caller %v), want caller", obj.String(), obj.IsUnit(), obj.IsCaller())
	}
}

func TestResolveListStacks(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "GET /api/user/stacks")

	cases := []struct {
		name   string
		target string
		want   string
	}{
		{"namespace only", "/api/user/stacks?organization=acme", "acme"},
		{"namespace and project", "/api/user/stacks?organization=acme&project=web", "acme"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			obj, err := resolve(rec, listRequest(t, tc.target))
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}

			if obj.String() != tc.want || obj.IsUnit() {
				t.Fatalf("object = %q (unit %v), want namespace %q", obj.String(), obj.IsUnit(), tc.want)
			}
		})
	}
}

func TestResolveListStacksInvalid(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "GET /api/user/stacks")

	cases := []struct {
		name   string
		target string
		want   error
	}{
		{"missing organization", "/api/user/stacks", namespace.ErrInvalidName},
		{"invalid organization", "/api/user/stacks?organization=../x", namespace.ErrInvalidName},
		{"invalid project", "/api/user/stacks?organization=acme&project=../x", stack.ErrInvalidName},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			if _, err := resolve(rec, listRequest(t, tc.target)); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func createRequest(t *testing.T, namespaceName, project, body string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/stacks/"+namespaceName+"/"+project,
		strings.NewReader(body),
	)
	req.SetPathValue("namespace", namespaceName)
	req.SetPathValue("project", project)

	return req
}

func TestResolveCreateStack(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "POST /api/stacks/{namespace}/{project}")

	rec := httptest.NewRecorder()
	obj, err := resolve(rec, createRequest(t, "acme", "web", `{"stackName":"prod"}`))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if want := "pulumi:acme/web/prod"; obj.String() != want || !obj.IsUnit() {
		t.Fatalf("object = %q (unit %v), want unit %q", obj.String(), obj.IsUnit(), want)
	}
}

// TestResolveCreateStackRestoresBody proves the handler can still read the
// body after the resolver has read it for authorization.
func TestResolveCreateStackRestoresBody(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "POST /api/stacks/{namespace}/{project}")

	const body = `{"stackName":"prod"}`

	req := createRequest(t, "acme", "web", body)
	rec := httptest.NewRecorder()

	if _, err := resolve(rec, req); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	restored := req.Body
	if restored == nil {
		t.Fatal("resolver left a nil body")
	}

	var decoded struct {
		StackName string `json:"stackName"`
	}

	if err := json.NewDecoder(req.Body).Decode(&decoded); err != nil {
		t.Fatalf("handler read: %v", err)
	}

	if decoded.StackName != "prod" {
		t.Fatalf("restored body stackName = %q, want %q", decoded.StackName, "prod")
	}
}

func TestResolveCreateStackInvalid(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "POST /api/stacks/{namespace}/{project}")

	cases := []struct {
		name      string
		namespace string
		body      string
		want      error
	}{
		{"invalid JSON", "acme", `{`, nil},
		{"invalid namespace", "../x", `{"stackName":"prod"}`, namespace.ErrInvalidName},
		{"invalid stack name", "acme", `{"stackName":"../x"}`, stack.ErrInvalidName},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			_, err := resolve(rec, createRequest(t, tc.namespace, "web", tc.body))
			if tc.want == nil {
				if err == nil {
					t.Fatal("resolve succeeded, want an error")
				}

				return
			}

			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestResolveGetStack(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "GET /api/stacks/{namespace}/{project}/{stack}")

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/api/stacks/acme/web/prod",
		http.NoBody,
	)
	req.SetPathValue("namespace", "acme")
	req.SetPathValue("project", "web")
	req.SetPathValue("stack", "prod")

	rec := httptest.NewRecorder()
	obj, err := resolve(rec, req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if want := "pulumi:acme/web/prod"; obj.String() != want || !obj.IsUnit() {
		t.Fatalf("object = %q (unit %v), want unit %q", obj.String(), obj.IsUnit(), want)
	}
}

func TestResolveGetStackInvalid(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "GET /api/stacks/{namespace}/{project}/{stack}")

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/api/stacks/acme/web/prod",
		http.NoBody,
	)
	req.SetPathValue("namespace", "acme")
	req.SetPathValue("project", "web")
	req.SetPathValue("stack", "../x")

	rec := httptest.NewRecorder()
	if _, err := resolve(rec, req); !errors.Is(err, stack.ErrInvalidName) {
		t.Fatalf("error = %v, want %v", err, stack.ErrInvalidName)
	}
}

// unusedUpdateStores satisfies the update ports for routes that never touch
// updates. Any call panics, which a test would report.
type unusedUpdateStores struct {
	stack.UpdateStore
	stack.CheckpointStore
	stack.EventStore
}

// newUnusedUpdates returns an Updates service for handlers whose tested routes
// never read or write updates.
func newUnusedUpdates(t *testing.T, namespaces *namespace.Service) *stack.Updates {
	t.Helper()

	stores := unusedUpdateStores{UpdateStore: nil, CheckpointStore: nil, EventStore: nil}

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Updates:            stores,
		Checkpoints:        stores,
		Events:             stores,
		Namespaces:         namespaces,
		Now:                nil,
		MaxCheckpointBytes: 0,
	})
	if err != nil {
		t.Fatalf("updates service: %v", err)
	}

	return updates
}
