package tfe_test

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/api/tfe"
	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/workspace"
)

// packageSources holds every Go file in this package, so TestNoMuxBypass and
// TestNoPulumiImport can prove the adapter shape from the sources.
//
//go:embed *.go
var packageSources embed.FS

// publicPatterns lists the routes that need no sign-in. Every other route
// must declare its actions in the route table.
func publicPatterns() []string {
	return []string{"GET /api/v2/test-public"}
}

// stubAuthenticator denies every token. The declaration tests send no token,
// so it never runs.
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

// stubWorkspaces resolves one workspace ID to acme/web. Unknown IDs answer
// workspace.ErrNotFound and ws-locked answers workspace.ErrLocked, so the
// error tests can prove the 404 and 409 documents through HTTP.
type stubWorkspaces struct{}

func (stubWorkspaces) GetWorkspaceByID(_ context.Context, id workspace.ID) (workspace.Workspace, error) {
	switch id.String() {
	case "ws-locked":
		return workspace.Workspace{}, workspace.ErrLocked
	case "ws-test123":
	default:
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	ns, err := namespace.ParseName("acme")
	if err != nil {
		return workspace.Workspace{}, err
	}

	name, err := workspace.ParseName("web")
	if err != nil {
		return workspace.Workspace{}, err
	}

	return workspace.Workspace{
		CreatedAt: time.Time{},
		UpdatedAt: time.Time{},
		Lock: workspace.Lock{
			LockedAt: time.Time{},
			ID:       "",
			Holder:   "",
			Reason:   "",
		},
		ID:       id,
		Ref:      workspace.Ref{Namespace: ns, Name: name},
		Revision: 1,
	}, nil
}

func newTestHandler(t *testing.T) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandlerWithRoutes(tfe.Config{
		Authenticator: stubAuthenticator{},
		Authorizer:    stubAuthorizer{},
		Logger:        slog.New(slog.DiscardHandler),
		Workspaces:    stubWorkspaces{},
	}, tfe.ExtraRoutes)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

func newEnforcingHandler(t *testing.T, authz identity.Authorizer) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandlerWithRoutes(tfe.Config{
		Authenticator: tokenAuthenticator{},
		Authorizer:    authz,
		Logger:        slog.New(slog.DiscardHandler),
		Workspaces:    stubWorkspaces{},
	}, tfe.ExtraRoutes)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// newProductionHandler returns a Handler with only the production table: no
// test-only extra routes.
func newProductionHandler(t *testing.T) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Authenticator: stubAuthenticator{},
		Authorizer:    stubAuthorizer{},
		Logger:        slog.New(slog.DiscardHandler),
		Workspaces:    stubWorkspaces{},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// TestRoutesDeclareActions walks every registered route and fails for a
// route with no declared action. It runs on the test routes and on the real
// table: every served pattern has a table row that passes the scope check,
// and every table row is served.
func TestRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	t.Run("test routes", func(t *testing.T) {
		t.Parallel()
		checkRoutesDeclareActions(t, newTestHandler(t), publicPatterns())
	})

	t.Run("production table", func(t *testing.T) {
		t.Parallel()
		checkRoutesDeclareActions(t, newProductionHandler(t), nil)
	})
}

// checkRoutesDeclareActions checks both directions: every served pattern has
// a table row that passes the scope check, and every table row is served.
func checkRoutesDeclareActions(t *testing.T, handler *tfe.Handler, publicList []string) {
	t.Helper()

	public := map[string]bool{}
	for _, pattern := range publicList {
		public[pattern] = true
	}

	byPattern := map[string]tfe.Route{}
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

// TestNoPulumiImport proves the TFE adapter never calls the other tool
// adapter: no source in this package imports it. The needle
// is built by concatenation so this file does not contain it literally.
func TestNoPulumiImport(t *testing.T) {
	t.Parallel()

	needle := "api/" + "pulumi"

	entries, err := packageSources.ReadDir(".")
	if err != nil {
		t.Fatalf("read package sources: %v", err)
	}

	for _, entry := range entries {
		content, err := packageSources.ReadFile(entry.Name())
		if err != nil {
			t.Fatalf("read file %s: %v", entry.Name(), err)
		}

		if strings.Contains(string(content), needle) {
			t.Fatalf("file %s imports the other tool adapter", entry.Name())
		}
	}
}

// errInvalidRoute reports a route table row that fails the scope check.
var errInvalidRoute = errors.New("invalid route")

// validateRoute fails for a route that is neither public nor authorized with
// actions and a resolver.
func validateRoute(route tfe.Route, public map[string]bool) error {
	switch route.Scope {
	case tfe.ScopePublic:
		return validatePublicRoute(route, public)
	case tfe.ScopeAuthorized:
		return validateAuthorizedRoute(route, public)
	default:
		return fmt.Errorf("%w: %q declares no scope", errInvalidRoute, route.Method+" "+route.Path)
	}
}

// validatePublicRoute fails for a public route that is not on the public
// list or that declares actions or a resolver.
func validatePublicRoute(route tfe.Route, public map[string]bool) error {
	pattern := route.Method + " " + route.Path

	if !public[pattern] {
		return fmt.Errorf("%w: %q is public but not on the public list", errInvalidRoute, pattern)
	}

	if len(route.Actions) != 0 {
		return fmt.Errorf("%w: public route %q declares actions", errInvalidRoute, pattern)
	}

	if route.Resolve != nil {
		return fmt.Errorf("%w: public route %q declares a resolver", errInvalidRoute, pattern)
	}

	return nil
}

// validateAuthorizedRoute fails for an authorized route that is on the
// public list or that declares no action or no resolver.
func validateAuthorizedRoute(route tfe.Route, public map[string]bool) error {
	pattern := route.Method + " " + route.Path

	if public[pattern] {
		return fmt.Errorf("%w: %q is on the public list but declares actions", errInvalidRoute, pattern)
	}

	if len(route.Actions) == 0 {
		return fmt.Errorf("%w: %q declares no action", errInvalidRoute, pattern)
	}

	if route.Resolve == nil {
		return fmt.Errorf("%w: %q declares actions but no resolver", errInvalidRoute, pattern)
	}

	return nil
}

// checkRouteScope fails the test for a route that validateRoute rejects.
func checkRouteScope(t *testing.T, route tfe.Route, public map[string]bool) {
	t.Helper()

	if err := validateRoute(route, public); err != nil {
		t.Fatalf("%v", err)
	}
}

// TestRouteWithoutActionsFails proves a route without actions fails the
// route test.
func TestRouteWithoutActionsFails(t *testing.T) {
	t.Parallel()

	route := tfe.Route{
		Method:  http.MethodGet,
		Path:    "/api/v2/test",
		Scope:   tfe.ScopeAuthorized,
		Actions: nil,
		Resolve: nil,
		Serve:   nil,
	}

	if err := validateRoute(route, map[string]bool{}); err == nil {
		t.Fatal("route without actions passed, want an error")
	}
}

// TestPublicRouteNotOnListFails proves a public route that is not on the
// public list fails the route test.
func TestPublicRouteNotOnListFails(t *testing.T) {
	t.Parallel()

	route := tfe.Route{
		Method:  http.MethodGet,
		Path:    "/api/v2/hidden",
		Scope:   tfe.ScopePublic,
		Actions: nil,
		Resolve: nil,
		Serve:   nil,
	}

	if err := validateRoute(route, map[string]bool{}); err == nil {
		t.Fatal("public route not on the list passed, want an error")
	}
}

// probeRoute checks that a declared route is registered: a request without a
// token must fail closed, never with 404.
func probeRoute(t *testing.T, handler *tfe.Handler, route tfe.Route) {
	t.Helper()

	req := routeRequest(t, route, "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("route %q is declared but not served", route.Method+" "+route.Path)
	}
}

// routeRequest builds a valid request for route: path placeholders filled
// and a JSON body where the route reads one. token is sent as a Bearer
// Authorization header when not empty.
func routeRequest(t *testing.T, route tfe.Route, token string) *http.Request {
	t.Helper()

	path := strings.NewReplacer(
		"{organization}", "acme",
		"{workspace}", "web",
		"{id}", "ws-test123",
	).Replace(route.Path)

	body := ""
	if route.Method == http.MethodPost {
		body = `{"data":{}}`
	}

	req := httptest.NewRequestWithContext(t.Context(), route.Method, path, strings.NewReader(body))

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	return req
}

// TestRoutesEnforceAuthentication proves the wrapper enforces the table. It
// is table-driven over every authorized route: without a token it answers
// 401, with a wrong token it answers 401, and with a token the Authorizer
// denies it answers 403. It runs on the test routes and on the real table.
func TestRoutesEnforceAuthentication(t *testing.T) {
	t.Parallel()

	t.Run("test routes", func(t *testing.T) {
		t.Parallel()

		handler := newTestHandler(t)

		for _, route := range handler.Routes() {
			if route.Scope != tfe.ScopeAuthorized {
				continue
			}

			t.Run(route.Method+" "+route.Path, func(t *testing.T) {
				t.Parallel()
				checkRouteAuthentication(t, route)
			})
		}
	})

	t.Run("production table", func(t *testing.T) {
		t.Parallel()

		handler := newProductionHandler(t)

		for _, route := range handler.Routes() {
			if route.Scope != tfe.ScopeAuthorized {
				continue
			}

			t.Run(route.Method+" "+route.Path, func(t *testing.T) {
				t.Parallel()
				checkRouteAuthentication(t, route)
			})
		}
	})
}

// checkRouteAuthentication proves one authorized route answers 401 without a
// token, 401 with a wrong token, 403 when denied, and 200 when allowed.
func checkRouteAuthentication(t *testing.T, route tfe.Route) {
	t.Helper()

	cases := []struct {
		name  string
		token string
		want  int
	}{
		{name: "no token", token: "", want: http.StatusUnauthorized},
		{name: "wrong token", token: "wrong-token", want: http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

			rec := httptest.NewRecorder()
			allowed.ServeHTTP(rec, routeRequest(t, route, tc.token))

			if rec.Code != tc.want {
				t.Fatalf("request answers %d, want %d", rec.Code, tc.want)
			}

			checkJSONAPIError(t, rec, tc.want)
		})
	}

	t.Run("denied", func(t *testing.T) {
		t.Parallel()

		deny := map[identity.Action]bool{}
		for _, action := range route.Actions {
			deny[action] = true
		}

		denied := newEnforcingHandler(t, selectiveAuthorizer{deny: deny})

		rec := httptest.NewRecorder()
		denied.ServeHTTP(rec, routeRequest(t, route, "test-token"))

		if rec.Code != http.StatusForbidden {
			t.Fatalf("denied request answers %d, want %d", rec.Code, http.StatusForbidden)
		}

		checkJSONAPIError(t, rec, http.StatusForbidden)
	})

	t.Run("allowed", func(t *testing.T) {
		t.Parallel()

		allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

		rec := httptest.NewRecorder()
		allowed.ServeHTTP(rec, routeRequest(t, route, "test-token"))

		if rec.Code != http.StatusOK {
			t.Fatalf("allowed request answers %d, want %d", rec.Code, http.StatusOK)
		}

		if got := rec.Header().Get("Content-Type"); got != "application/vnd.api+json" {
			t.Fatalf("media type = %q, want %q", got, "application/vnd.api+json")
		}
	})
}

// jsonAPIErrorItem is one entry of a JSON:API error document.
type jsonAPIErrorItem struct {
	Status string `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// jsonAPIErrorDoc decodes a JSON:API error document in tests.
type jsonAPIErrorDoc struct {
	Errors []jsonAPIErrorItem `json:"errors"`
}

// resolveIDAttributes is the name attribute in the ID test body.
type resolveIDAttributes struct {
	Name string `json:"name"`
}

// resolveIDData is the data object in the ID test body.
type resolveIDData struct {
	Attributes resolveIDAttributes `json:"attributes"`
}

// resolveIDBody is the body the ID resolver restores for the handler.
type resolveIDBody struct {
	Data resolveIDData `json:"data"`
}

// checkJSONAPIError proves rec is a JSON:API error with the media type and
// an errors array of status, title and detail. Status is the HTTP status
// code as a string, for example "404".
func checkJSONAPIError(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()

	if rec.Code != want {
		t.Fatalf("status = %d, want %d", rec.Code, want)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/vnd.api+json" {
		t.Fatalf("media type = %q, want %q", got, "application/vnd.api+json")
	}

	var doc jsonAPIErrorDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode error body: %v", err)
	}

	if len(doc.Errors) == 0 {
		t.Fatal("error document holds no errors")
	}

	wantStatus := strconv.Itoa(want)

	for _, item := range doc.Errors {
		if item.Status == "" || item.Title == "" || item.Detail == "" {
			t.Fatalf("error entry %+v misses status, title or detail", item)
		}

		if item.Status != wantStatus {
			t.Fatalf("error status = %q, want %q", item.Status, wantStatus)
		}
	}
}

// TestErrorDocuments proves every error helper writes a JSON:API document
// with status, title and detail and the media type. Status is the HTTP
// status code as a string: "401", "403", "404", "409" and "422" ("500" is
// covered by TestErrorHidesInternalDetail).
func TestErrorDocuments(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	cases := []struct {
		name   string
		target string
		want   int
		deny   bool
	}{
		{name: "unauthorized", target: "/api/v2/test-by-name/acme/web", want: http.StatusUnauthorized, deny: false},
		{name: "forbidden", target: "/api/v2/test-by-name/acme/web", want: http.StatusForbidden, deny: true},
		{name: "not found", target: "/api/v2/test-by-id/ws-missing", want: http.StatusNotFound, deny: false},
		{name: "conflict", target: "/api/v2/test-by-id/ws-locked", want: http.StatusConflict, deny: false},
		{
			name:   "unprocessable",
			target: "/api/v2/test-by-name/acme/bad!",
			want:   http.StatusUnprocessableEntity,
			deny:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var rec *httptest.ResponseRecorder

			switch {
			case tc.want == http.StatusUnauthorized:
				rec = httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequestWithContext(
					t.Context(),
					http.MethodPost,
					tc.target,
					strings.NewReader(`{"data":{}}`),
				))
			case tc.deny:
				denied := newEnforcingHandler(t, stubAuthorizer{})
				req := httptest.NewRequestWithContext(
					t.Context(),
					http.MethodPost,
					tc.target,
					strings.NewReader(`{"data":{}}`),
				)
				req.Header.Set("Authorization", "Bearer test-token")
				rec = httptest.NewRecorder()
				denied.ServeHTTP(rec, req)
			default:
				allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})
				req := httptest.NewRequestWithContext(
					t.Context(),
					http.MethodPost,
					tc.target,
					strings.NewReader(`{"data":{}}`),
				)
				req.Header.Set("Authorization", "Bearer test-token")
				rec = httptest.NewRecorder()
				allowed.ServeHTTP(rec, req)
			}

			if rec.Code != tc.want {
				t.Fatalf("request answers %d, want %d", rec.Code, tc.want)
			}

			checkJSONAPIError(t, rec, tc.want)
		})
	}
}

// TestErrorHidesInternalDetail proves a 500 never returns an internal error
// text.
func TestErrorHidesInternalDetail(t *testing.T) {
	t.Parallel()

	handler, err := tfe.NewHandlerWithRoutes(tfe.Config{
		Authenticator: tokenAuthenticator{},
		Authorizer:    selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:        slog.New(slog.DiscardHandler),
		Workspaces:    nil,
	}, tfe.ExtraRoutes)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/v2/test-by-id/ws-test123",
		strings.NewReader(`{}`),
	)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("request answers %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	checkJSONAPIError(t, rec, http.StatusInternalServerError)

	var doc jsonAPIErrorDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode error body: %v", err)
	}

	if len(doc.Errors) != 1 {
		t.Fatalf("error document holds %d errors, want 1", len(doc.Errors))
	}

	if doc.Errors[0].Status != strconv.Itoa(http.StatusInternalServerError) {
		t.Fatalf("error status = %q, want %q", doc.Errors[0].Status, "500")
	}

	if strings.Contains(doc.Errors[0].Detail, "lookup") {
		t.Fatalf("internal error leaks detail %q", doc.Errors[0].Detail)
	}
}

// TestProductionTableHasNoTestRoutes proves the production table serves no
// test path: test-only routes reach only handlers built with
// NewHandlerWithRoutes.
func TestProductionTableHasNoTestRoutes(t *testing.T) {
	t.Parallel()

	handler := newProductionHandler(t)

	for _, route := range handler.Routes() {
		if strings.Contains(route.Path, "test") {
			t.Fatalf("production route %q contains test", route.Method+" "+route.Path)
		}
	}

	for _, pattern := range handler.RegisteredPatterns() {
		if strings.Contains(pattern, "test") {
			t.Fatalf("production pattern %q contains test", pattern)
		}
	}
}

// resolverFor returns the resolver that a route declares.
func resolverFor(t *testing.T, handler *tfe.Handler, pattern string) tfe.ObjectResolver {
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

// TestResolveWorkspaceByName proves the name route resolves the unit in the
// path.
func TestResolveWorkspaceByName(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "POST /api/v2/test-by-name/{organization}/{workspace}")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v2/test-by-name/acme/web", http.NoBody)
	req.SetPathValue("organization", "acme")
	req.SetPathValue("workspace", "web")

	rec := httptest.NewRecorder()
	obj, err := resolve(rec, req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if want := "opentofu:acme/web"; obj.String() != want || !obj.IsUnit() {
		t.Fatalf("object = %q (unit %v), want unit %q", obj.String(), obj.IsUnit(), want)
	}
}

// TestResolveWorkspaceByNameInvalid proves invalid names fail.
func TestResolveWorkspaceByNameInvalid(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "POST /api/v2/test-by-name/{organization}/{workspace}")

	cases := []struct {
		name          string
		organization  string
		workspaceName string
		want          error
	}{
		{name: "invalid organization", organization: "../x", workspaceName: "web", want: namespace.ErrInvalidName},
		{name: "invalid workspace", organization: "acme", workspaceName: "../x", want: workspace.ErrInvalidName},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", http.NoBody)
			req.SetPathValue("organization", tc.organization)
			req.SetPathValue("workspace", tc.workspaceName)

			rec := httptest.NewRecorder()
			if _, err := resolve(rec, req); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestResolveWorkspaceByID proves the ID route resolves the ID to the unit
// name and restores the body for the handler.
func TestResolveWorkspaceByID(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "POST /api/v2/test-by-id/{id}")

	const body = `{"data":{"attributes":{"name":"web"}}}`

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/v2/test-by-id/ws-test123",
		strings.NewReader(body),
	)
	req.SetPathValue("id", "ws-test123")

	rec := httptest.NewRecorder()
	obj, err := resolve(rec, req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if want := "opentofu:acme/web"; obj.String() != want || !obj.IsUnit() {
		t.Fatalf("object = %q (unit %v), want unit %q", obj.String(), obj.IsUnit(), want)
	}

	if req.Body == nil {
		t.Fatal("resolver left a nil body")
	}

	var decoded resolveIDBody

	if err := json.NewDecoder(req.Body).Decode(&decoded); err != nil {
		t.Fatalf("handler read: %v", err)
	}

	if decoded.Data.Attributes.Name != "web" {
		t.Fatalf("restored body name = %q, want %q", decoded.Data.Attributes.Name, "web")
	}
}

// TestResolveWorkspaceByIDNotFound proves an unknown ID fails with the
// workspace error.
func TestResolveWorkspaceByIDNotFound(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "POST /api/v2/test-by-id/{id}")

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/v2/test-by-id/ws-missing",
		strings.NewReader(`{}`),
	)
	req.SetPathValue("id", "ws-missing")

	rec := httptest.NewRecorder()
	if _, err := resolve(rec, req); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("error = %v, want %v", err, workspace.ErrNotFound)
	}
}

// TestPublicRouteServesWithoutToken proves the public test route answers 200
// with no sign-in.
func TestPublicRouteServesWithoutToken(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v2/test-public", http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("public route answers %d, want %d", rec.Code, http.StatusOK)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/vnd.api+json" {
		t.Fatalf("media type = %q, want %q", got, "application/vnd.api+json")
	}
}
