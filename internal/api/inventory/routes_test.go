package inventory_test

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
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
		ID:              namespace.NewID(),
		CreatedAt:       changeTime(),
		Name:            "acme",
		HomeRegion:      "test-region",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	return []namespace.Namespace{ns}, nil
}

func (homedNamespaceStore) Ping(_ context.Context) error {
	return nil
}

func newTestHandler(t *testing.T) *apiinventory.Handler {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}

	namespaces, err := namespace.NewService(
		namespace.Config{
			Store:  nsStore,
			Now:    nil,
			Region: "test-region",
		},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := apiinventory.NewHandler(apiinventory.Config{
		Views: testViewsConfig(t),
		Audit: nil, OnAuditFailure: nil, Locks: nil, Moves: nil,
		Collections:   testCollectionService(t, nsStore),
		Namespaces:    namespaces,
		Units:         &memReader{entries: map[namespace.Name][]coreinventory.Entry{}, err: nil},
		Deleted:       seededDeleted(),
		History:       nil,
		Lifecycles:    testLifecycles(),
		ReadScope:     fixedScope{err: nil, ids: nil, all: true},
		Authenticator: stubAuthenticator{},
		Authorizer:    stubAuthorizer{},
		Sessions:      nil,
		Logger:        slog.New(slog.DiscardHandler),
		Now:           nil,
		PublicURL:     "",
		DenialLimiter: nil,
		StateGuard:    nil,
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
		Views: nil,
		Audit: nil, OnAuditFailure: nil, Locks: nil, Moves: nil,
		Collections:   testCollectionService(t, homedNamespaceStore{}),
		Namespaces:    namespaces,
		Units:         &memReader{entries: enforcingUnitEntries(), err: nil},
		Deleted:       seededDeleted(),
		History:       nil,
		Lifecycles:    testLifecycles(),
		ReadScope:     fixedScope{err: nil, ids: nil, all: true},
		Authenticator: tokenAuthenticator{},
		Authorizer:    authz,
		Sessions:      nil,
		Logger:        slog.New(slog.DiscardHandler),
		Now:           nil,
		PublicURL:     "",
		DenialLimiter: nil,
		StateGuard:    nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// enforcingUnitEntries holds the live unit that the by-ID route resolves in
// the enforcement tests: the unit of seededUnitID in acme.
func enforcingUnitEntries() map[namespace.Name][]coreinventory.Entry {
	return map[namespace.Name][]coreinventory.Entry{
		"acme": {{
			ManualLock:    nil,
			LastChanged:   changeTime(),
			Namespace:     "acme",
			Tool:          "pulumi",
			Name:          "web/prod",
			UnitID:        seededUnitID,
			DisplayName:   "web/prod",
			Status:        "succeeded",
			ResourceCount: nil,
			Lock:          nil,
			Collection:    "",
			Labels:        nil,
		}},
	}
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
	for _, rt := range handler.Routes() {
		pattern := rt.Method + " " + rt.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("duplicate route %q", pattern)
		}

		byPattern[pattern] = rt

		checkRouteScope(t, rt, public)
	}

	for _, pattern := range handler.RegisteredPatterns() {
		if _, ok := byPattern[pattern]; !ok {
			t.Fatalf("served route %q has no declared action", pattern)
		}
	}

	for _, rt := range byPattern {
		probeRoute(t, handler, rt)
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
func checkRouteScope(t *testing.T, rt apiinventory.Route, public map[string]bool) {
	t.Helper()

	pattern := rt.Method + " " + rt.Path

	switch rt.Scope {
	case route.ScopePublic:
		checkPublicRoute(t, rt, public)
	case route.ScopeAuthorized:
		checkAuthorizedRoute(t, rt, public)
	default:
		t.Fatalf("route %q declares no scope", pattern)
	}
}

// checkPublicRoute fails for a public route that is not on the public list
// or that declares actions or a resolver.
func checkPublicRoute(t *testing.T, rt apiinventory.Route, public map[string]bool) {
	t.Helper()

	pattern := rt.Method + " " + rt.Path

	if !public[pattern] {
		t.Fatalf("route %q is public but not on the public list", pattern)
	}

	if len(rt.Actions) != 0 {
		t.Fatalf("public route %q declares actions", pattern)
	}

	if rt.Resolve != nil {
		t.Fatalf("public route %q declares a resolver", pattern)
	}
}

// checkAuthorizedRoute fails for an authorized route that is on the public
// list or that declares no action or no resolver.
func checkAuthorizedRoute(t *testing.T, rt apiinventory.Route, public map[string]bool) {
	t.Helper()

	pattern := rt.Method + " " + rt.Path

	if public[pattern] {
		t.Fatalf("route %q is on the public list but declares actions", pattern)
	}

	if len(rt.Actions) == 0 {
		t.Fatalf("route %q declares no action", pattern)
	}

	if rt.Resolve == nil {
		t.Fatalf("route %q declares actions but no resolver", pattern)
	}
}

// probeRoute checks that a declared route is registered: a request without a
// token must fail closed, never with 404.
func probeRoute(t *testing.T, handler *apiinventory.Handler, rt apiinventory.Route) {
	t.Helper()

	req := routeRequest(t, rt, "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("route %q is declared but not served", rt.Method+" "+rt.Path)
	}
}

// routeRequest builds a valid request for route: the organization query
// value where the route reads it. token is sent as the Authorization header
// when not empty.
func routeRequest(t *testing.T, rt apiinventory.Route, token string) *http.Request {
	t.Helper()

	path := strings.NewReplacer("{tool}", "pulumi", "{unitID}", seededUnitID, "{namespace}", "acme").Replace(rt.Path)
	target := path

	if !strings.Contains(rt.Path, "{namespace}") {
		target += "?organization=acme"
	}

	req := httptest.NewRequestWithContext(t.Context(), rt.Method, target, http.NoBody)

	if strings.Contains(rt.Path, "{namespace}") {
		req.SetPathValue("namespace", "acme")
	}

	if strings.Contains(rt.Path, "{unitID}") {
		req.SetPathValue("tool", "pulumi")
		req.SetPathValue("unitID", seededUnitID)
	}

	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	return req
}

// expectedObject is the object wire form that route resolves for
// routeRequest, and whether it is a unit: the deleted unit for the routes
// with a unit ID in the path, else the namespace.
func expectedObject(rt apiinventory.Route) (string, bool) {
	if strings.Contains(rt.Path, "{unitID}") {
		return "pulumi:acme/web/prod", true
	}

	return "acme", false
}

// TestRoutesEnforceAuthorization proves the table is enforced: with an
// Authorizer that denies a declared action the route answers 403, and it
// answers 200 or 204 when allowed. Table-driven over all routes.
func TestRoutesEnforceAuthorization(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	for _, rt := range handler.Routes() {
		if isCollectionRoute(rt) {
			continue
		}

		t.Run(rt.Method+" "+rt.Path, func(t *testing.T) {
			t.Parallel()
			checkRouteEnforcement(t, rt)
		})
	}
}

// checkRouteEnforcement proves one route enforces its row: it answers 200
// or 204 when allowed, and an authorized route answers 403 when its actions are
// denied.
func checkRouteEnforcement(t *testing.T, rt apiinventory.Route) {
	t.Helper()

	if rt.Scope == route.ScopePublic {
		allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

		rec := httptest.NewRecorder()
		allowed.ServeHTTP(rec, routeRequest(t, rt, ""))

		if rec.Code != http.StatusOK {
			t.Fatalf("public route answers %d, want %d", rec.Code, http.StatusOK)
		}

		return
	}

	allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	rec := httptest.NewRecorder()
	allowed.ServeHTTP(rec, routeRequest(t, rt, "test-token"))

	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("allowed request answers %d, want %d or %d (%s)",
			rec.Code, http.StatusOK, http.StatusNoContent, rec.Body.String())
	}

	deny := map[identity.Action]bool{}
	for _, action := range rt.Actions {
		deny[action] = true
	}

	for _, action := range rt.NamespaceActions {
		deny[action] = true
	}

	wantDenied := http.StatusForbidden
	if rt.DeniedAsNotFound {
		wantDenied = http.StatusNotFound
	}

	denied := newEnforcingHandler(t, selectiveAuthorizer{deny: deny})

	rec = httptest.NewRecorder()
	denied.ServeHTTP(rec, routeRequest(t, rt, "test-token"))

	if rec.Code != wantDenied {
		t.Fatalf("denied request answers %d, want %d", rec.Code, wantDenied)
	}
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

// ListObjects gives an administrator every object and any other principal none.
func (*recordingAuthorizer) ListObjects(
	_ context.Context,
	p identity.Principal,
	_ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// denyObjectAuthorizer denies the one object wire form in deny and allows
// everything else, so tests prove a route authorizes the resolved object:
// denying it answers 403.
type denyObjectAuthorizer struct {
	deny string
}

func (d denyObjectAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if obj.String() == d.deny {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	return nil
}

// ListObjects gives an administrator every object and any other principal none.
func (denyObjectAuthorizer) ListObjects(
	_ context.Context,
	p identity.Principal,
	_ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// TestRoutesAuthorizeDeclaredObject proves every authorized route asks for
// its declared actions on the object its resolver returns: the recording
// Authorizer sees each declared action on the resolved object, and denying
// that object answers 403.
func TestRoutesAuthorizeDeclaredObject(t *testing.T) {
	t.Parallel()

	handler := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})

	for _, rt := range handler.Routes() {
		if isCollectionRoute(rt) {
			continue
		}

		t.Run(rt.Method+" "+rt.Path, func(t *testing.T) {
			t.Parallel()
			checkRouteObject(t, rt)
		})
	}
}

// checkRouteObject proves one route authorizes its declared actions on its
// resolved object.
func checkRouteObject(t *testing.T, rt apiinventory.Route) {
	t.Helper()

	if rt.Scope == route.ScopePublic {
		checkPublicRouteObject(t, rt)

		return
	}

	checkAuthorizedRouteObject(t, rt)
}

// checkPublicRouteObject proves a public route serves without sign-in.
func checkPublicRouteObject(t *testing.T, rt apiinventory.Route) {
	t.Helper()

	allowed := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})

	rec := httptest.NewRecorder()
	allowed.ServeHTTP(rec, routeRequest(t, rt, ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("public route answers %d, want %d", rec.Code, http.StatusOK)
	}
}

// checkAuthorizedRouteObject proves an authorized route asks for its declared
// actions on its resolved object: the recorder sees each action on it, and
// denying it answers 403.
func checkAuthorizedRouteObject(t *testing.T, rt apiinventory.Route) {
	t.Helper()

	expected, unit := expectedObject(rt)

	obj, err := rt.Resolve(httptest.NewRecorder(), routeRequest(t, rt, "test-token"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if obj.String() != expected || obj.IsUnit() != unit {
		t.Fatalf("object = %q (unit %v), want %q (unit %v)", obj.String(), obj.IsUnit(), expected, unit)
	}

	recorder := &recordingAuthorizer{calls: nil}
	allowed := newEnforcingHandler(t, recorder)

	rec := httptest.NewRecorder()
	allowed.ServeHTTP(rec, routeRequest(t, rt, "test-token"))

	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("allowed request answers %d, want %d or %d (%s)",
			rec.Code, http.StatusOK, http.StatusNoContent, rec.Body.String())
	}

	for _, action := range rt.Actions {
		if !containsCall(recorder.calls, recordedCall{action: action, object: expected}) {
			t.Fatalf("route never asks %s on %q (calls %v)", action, expected, recorder.calls)
		}
	}

	nsObject := identity.NamespaceObject("acme").String()

	for _, action := range rt.NamespaceActions {
		if !containsCall(recorder.calls, recordedCall{action: action, object: nsObject}) {
			t.Fatalf("route never asks %s on %q (calls %v)", action, nsObject, recorder.calls)
		}
	}

	wantDenied := http.StatusForbidden
	if rt.DeniedAsNotFound {
		wantDenied = http.StatusNotFound
	}

	denied := newEnforcingHandler(t, denyObjectAuthorizer{deny: expected})

	rec = httptest.NewRecorder()
	denied.ServeHTTP(rec, routeRequest(t, rt, "test-token"))

	if rec.Code != wantDenied {
		t.Fatalf("request denied on %q answers %d, want %d", expected, rec.Code, wantDenied)
	}
}

// containsCall reports whether calls holds want.
func containsCall(calls []recordedCall, want recordedCall) bool {
	return slices.Contains(calls, want)
}

// SetOverrides is not used by this fake.
func (homedNamespaceStore) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}
