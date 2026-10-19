package objectcontext_test

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	contextapi "opendustpan.org/opendustpan/internal/api/objectcontext"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// packageSources holds every Go file in this package, so TestNoMuxBypass can
// prove that no production file reaches the mux without recording.
//
//go:embed *.go
var packageSources embed.FS

const (
	testToken  = "context-test-token"
	testUnitID = "ws-1"
)

// fakeNamespaces answers every namespace except "absent", homed in the test
// region.
type fakeNamespaces struct{}

func (fakeNamespaces) CreateNamespace(context.Context, namespace.Namespace) error { return nil }

func (fakeNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name == "absent" {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Unix(0, 0), Name: name, HomeRegion: "test-region", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

func (fakeNamespaces) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

func (fakeNamespaces) ListNamespaces(context.Context) ([]namespace.Namespace, error) { return nil, nil }

func (fakeNamespaces) Ping(context.Context) error { return nil }

// fakeUnits holds the workspace ws-1 in acme.
type fakeUnits struct{}

func (fakeUnits) ListUnits(_ context.Context, ns namespace.Name) ([]coreinventory.Entry, error) {
	if ns != "acme" {
		return nil, nil
	}

	return []coreinventory.Entry{{
		LastChanged: time.Unix(0, 0), Namespace: ns, Tool: "opentofu", Name: "net", UnitID: testUnitID,
		DisplayName: "", Status: "", ResourceCount: nil, Lock: nil, Collection: "",
	}}, nil
}

// fakeDeleted holds no deleted unit.
type fakeDeleted struct{}

func (fakeDeleted) ListDeletedUnits(context.Context, namespace.Name) ([]coreinventory.DeletedEntry, error) {
	return nil, nil
}

func (fakeDeleted) QueryDeletedUnits(context.Context, coreinventory.DeletedQuery) (coreinventory.DeletedPage, error) {
	return coreinventory.DeletedPage{Next: nil, Entries: nil, Total: 0}, nil
}

func (fakeDeleted) GetDeletedUnit(context.Context, namespace.Name, string, string) (coreinventory.DeletedEntry, error) {
	return coreinventory.DeletedEntry{}, coreinventory.ErrNotFound
}

// fakeStore keeps no context and counts the writes.
type fakeStore struct {
	writes int
	mu     sync.Mutex
}

func newFakeStore() *fakeStore {
	return &fakeStore{writes: 0, mu: sync.Mutex{}}
}

func (*fakeStore) Entries(context.Context, []objectcontext.Ref) ([]objectcontext.Entry, error) {
	return nil, nil
}

//nolint:revive // The signature of the objectcontext.Store port.
func (s *fakeStore) SetAPI(
	_ context.Context, object objectcontext.Ref, p objectcontext.Patch, by string, _ namespace.Fence, e audit.Entry,
) (objectcontext.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.writes++

	none := objectcontext.Fields{Summary: "", Runbook: "", Owners: nil, OnCall: "", Links: nil}

	fields, err := p.Apply(none)
	if err != nil {
		return objectcontext.Entry{}, fmt.Errorf("apply: %w", err)
	}

	return objectcontext.Entry{
		UpdatedAt: e.At, Object: object, Source: objectcontext.SourceAPI, UpdatedBy: by, Fields: fields,
		Readme: "", Item: "", Warning: "",
	}, nil
}

func (s *fakeStore) DeleteAPI(context.Context, objectcontext.Ref, namespace.Fence, audit.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.writes++

	return nil
}

func (*fakeStore) ReplaceOutput(
	context.Context, objectcontext.Ref, objectcontext.OutputItem, []objectcontext.Entry, namespace.Fence,
) error {
	return nil
}

func (*fakeStore) DropUnit(context.Context, objectcontext.Ref, namespace.Fence) error { return nil }

func (*fakeStore) ByOwner(context.Context, namespace.Name, string, string, int) ([]objectcontext.Entry, string, error) {
	return nil, "", nil
}

func (s *fakeStore) written() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.writes
}

// tokenAuthenticator signs in the principal ada for testToken.
type tokenAuthenticator struct{}

func (tokenAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token != testToken {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		Subject: "ada", DisplayName: "ada", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Kind:       "",
		Credential: "",
		JobID:      "",
	}, nil
}

// errNoListing reports a call of ListObjects, which no route of this API makes.
var errNoListing = errors.New("no route lists objects")

// recordingAuthorizer allows the checks that holds names, as "<action> <object>",
// and records every check in order.
type recordingAuthorizer struct {
	holds  map[string]bool
	checks []string
	mu     sync.Mutex
}

func newAuthorizer(holds ...string) *recordingAuthorizer {
	a := &recordingAuthorizer{holds: map[string]bool{}, checks: nil, mu: sync.Mutex{}}
	for _, h := range holds {
		a.holds[h] = true
	}

	return a
}

func (a *recordingAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, obj identity.Object,
) error {
	key := string(action) + " " + obj.String()

	a.mu.Lock()
	defer a.mu.Unlock()

	a.checks = append(a.checks, key)

	if !a.holds[key] {
		return fmt.Errorf("%w: %s", identity.ErrDenied, key)
	}

	return nil
}

// ListObjects implements identity.Authorizer. No route of this API lists
// objects.
func (*recordingAuthorizer) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoListing
}

func (a *recordingAuthorizer) seen() []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	return slices.Clone(a.checks)
}

func newHandler(t *testing.T, authz identity.Authorizer, store *fakeStore) *contextapi.Handler {
	t.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: fakeNamespaces{}, Now: nil, Region: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	writer, err := objectcontext.NewWriter(
		objectcontext.WriterConfig{Store: store, Namespaces: fakeNamespaces{}, Now: nil},
	)
	if err != nil {
		t.Fatalf("writer: %v", err)
	}

	handler, err := contextapi.NewHandler(contextapi.Config{
		Namespaces: namespaces, Units: fakeUnits{}, Deleted: fakeDeleted{}, Collections: nil, Entries: store,
		Writer: writer, Authenticator: tokenAuthenticator{}, Authorizer: authz, Sessions: nil,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// request is one call of a test: the method, the path, the token and the body.
type request struct {
	method, path, token, body string
}

func serve(t *testing.T, h http.Handler, r request) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), r.method, r.path, strings.NewReader(r.body))
	if r.token != "" {
		req.Header.Set("Authorization", "token "+r.token)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// routeCase is a route with the request path and the checks that the route
// makes, in order.
type routeCase struct {
	route  contextapi.Route
	path   string
	body   string
	checks []string
	reads  int
}

// casesOf returns a routeCase for each route of the table. The namespace read
// is the first check of each route. A check that a route names twice, the
// namespace read of a namespace route, runs once.
func casesOf(t *testing.T) []routeCase {
	t.Helper()

	routes := newHandler(t, newAuthorizer(), newFakeStore()).Routes()
	out := make([]routeCase, 0, len(routes))

	for _, route := range routes {
		path := strings.NewReplacer("{namespace}", "acme", "{tool}", "opentofu", "{unit_id}", testUnitID).
			Replace(route.Path)
		object := "acme"

		if strings.Contains(route.Path, "{unit_id}") {
			object = "opentofu:acme/net"
		}

		checks := []string{string(identity.ActionNamespaceRead) + " acme"}

		for _, a := range route.Reads {
			if c := string(a) + " " + object; !slices.Contains(checks, c) {
				checks = append(checks, c)
			}
		}

		reads := len(checks)

		for _, a := range route.Actions {
			checks = append(checks, string(a)+" "+object)
		}

		body := ""
		if route.Method == http.MethodPatch {
			body = `{"onCall":"pager:x"}`
		}

		out = append(out, routeCase{route: route, path: path, body: body, checks: checks, reads: reads})
	}

	return out
}

// wantStatus returns the status that a caller gets when it holds the first
// held checks of the route.
func (rc *routeCase) wantStatus(held int) int {
	switch {
	case held < rc.reads:
		return http.StatusNotFound
	case held < len(rc.checks):
		return http.StatusForbidden
	case rc.route.Method == http.MethodDelete:
		return http.StatusNoContent
	default:
		return http.StatusOK
	}
}

// TestRoutesDeclareActions walks every route and fails for a route with no
// scope, no read action or no resolver, and for a write route with no set
// action. It checks both directions: every served pattern has a table row,
// and every table row is served.
func TestRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	handler := newHandler(t, newAuthorizer(), newFakeStore())
	byPattern := map[string]contextapi.Route{}

	for _, route := range handler.Routes() {
		pattern := route.Method + " " + route.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("duplicate route %q", pattern)
		}

		byPattern[pattern] = route

		switch {
		case route.Scope != contextapi.ScopeAuthorized:
			t.Errorf("route %q is not authorized: this API serves no public route", pattern)
		case len(route.Reads) == 0:
			t.Errorf("route %q declares no read action", pattern)
		case route.Resolve == nil || route.Serve == nil:
			t.Errorf("route %q declares no resolver or no handler", pattern)
		case (route.Method == http.MethodGet) != (len(route.Actions) == 0):
			t.Errorf("route %q: only a read route may declare no set action", pattern)
		default:
		}
	}

	for _, pattern := range handler.RegisteredPatterns() {
		if _, ok := byPattern[pattern]; !ok {
			t.Errorf("served route %q has no table row", pattern)
		}
	}

	checkSignedOut(t, handler)
}

// checkSignedOut proves that each route fails closed for a request with no
// token: it answers 401, never 404.
func checkSignedOut(t *testing.T, handler http.Handler) {
	t.Helper()

	cases := casesOf(t)

	for i := range cases {
		rc := &cases[i]
		rec := serve(t, handler, request{method: rc.route.Method, path: rc.path, token: "", body: ""})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("route %q with no token = %d, want 401", rc.route.Method+" "+rc.route.Path, rec.Code)
		}
	}
}

// TestNoMuxBypass proves every served route goes through the recorder: it
// fails if any production file in this package builds a ServeMux or touches
// the inner mux of patternMux outside routes.go.
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

// TestRoutesEnforceTheChecks proves, for every route, the order and the
// answer of the checks: a refused namespace read or object read answers the
// one 404, a refused set action answers 403, and a caller who holds every
// check passes. A refused write never reaches the store.
func TestRoutesEnforceTheChecks(t *testing.T) {
	t.Parallel()

	cases := casesOf(t)

	for i := range cases {
		rc := &cases[i]

		for held := range len(rc.checks) + 1 {
			t.Run(fmt.Sprintf("%s %s holding %d of %d checks", rc.route.Method, rc.route.Path, held, len(rc.checks)),
				func(t *testing.T) {
					t.Parallel()
					checkRoute(t, rc, held)
				})
		}
	}
}

// checkRoute runs rc with a caller that holds the first held checks.
func checkRoute(t *testing.T, rc *routeCase, held int) {
	t.Helper()

	authz := newAuthorizer(rc.checks[:held]...)
	store := newFakeStore()
	rec := serve(
		t,
		newHandler(t, authz, store),
		request{method: rc.route.Method, path: rc.path, token: testToken, body: rc.body},
	)

	want := rc.wantStatus(held)
	if rec.Code != want {
		t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), want)
	}

	// The read document asks the same questions again for each layer of its
	// chain, so a read route may repeat checks.
	wantChecks := rc.checks[:min(held+1, len(rc.checks))]
	if got := authz.seen(); len(got) < len(wantChecks) || !slices.Equal(got[:len(wantChecks)], wantChecks) {
		t.Errorf("checks = %q, want %q first, in order", got, wantChecks)
	}

	if rc.route.Method == http.MethodGet {
		return
	}

	if wrote := store.written() == 1; wrote != (held == len(rc.checks)) {
		t.Errorf("store writes = %d for status %d", store.written(), want)
	}
}

// TestUnknownObjectsAnswerAlike proves an unknown namespace and an unknown
// unit answer 404 before any check of the caller runs for the object.
func TestUnknownObjectsAnswerAlike(t *testing.T) {
	t.Parallel()

	authz := newAuthorizer()
	handler := newHandler(t, authz, newFakeStore())

	for _, path := range []string{
		"/api/context/namespaces/absent",
		"/api/context/namespaces/absent/units/opentofu/" + testUnitID,
		"/api/context/namespaces/acme/units/opentofu/ws-absent",
		"/api/context/namespaces/other/units/opentofu/" + testUnitID,
		"/api/context/namespaces/BAD%20NAME",
	} {
		rec := serve(t, handler, request{method: http.MethodGet, path: path, token: testToken, body: ""})
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"message":"not found"`) {
			t.Errorf("GET %s = %d %s, want the 404 of an unknown object", path, rec.Code, rec.Body.String())
		}
	}

	if got := authz.seen(); len(got) != 0 {
		t.Errorf("checks = %q, want none for an object that does not exist", got)
	}
}

// TestNewHandlerRequiresDependencies proves each required dependency.
func TestNewHandlerRequiresDependencies(t *testing.T) {
	t.Parallel()

	none := contextapi.Config{
		Namespaces: nil, Units: nil, Deleted: nil, Collections: nil, Entries: nil, Writer: nil, Authenticator: nil,
		Authorizer: nil, Sessions: nil, Logger: nil,
	}
	if _, err := contextapi.NewHandler(none); !errors.Is(err, contextapi.ErrMissingDependency) {
		t.Errorf("NewHandler with no dependency: %v, want ErrMissingDependency", err)
	}
}
