package tfe_test

import (
	"context"
	"crypto/md5" //nolint:gosec // The test fixture uses MD5 as the protocol does.
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/api/tfe"
	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/token"
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
	return []string{
		"GET /.well-known/terraform.json",
		"GET /api/v2/ping",
		"GET /api/v2/test-public",
	}
}

// productionPublicPatterns lists the public routes of the production table:
// every public route without a test-only path.
func productionPublicPatterns() []string {
	return []string{
		"GET /.well-known/terraform.json",
		"GET /api/v2/ping",
	}
}

// stubAuthenticator denies every token. The declaration tests send no token,
// so it never runs.
type stubAuthenticator struct{}

func (stubAuthenticator) AuthenticateToken(_ context.Context, _ string) (identity.Principal, error) {
	return identity.Principal{}, identity.ErrUnauthenticated
}

// tokenAuthenticator signs in exactly the test token.
type tokenAuthenticator struct{}

func (tokenAuthenticator) AuthenticateToken(_ context.Context, raw string) (identity.Principal, error) {
	if raw != "test-token" {
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

// stubWorkspaces serves one workspace, acme/web with ID ws-test123, for
// every workspace route. Unknown names and IDs answer workspace.ErrNotFound
// and ws-locked answers workspace.ErrLocked, so the error tests can prove
// the 404 and 409 documents through HTTP. Create answers a new workspace, or
// workspace.ErrAlreadyExists for the existing web name.
type stubWorkspaces struct{}

func (stubWorkspaces) GetWorkspaceByID(_ context.Context, id workspace.ID) (workspace.Workspace, error) {
	return stubWorkspaceByID(id)
}

func (stubWorkspaces) GetByID(_ context.Context, id workspace.ID) (workspace.Workspace, error) {
	return stubWorkspaceByID(id)
}

func (stubWorkspaces) Get(_ context.Context, ref workspace.Ref) (workspace.Workspace, error) {
	switch {
	case ref.Namespace.String() == "acme" && ref.Name.String() == "web":
		return stubWorkspaceByID(workspace.ID("ws-test123"))
	case ref.Namespace.String() == "acme" && ref.Name.String() == "empty":
		return stubWorkspaceByID(workspace.ID("ws-empty123"))
	default:
		return workspace.Workspace{}, workspace.ErrNotFound
	}
}

func (stubWorkspaces) Create(_ context.Context, ref workspace.Ref) (workspace.Workspace, error) {
	if ref.Namespace.String() != "acme" {
		return workspace.Workspace{}, namespace.ErrNotFound
	}

	if ref.Name.String() == "web" {
		return workspace.Workspace{}, workspace.ErrAlreadyExists
	}

	ns, err := namespace.ParseName(ref.Namespace.String())
	if err != nil {
		return workspace.Workspace{}, err
	}

	name, err := workspace.ParseName(ref.Name.String())
	if err != nil {
		return workspace.Workspace{}, err
	}

	return workspace.Workspace{
		CreatedAt: stubWorkspaceTime(),
		UpdatedAt: stubWorkspaceTime(),
		Lock:      workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		ID:        workspace.ID("ws-" + name.String()),
		Ref:       workspace.Ref{Namespace: ns, Name: name},
		Revision:  1,
	}, nil
}

func (stubWorkspaces) List(_ context.Context, ns namespace.Name) ([]workspace.Workspace, error) {
	if ns.String() != "acme" {
		return nil, nil
	}

	ws, err := stubWorkspaceByID(workspace.ID("ws-test123"))
	if err != nil {
		return nil, err
	}

	return []workspace.Workspace{ws}, nil
}

func (stubWorkspaces) Lock(
	_ context.Context, id workspace.ID, holder, _ string,
) (workspace.Workspace, error) {
	ws, err := stubWorkspaceByID(id)
	if err != nil {
		return workspace.Workspace{}, err
	}

	ws.Lock = workspace.Lock{LockedAt: stubWorkspaceTime(), ID: "lock-test", Holder: holder, Reason: ""}

	return ws, nil
}

func (stubWorkspaces) Unlock(_ context.Context, id workspace.ID, _ string) (workspace.Workspace, error) {
	ws, err := stubWorkspaceByID(id)
	if err != nil {
		return workspace.Workspace{}, err
	}

	ws.Lock = workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}

	return ws, nil
}

func (stubWorkspaces) ForceUnlock(_ context.Context, id workspace.ID) (workspace.Workspace, error) {
	ws, err := stubWorkspaceByID(id)
	if err != nil {
		return workspace.Workspace{}, err
	}

	ws.Lock = workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}

	return ws, nil
}

func (stubWorkspaces) Current(_ context.Context, id workspace.ID) (workspace.StateVersion, error) {
	if id.String() != "ws-test123" {
		return workspace.StateVersion{}, workspace.ErrNoState
	}

	return stubStateVersion(), nil
}

// BeginState declares a pending version with a fresh capability. The stub
// ignores the lock and echoes the declared values: route tests prove
// authorization, not the state rules.
func (stubWorkspaces) BeginState(
	_ context.Context, id workspace.ID, holder string, in workspace.NewState,
) (workspace.PendingState, error) {
	upload, err := token.New()
	if err != nil {
		return workspace.PendingState{}, err
	}

	var sum [16]byte

	if raw, err := hex.DecodeString(in.MD5); err == nil && len(raw) == len(sum) {
		copy(sum[:], raw)
	}

	return workspace.PendingState{
		Upload: upload,
		Version: workspace.StateVersion{
			CreatedAt:       stubWorkspaceTime(),
			FinalizedAt:     time.Time{},
			UploadExpiresAt: stubWorkspaceTime().Add(time.Hour),
			Upload:          token.Digest{},
			ID:              workspace.StateVersionID("sv-new123"),
			Workspace:       id,
			Holder:          holder,
			LockID:          "lock-test",
			Lineage:         in.Lineage,
			Status:          workspace.StatusPending,
			Content:         "",
			Outputs:         in.Outputs,
			MD5:             sum,
			Serial:          in.Serial,
			Size:            0,
			Force:           in.Force,
		},
	}, nil
}

// Upload denies every content upload: route tests never reach it with a
// valid capability.
func (stubWorkspaces) Upload(
	_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, workspace.ErrUploadDenied
}

// DiscardJSONRendering denies every rendering upload: route tests never
// reach it with a valid capability.
func (stubWorkspaces) DiscardJSONRendering(
	_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
) error {
	return workspace.ErrUploadDenied
}

// stubStateContent is the state the stub download serves: a tfstate document
// with a greeting output.
func stubStateContent() string {
	return `{"version":4,"terraform_version":"1.11.0","serial":3,"lineage":"seed-lineage",` +
		`"outputs":{"greeting":{"value":"hello","type":"string"}},"resources":[]}`
}

// stubStateOutputs is the json-state-outputs document of the stub state: the
// flat map of outputs the CLI sends, with the greeting output and its cty
// value and type.
func stubStateOutputs() string {
	return `{"greeting":{"sensitive":false,"value":"hello","type":"string"}}`
}

// stubStateVersion is the finalized current state of ws-test123: serial 3 of
// the seed lineage, with the greeting output.
func stubStateVersion() workspace.StateVersion {
	content := stubStateContent()
	sum := md5.Sum([]byte(content)) //nolint:gosec // The test fixture uses MD5 as the protocol does.

	return workspace.StateVersion{
		CreatedAt:       stubWorkspaceTime(),
		FinalizedAt:     stubWorkspaceTime(),
		UploadExpiresAt: stubWorkspaceTime(),
		Upload:          token.Digest{},
		ID:              workspace.StateVersionID("sv-test123"),
		Workspace:       workspace.ID("ws-test123"),
		Holder:          "tester",
		LockID:          "lock-test",
		Lineage:         "seed-lineage",
		Status:          workspace.StatusFinalized,
		Content:         "tofu/ws-test123/sv-test123/seed",
		Outputs:         []byte(stubStateOutputs()),
		MD5:             sum,
		Serial:          3,
		Size:            int64(len(content)),
		Force:           false,
	}
}

func (stubWorkspaces) ReadState(
	_ context.Context, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	switch id.String() {
	case "sv-test123":
		return stubStateVersion(), nil
	default:
		return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
	}
}

func (stubWorkspaces) Open(_ context.Context, sv workspace.StateVersion) (io.ReadCloser, error) {
	if sv.ID.String() != "sv-test123" || sv.Status != workspace.StatusFinalized {
		return nil, workspace.ErrStateVersionNotFound
	}

	return io.NopCloser(strings.NewReader(stubStateContent())), nil
}

// stubWorkspaceTime stamps every stub workspace.
func stubWorkspaceTime() time.Time {
	return time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
}

// stubWorkspaceByID resolves workspace IDs: ws-test123 is acme/web with
// state, ws-empty123 is acme/empty without state. Unknown IDs answer
// workspace.ErrNotFound and ws-locked answers workspace.ErrLocked.
func stubWorkspaceByID(id workspace.ID) (workspace.Workspace, error) {
	var name string

	switch id.String() {
	case "ws-locked":
		return workspace.Workspace{}, workspace.ErrLocked
	case "ws-test123":
		name = "web"
	case "ws-empty123":
		name = "empty"
	default:
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	ns, err := namespace.ParseName("acme")
	if err != nil {
		return workspace.Workspace{}, err
	}

	parsed, err := workspace.ParseName(name)
	if err != nil {
		return workspace.Workspace{}, err
	}

	return workspace.Workspace{
		CreatedAt: stubWorkspaceTime(),
		UpdatedAt: stubWorkspaceTime(),
		Lock: workspace.Lock{
			LockedAt: time.Time{},
			ID:       "",
			Holder:   "",
			Reason:   "",
		},
		ID:       id,
		Ref:      workspace.Ref{Namespace: ns, Name: parsed},
		Revision: 1,
	}, nil
}

func newTestHandler(t *testing.T) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandlerWithRoutes(tfe.Config{
		Authenticator: stubAuthenticator{},
		Authorizer:    stubAuthorizer{},
		Logger:        slog.New(slog.DiscardHandler),
		Namespaces:    newTestNamespaces(t),
		Workspaces:    stubWorkspaces{},
		BaseURL:       baseurl.Resolver{},
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
		Namespaces:    newTestNamespaces(t),
		Workspaces:    stubWorkspaces{},
		BaseURL:       baseurl.Resolver{},
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
		Namespaces:    newTestNamespaces(t),
		Workspaces:    stubWorkspaces{},
		BaseURL:       baseurl.Resolver{},
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// stubNamespaceStore holds only the acme namespace, so the entitlement set
// answers 200 for acme and 404 for every other organization.
type stubNamespaceStore struct{}

func (stubNamespaceStore) CreateNamespace(_ context.Context, _ namespace.Namespace) error {
	return nil
}

func (stubNamespaceStore) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name.String() != "acme" {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return namespace.Namespace{
		CreatedAt:       time.Time{},
		Name:            name,
		HomeRegion:      "test-region",
		FailoverVersion: 1,
	}, nil
}

func (stubNamespaceStore) ListNamespaces(ctx context.Context) ([]namespace.Namespace, error) {
	ns, err := stubNamespaceStore{}.GetNamespace(ctx, namespace.Name("acme"))
	if err != nil {
		return nil, err
	}

	return []namespace.Namespace{ns}, nil
}

// newTestNamespaces returns a namespace service over the stub store.
func newTestNamespaces(t *testing.T) *namespace.Service {
	t.Helper()

	svc, err := namespace.NewService(
		namespace.Config{Store: stubNamespaceStore{}, Now: nil, Region: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	return svc
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
		checkRoutesDeclareActions(t, newProductionHandler(t), productionPublicPatterns())
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

// validateRoute fails for a route that is neither public, authorized with
// actions and a resolver, nor an upload route with the capability scope.
func validateRoute(route tfe.Route, public map[string]bool) error {
	switch route.Scope {
	case tfe.ScopePublic:
		return validatePublicRoute(route, public)
	case tfe.ScopeAuthorized:
		return validateAuthorizedRoute(route, public)
	case tfe.ScopeUpload:
		return validateUploadRoute(route, public)
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

// validateUploadRoute fails for an upload route that is on the public list,
// that declares anything but unit:write, or that declares a resolver. The
// capability in the path stands in for unit:write granted at creation, so
// the Authorizer does not run and no resolver is needed.
func validateUploadRoute(route tfe.Route, public map[string]bool) error {
	pattern := route.Method + " " + route.Path

	if public[pattern] {
		return fmt.Errorf("%w: %q is on the public list but declares the upload scope", errInvalidRoute, pattern)
	}

	if len(route.Actions) != 1 || route.Actions[0] != identity.ActionUnitWrite {
		return fmt.Errorf(
			"%w: upload route %q declares %v, want exactly [unit:write]",
			errInvalidRoute,
			pattern,
			route.Actions,
		)
	}

	if route.Resolve != nil {
		return fmt.Errorf("%w: upload route %q declares a resolver", errInvalidRoute, pattern)
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
// and a JSON body where the route reads one. raw is sent as a Bearer
// Authorization header when not empty.
func routeRequest(t *testing.T, route tfe.Route, raw string) *http.Request {
	t.Helper()

	path := strings.NewReplacer(
		"{organization}", "acme",
		"{workspace}", "web",
		"{id}", "ws-test123",
		"{state_version_id}", "sv-test123",
		"{output_id}", "sv-test123.greeting",
		"{token}", "wrong-token",
	).Replace(route.Path)

	body := ""
	if route.Method == http.MethodPost {
		body = `{"data":{}}`
		if strings.HasSuffix(route.Path, "/workspaces") {
			body = `{"data":{"type":"workspaces","attributes":{"name":"fresh"}}}`
		}

		if strings.HasSuffix(route.Path, "/state-versions") {
			body = `{"data":{"type":"state-versions","attributes":{` +
				`"serial":4,"lineage":"seed-lineage","md5":"d41d8cd98f00b204e9800998ecf8427e"}}}`
		}
	}

	req := httptest.NewRequestWithContext(t.Context(), route.Method, path, strings.NewReader(body))

	if raw != "" {
		req.Header.Set("Authorization", "Bearer "+raw)
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

	checkRouteAuthenticationUnauthorized(t, route)
	checkRouteAuthenticationDenied(t, route)
	checkRouteAuthenticationAllowed(t, route)
}

// checkRouteAuthenticationUnauthorized proves one authorized route answers
// 401 without a token and with a wrong token.
func checkRouteAuthenticationUnauthorized(t *testing.T, route tfe.Route) {
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
}

// checkRouteAuthenticationDenied proves one authorized route answers 403
// when the Authorizer denies it.
func checkRouteAuthenticationDenied(t *testing.T, route tfe.Route) {
	t.Helper()

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
}

// checkRouteAuthenticationAllowed proves one authorized route serves when the
// Authorizer allows it: a creation answers 201 and every other route 200.
func checkRouteAuthenticationAllowed(t *testing.T, route tfe.Route) {
	t.Helper()

	t.Run("allowed", func(t *testing.T) {
		t.Parallel()

		allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

		rec := httptest.NewRecorder()
		allowed.ServeHTTP(rec, routeRequest(t, route, "test-token"))

		want := wantSuccessStatus(route)

		if rec.Code != want {
			t.Fatalf("allowed request answers %d, want %d", rec.Code, want)
		}

		if got := rec.Header().Get("Content-Type"); got != wantMediaType(route) {
			t.Fatalf("media type = %q, want %q", got, wantMediaType(route))
		}
	})
}

// wantSuccessStatus returns the success status of route: a creation answers
// 201 and every other route answers 200.
func wantSuccessStatus(route tfe.Route) int {
	if route.Method == http.MethodPost &&
		(strings.HasSuffix(route.Path, "/workspaces") || strings.HasSuffix(route.Path, "/state-versions")) {
		return http.StatusCreated
	}

	return http.StatusOK
}

// wantMediaType returns the media type route serves on success: the download
// route streams raw state as application/json, every other route answers
// JSON:API.
func wantMediaType(route tfe.Route) string {
	if route.Method == http.MethodGet && route.Path == "/api/v2/state-versions/{state_version_id}/download" {
		return "application/json"
	}

	return "application/vnd.api+json"
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

// errorWorkspaces fails every workspace use case with an internal error, so
// the 500 path stays covered now that the workspace service is required.
type errorWorkspaces struct{}

func (errorWorkspaces) Create(_ context.Context, _ workspace.Ref) (workspace.Workspace, error) {
	return workspace.Workspace{}, errTestInternal
}

func (errorWorkspaces) Get(_ context.Context, _ workspace.Ref) (workspace.Workspace, error) {
	return workspace.Workspace{}, errTestInternal
}

func (errorWorkspaces) GetByID(_ context.Context, _ workspace.ID) (workspace.Workspace, error) {
	return workspace.Workspace{}, errTestInternal
}

func (errorWorkspaces) List(_ context.Context, _ namespace.Name) ([]workspace.Workspace, error) {
	return nil, errTestInternal
}

func (errorWorkspaces) Lock(
	_ context.Context, _ workspace.ID, _, _ string,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, errTestInternal
}

func (errorWorkspaces) Unlock(
	_ context.Context, _ workspace.ID, _ string,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, errTestInternal
}

func (errorWorkspaces) ForceUnlock(_ context.Context, _ workspace.ID) (workspace.Workspace, error) {
	return workspace.Workspace{}, errTestInternal
}

func (errorWorkspaces) Current(_ context.Context, _ workspace.ID) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, errTestInternal
}

func (errorWorkspaces) BeginState(
	_ context.Context, _ workspace.ID, _ string, _ workspace.NewState,
) (workspace.PendingState, error) {
	return workspace.PendingState{}, errTestInternal
}

func (errorWorkspaces) Upload(
	_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, errTestInternal
}

func (errorWorkspaces) DiscardJSONRendering(
	_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
) error {
	return errTestInternal
}

func (errorWorkspaces) ReadState(
	_ context.Context, _ workspace.StateVersionID,
) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, errTestInternal
}

func (errorWorkspaces) Open(_ context.Context, _ workspace.StateVersion) (io.ReadCloser, error) {
	return nil, errTestInternal
}

// errTestInternal is the internal failure errorWorkspaces answers with.
var errTestInternal = errors.New("test internal failure")

// TestErrorHidesInternalDetail proves a 500 never returns an internal error
// text.
func TestErrorHidesInternalDetail(t *testing.T) {
	t.Parallel()

	handler, err := tfe.NewHandlerWithRoutes(tfe.Config{
		Authenticator: tokenAuthenticator{},
		Authorizer:    selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:        slog.New(slog.DiscardHandler),
		Namespaces:    newTestNamespaces(t),
		Workspaces:    errorWorkspaces{},
		BaseURL:       baseurl.Resolver{},
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

// TestDiscoveryAdvertisesTFE proves the discovery document advertises tfe.v2
// and tfe.v2.1 at /api/v2/ and nothing else: there is no login.v1 service.
func TestDiscoveryAdvertisesTFE(t *testing.T) {
	t.Parallel()

	handler := newProductionHandler(t)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/.well-known/terraform.json",
		http.NoBody,
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("discovery answers %d, want %d", rec.Code, http.StatusOK)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("media type = %q, want %q", got, "application/json")
	}

	var doc map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode discovery body: %v", err)
	}

	if len(doc) != 2 || doc["tfe.v2"] != "/api/v2/" || doc["tfe.v2.1"] != "/api/v2/" {
		t.Fatalf("discovery body = %v, want tfe.v2 and tfe.v2.1 at /api/v2/", doc)
	}
}

// TestPingSetsAPIVersion proves ping answers 200 with TFP-API-Version 2.5.
func TestPingSetsAPIVersion(t *testing.T) {
	t.Parallel()

	handler := newProductionHandler(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v2/ping", http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ping answers %d, want %d", rec.Code, http.StatusOK)
	}

	if got := rec.Header().Get("Tfp-Api-Version"); got != "2.5" {
		t.Fatalf("TFP-API-Version = %q, want %q", got, "2.5")
	}
}

// entitlementTestAttributes is the attributes object of an entitlement
// document in tests.
type entitlementTestAttributes struct {
	Operations bool `json:"operations"`
}

// entitlementTestData is the data object of an entitlement document in tests.
type entitlementTestData struct {
	Type       string                    `json:"type"`
	Attributes entitlementTestAttributes `json:"attributes"`
}

// entitlementTestDoc decodes an entitlement document in tests.
type entitlementTestDoc struct {
	Data entitlementTestData `json:"data"`
}

// TestEntitlementSetReportsLocal proves the entitlement set reports
// operations false, so OpenTofu runs plans locally.
func TestEntitlementSetReportsLocal(t *testing.T) {
	t.Parallel()

	allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/api/v2/organizations/acme/entitlement-set",
		http.NoBody,
	)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	allowed.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("entitlement set answers %d, want %d", rec.Code, http.StatusOK)
	}

	var doc entitlementTestDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode entitlement body: %v", err)
	}

	if doc.Data.Type != "entitlement-sets" {
		t.Fatalf("entitlement type = %q, want %q", doc.Data.Type, "entitlement-sets")
	}

	if doc.Data.Attributes.Operations {
		t.Fatal("entitlement operations is true, want false")
	}
}

// TestEntitlementSetUnknownOrganization proves an unknown organization
// answers 404 as a JSON:API error.
func TestEntitlementSetUnknownOrganization(t *testing.T) {
	t.Parallel()

	allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/api/v2/organizations/ghost/entitlement-set",
		http.NoBody,
	)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	allowed.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown organization answers %d, want %d", rec.Code, http.StatusNotFound)
	}

	checkJSONAPIError(t, rec, http.StatusNotFound)
}

// TestEntitlementSetDeclaresNamespaceRead proves the entitlement-set route
// declares namespace:read on the namespace object.
func TestEntitlementSetDeclaresNamespaceRead(t *testing.T) {
	t.Parallel()

	handler := newProductionHandler(t)

	const pattern = "GET /api/v2/organizations/{organization}/entitlement-set"

	var found *tfe.Route

	for _, route := range handler.Routes() {
		if route.Method+" "+route.Path == pattern {
			found = &route

			break
		}
	}

	if found == nil {
		t.Fatalf("route %q is not declared", pattern)
	}

	if len(found.Actions) != 1 || found.Actions[0] != identity.ActionNamespaceRead {
		t.Fatalf("route %q actions = %v, want [namespace:read]", pattern, found.Actions)
	}

	if found.Resolve == nil {
		t.Fatalf("route %q declares no resolver", pattern)
	}

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/api/v2/organizations/acme/entitlement-set",
		http.NoBody,
	)
	req.SetPathValue("organization", "acme")

	obj, err := found.Resolve(httptest.NewRecorder(), req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if obj.String() != "acme" || obj.IsUnit() {
		t.Fatalf("object = %q (unit %v), want namespace %q", obj.String(), obj.IsUnit(), "acme")
	}
}

// TestUnknownPathAnswersJSONAPI404 proves a path the table does not hold
// answers 404 as a JSON:API error from this adapter.
func TestUnknownPathAnswersJSONAPI404(t *testing.T) {
	t.Parallel()

	handler := newProductionHandler(t)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/api/v2/no-such-route",
		http.NoBody,
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path answers %d, want %d", rec.Code, http.StatusNotFound)
	}

	checkJSONAPIError(t, rec, http.StatusNotFound)
}
