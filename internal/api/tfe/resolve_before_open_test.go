package tfe_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// openCountingWorkspaces serves the stub workspace. Its ReadState is the call
// that opens sealed outputs in the real service, so it counts each call and
// fails the way the real service fails for a sealed version. Its
// StateVersionWorkspace is the lookup that opens nothing.
type openCountingWorkspaces struct {
	stubWorkspaces

	opens *atomic.Int32
}

// The state version IDs that openCountingWorkspaces knows. Each known ID
// belongs to ws-test123.
const (
	svDecryptFault  = "sv-decrypt123"
	svKeyStoreFault = "sv-keystore123"
	svPending       = "sv-pending123"
	// svOtherNamespace belongs to the workspace wsOtherNamespace of the
	// namespace "other", and has a decrypt fault.
	svOtherNamespace = "sv-nsb123"
	wsOtherNamespace = "ws-nsb123"
)

func (o openCountingWorkspaces) ReadState(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	o.opens.Add(1)

	switch id.String() {
	case svDecryptFault, svOtherNamespace:
		return workspace.StateVersion{}, workspace.ErrSealedContent
	case svKeyStoreFault:
		return workspace.StateVersion{}, errTestInternal
	default:
		return o.stubWorkspaces.ReadState(ctx, id)
	}
}

func (o openCountingWorkspaces) StateVersionWorkspace(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.ID, error) {
	switch id.String() {
	case svDecryptFault, svKeyStoreFault:
		return workspace.ID("ws-test123"), nil
	case svOtherNamespace:
		return workspace.ID(wsOtherNamespace), nil
	case svPending:
		return "", workspace.ErrStateVersionNotFound
	default:
		return locateVia(ctx, o.stubWorkspaces.ReadState, id)
	}
}

// GetByID serves the workspace of namespace "other" and, for any other ID,
// the stub workspaces of namespace "acme".
func (o openCountingWorkspaces) GetByID(ctx context.Context, id workspace.ID) (workspace.Workspace, error) {
	if id.String() != wsOtherNamespace {
		return o.stubWorkspaces.GetByID(ctx, id)
	}

	ws, err := o.stubWorkspaces.GetByID(ctx, workspace.ID("ws-test123"))
	if err != nil {
		return workspace.Workspace{}, err
	}

	other, err := namespace.ParseName("other")
	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("parse namespace: %w", err)
	}

	ws.ID = id
	ws.Ref.Namespace = other

	return ws, nil
}

func newOpenCountingHandler(t *testing.T, authz identity.Authorizer) (*tfe.Handler, *atomic.Int32) {
	t.Helper()

	opens := &atomic.Int32{}
	ws := openCountingWorkspaces{stubWorkspaces: stubWorkspaces{}, opens: opens}

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     authz,
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     ws,
		OutputChecker:  newTestOutputChecker(t, authz, ws),
		BaseURL:        baseurl.Resolver{},
		Audit:          newTestAudit(),
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler, opens
}

// resolverRoute is one route whose resolver maps a state version ID, or an
// output ID, to a unit.
type resolverRoute struct {
	method   string
	template string
}

func (r resolverRoute) String() string { return r.method + " " + r.template }

// resolverRoutes derives, from the route table of the handler, every route
// whose Resolve is resolveStateVersionID or resolveStateOutputID. A new route
// with one of these resolvers joins the hostile tables without an edit.
func resolverRoutes(t *testing.T, handler *tfe.Handler) []resolverRoute {
	t.Helper()

	var out []resolverRoute

	for _, route := range handler.Routes() {
		if route.Resolve == nil {
			continue
		}

		fn := runtime.FuncForPC(reflect.ValueOf(route.Resolve).Pointer())
		if fn == nil {
			t.Fatalf("route %s %s: no function name for its resolver", route.Method, route.Path)
		}

		if name := fn.Name(); strings.Contains(name, ".resolveStateVersionID") ||
			strings.Contains(name, ".resolveStateOutputID") {
			out = append(out, resolverRoute{method: route.Method, template: route.Path})
		}
	}

	return out
}

// request builds the request of the route for the state version ID id. The
// output route takes the output ID "<id>.greeting".
func (r resolverRoute) request(t *testing.T, id string) *http.Request {
	t.Helper()

	target := strings.NewReplacer(
		"{state_version_id}", id,
		"{output_id}", id+".greeting",
		"{address}", "terraform_data.smoke",
	).Replace(r.template)
	if strings.Contains(target, "{") {
		t.Fatalf("route %s has a path value that the test does not fill", r)
	}

	if strings.HasSuffix(target, "/diff") {
		target += "?from=sv-test123"
	}

	req := httptest.NewRequestWithContext(t.Context(), r.method, target, http.NoBody)
	req.Header.Set("Authorization", "Bearer test-token")

	return req
}

// TestResolverRoutesAreListed fails when the derivation from the route table
// loses a route that is known to resolve a state version or an output, for
// example when a resolver is renamed. The hostile tables below iterate over
// the derived list, so an empty or short list would hide a route.
func TestResolverRoutesAreListed(t *testing.T) {
	t.Parallel()

	handler, _ := newOpenCountingHandler(t, stubAuthorizer{})

	got := map[string]bool{}
	for _, route := range resolverRoutes(t, handler) {
		got[route.String()] = true
	}

	for _, want := range []string{
		"GET /api/v2/state-versions/{state_version_id}",
		"GET /api/v2/state-versions/{state_version_id}/download",
		"GET /api/v2/state-versions/{state_version_id}/resources",
		"GET /api/v2/state-versions/{state_version_id}/resources/{address}",
		"GET /api/v2/state-versions/{state_version_id}/diff",
		"GET /api/v2/state-version-outputs/{output_id}",
		"POST /api/v2/state-versions/{state_version_id}/actions/restore",
		"POST /api/v2/state-versions/{state_version_id}/actions/reveal",
	} {
		if !got[want] {
			t.Errorf("route %q is not among the routes with a state version resolver", want)
		}
	}
}

// TestStateVersionRoutesAuthorizeBeforeOpeningOutputs proves, for a caller
// without any right, that no route with a state version resolver opens a
// sealed output, and that the answer for each version that cannot be opened
// is the answer for an unknown ID: same status, same body, same headers. It
// fails when the resolver reads the version through a call that opens
// outputs.
func TestStateVersionRoutesAuthorizeBeforeOpeningOutputs(t *testing.T) {
	t.Parallel()

	handler, opens := newOpenCountingHandler(t, stubAuthorizer{})

	t.Cleanup(func() {
		if n := opens.Load(); n != 0 {
			t.Errorf("the server opened outputs %d times before authorization, want 0", n)
		}
	})

	routes := resolverRoutes(t, handler)
	if len(routes) == 0 {
		t.Fatal("no route with a state version resolver")
	}

	for _, route := range routes {
		t.Run(route.String(), func(t *testing.T) {
			t.Parallel()

			reference := httptest.NewRecorder()
			handler.ServeHTTP(reference, route.request(t, "sv-unknown123"))

			if reference.Code != http.StatusNotFound {
				t.Fatalf("unknown ID answered %d, want 404", reference.Code)
			}

			for _, id := range []string{
				"sv-test123", svDecryptFault, svKeyStoreFault, svPending, "sv-second-unknown123", "not-an-id", "sv-%00",
			} {
				t.Run(id, func(t *testing.T) {
					t.Parallel()

					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, route.request(t, id))

					assertSameAnswer(t, reference, rec)
				})
			}
		})
	}
}

// TestStateVersionRoutesAuthorizeTheNamespaceBeforeOpeningOutputs proves that
// the object that the resolver returns carries the namespace of the
// workspace. The caller holds every unit right on the units of namespace
// acme and none elsewhere. The version sv-nsb123 belongs to a workspace of
// namespace other and has a decrypt fault. Each route must answer for it
// exactly as for an unknown ID, with 0 opens. A control request for a
// version of acme proves that the authorizer does allow the caller there.
func TestStateVersionRoutesAuthorizeTheNamespaceBeforeOpeningOutputs(t *testing.T) {
	t.Parallel()

	handler, opens := newOpenCountingHandler(t, namespaceAuthorizer{namespace: "acme"})

	t.Run("control", func(t *testing.T) {
		t.Parallel()

		control, controlOpens := newOpenCountingHandler(t, namespaceAuthorizer{namespace: "acme"})

		rec := httptest.NewRecorder()
		control.ServeHTTP(rec, stateRequest(t, "/api/v2/state-versions/sv-test123"))

		if rec.Code != http.StatusOK {
			t.Errorf("a version of the allowed namespace answered %d, want 200", rec.Code)
		}

		if controlOpens.Load() == 0 {
			t.Error("the control read opened no output: the counter does not count")
		}
	})

	t.Cleanup(func() {
		if n := opens.Load(); n != 0 {
			t.Errorf("the server opened outputs %d times for a version of another namespace, want 0", n)
		}
	})

	for _, route := range resolverRoutes(t, handler) {
		t.Run(route.String(), func(t *testing.T) {
			t.Parallel()

			reference := httptest.NewRecorder()
			handler.ServeHTTP(reference, route.request(t, "sv-unknown123"))

			if reference.Code != http.StatusNotFound {
				t.Fatalf("unknown ID answered %d, want 404", reference.Code)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, route.request(t, svOtherNamespace))

			assertSameAnswer(t, reference, rec)
		})
	}
}

// namespaceAuthorizer allows every unit action of the caller on a unit of
// one namespace, and denies every other object. It lets the namespace of the
// object be the only thing that decides, for each route.
type namespaceAuthorizer struct {
	namespace string
}

func (n namespaceAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	allowed := []identity.Action{
		identity.ActionUnitRead, identity.ActionUnitReadOutputs,
		identity.ActionUnitReadSecrets, identity.ActionUnitWrite,
	}
	if obj.IsUnit() && obj.Namespace().String() == n.namespace && slices.Contains(allowed, action) {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

// ListObjects gives an administrator every object and any other principal none.
func (namespaceAuthorizer) ListObjects(
	_ context.Context, p identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// assertSameAnswer fails when got differs from want in status, body or
// headers.
func assertSameAnswer(t *testing.T, want, got *httptest.ResponseRecorder) {
	t.Helper()

	if got.Code != want.Code {
		t.Errorf("status = %d, want %d", got.Code, want.Code)
	}

	if got.Body.String() != want.Body.String() {
		t.Errorf("body = %q, want %q", got.Body.String(), want.Body.String())
	}

	for name, values := range want.Header() {
		if g := got.Header().Values(name); len(g) != len(values) || (len(g) > 0 && g[0] != values[0]) {
			t.Errorf("header %s = %v, want %v", name, g, values)
		}
	}

	if len(got.Header()) != len(want.Header()) {
		t.Errorf("headers = %v, want %v", got.Header(), want.Header())
	}
}
