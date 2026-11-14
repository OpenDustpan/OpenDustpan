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
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/identity/identitytest"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
)

// packageSources holds every Go file in this package, so TestNoMuxBypass can
// prove that no production file reaches the mux without recording.
//
//go:embed *.go
var packageSources embed.FS

// publicPatterns lists the routes that need no sign-in: service discovery,
// capabilities and health. Every other route must declare its actions in the
// route table. GET /health/live and GET /health/ready are served by the root
// handler, not by the Pulumi CLI handler, and are listed here so the public
// surface stays in one place.
func publicPatterns() []string {
	return []string{"GET /api/capabilities", "GET /health/live", "GET /health/ready"}
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

func (stubNamespaceStore) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	return nil, nil
}

func (stubNamespaceStore) Ping(_ context.Context) error {
	return nil
}

// homedNamespaceStore answers every namespace whose primary installation is the test installation, so
// stack writes pass the primary-installation check.
type homedNamespaceStore struct{}

func (homedNamespaceStore) CreateNamespace(_ context.Context, _ namespace.Namespace) error {
	return nil
}

func (homedNamespaceStore) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	var out namespace.Namespace

	out.ID = namespace.ID("ns-" + name.String())
	out.Name = name
	out.PrimaryInstallation = "test-region"

	return out, nil
}

func (homedNamespaceStore) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.Time{},
		Name:                "acme",
		PrimaryInstallation: "test-region",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	return []namespace.Namespace{ns}, nil
}

func (homedNamespaceStore) Ping(_ context.Context) error {
	return nil
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

func (stubStackStore) DeleteStack(
	_ context.Context,
	_ stack.Ref,
	_ namespace.Fence,
	_ stack.DeleteGuard,
	_ inventory.Deletion,
) error {
	return stack.ErrNotFound
}

func (stubStackStore) ForceDeleteStack(
	_ context.Context,
	_ stack.Ref,
	_ namespace.Fence,
	_ inventory.Deletion,
) error {
	return stack.ErrNotFound
}

// RestoreStack implements stack.Store; this fake holds no deleted stack.
func (stubStackStore) RestoreStack(
	_ context.Context, _ stack.ID, _ stack.Name, _ namespace.Fence, _ audit.Entry,
) (stack.Ref, error) {
	return stack.Ref{}, stack.ErrNotFound
}

// PurgeStack implements stack.Store; this fake holds no deleted stack.
func (stubStackStore) PurgeStack(_ context.Context, _ stack.ID, _ namespace.Fence, _ time.Time, _ audit.Entry) error {
	return stack.ErrNotFound
}

func (stubStackStore) ListStacks(_ context.Context, _ stack.Filter) ([]stack.Stack, error) {
	return nil, nil
}

// QueryStacks implements stack.Store over the lists of ListStacks.
func (stubStackStore) QueryStacks(ctx context.Context, q stack.PageQuery) ([]stack.Stack, error) {
	return stack.ListPage(ctx, stubStackStore{}.ListStacks, q)
}

func (stubStackStore) RenameStack(
	_ context.Context, _ stack.Ref, _ stack.Name, _ namespace.Fence, _ audit.Entry,
) error {
	return stack.ErrNotFound
}

func (stubStackStore) UpdateStackTags(
	_ context.Context, _ stack.Ref, _ map[string]string, _ time.Time, _ namespace.Fence,
) error {
	return nil
}

// taggedStack is one UpdateStackTags call: the stack and its new tags.
type taggedStack struct {
	tags map[string]string
	ref  stack.Ref
}

// renamedStack records one rename call: the old reference, the new name and
// the audit entry that the service passed to the store.
type renamedStack struct {
	old   stack.Ref
	new   stack.Name
	entry audit.Entry
}

// recordingStackStore answers every Get with a stack for the requested
// reference and records every Create and Rename. It lets the enforcement tests prove
// that allowed requests reach the service.
type recordingStackStore struct {
	placed    []stack.Placement
	placeErr  error
	tags      []taggedStack
	created   []stack.Stack
	renamed   []renamedStack
	deleted   []stack.Ref
	forced    []stack.Ref
	entries   []audit.Entry
	deleteErr error
}

func (s *recordingStackStore) CreateStack(_ context.Context, st stack.Stack, _ namespace.Fence) error {
	s.created = append(s.created, st)

	return nil
}

// CreateStackPlaced records the stack and its placement, or fails as placeErr
// says and records nothing, as the transaction of a real store does.
func (s *recordingStackStore) CreateStackPlaced(
	_ context.Context, st stack.Stack, p stack.Placement, _ namespace.Fence,
) error {
	if s.placeErr != nil {
		return s.placeErr
	}

	s.created = append(s.created, st)
	s.placed = append(s.placed, p)

	return nil
}

func (s *recordingStackStore) RenameStack(
	_ context.Context, oldRef stack.Ref, newName stack.Name, _ namespace.Fence, e audit.Entry,
) error {
	if err := e.Validate(); err != nil {
		return err
	}

	s.renamed = append(s.renamed, renamedStack{old: oldRef, new: newName, entry: e})

	return nil
}

func (s *recordingStackStore) UpdateStackTags(
	_ context.Context, ref stack.Ref, tags map[string]string, _ time.Time, _ namespace.Fence,
) error {
	s.tags = append(s.tags, taggedStack{ref: ref, tags: tags})

	return nil
}

// unknownStackName is the one stack name that GetStack reports as not found.
const unknownStackName = "absent"

func (s *recordingStackStore) GetStack(_ context.Context, ref stack.Ref) (stack.Stack, error) {
	var out stack.Stack

	if string(ref.Stack) == unknownStackName {
		return out, stack.ErrNotFound
	}

	out.Ref = ref
	out.ID = "stk-test"
	out.NamespaceID = namespace.ID("ns-" + ref.Namespace.String())
	out.Tags = map[string]string{}

	return out, nil
}

func (s *recordingStackStore) DeleteStack(
	_ context.Context, ref stack.Ref, _ namespace.Fence, _ stack.DeleteGuard, d inventory.Deletion,
) error {
	e := d.Entry

	s.deleted = append(s.deleted, ref)
	s.entries = append(s.entries, e)

	return s.deleteErr
}

func (s *recordingStackStore) ForceDeleteStack(
	_ context.Context, ref stack.Ref, _ namespace.Fence, d inventory.Deletion,
) error {
	e := d.Entry

	s.forced = append(s.forced, ref)
	s.entries = append(s.entries, e)

	return s.deleteErr
}

// RestoreStack implements stack.Store; this fake holds no deleted stack.
func (*recordingStackStore) RestoreStack(
	_ context.Context, _ stack.ID, _ stack.Name, _ namespace.Fence, _ audit.Entry,
) (stack.Ref, error) {
	return stack.Ref{}, stack.ErrNotFound
}

// PurgeStack implements stack.Store; this fake holds no deleted stack.
func (*recordingStackStore) PurgeStack(
	_ context.Context,
	_ stack.ID,
	_ namespace.Fence,
	_ time.Time,
	_ audit.Entry,
) error {
	return stack.ErrNotFound
}

func (s *recordingStackStore) ListStacks(_ context.Context, _ stack.Filter) ([]stack.Stack, error) {
	return nil, nil
}

// QueryStacks implements stack.Store over the lists of ListStacks.
func (s *recordingStackStore) QueryStacks(ctx context.Context, q stack.PageQuery) ([]stack.Stack, error) {
	return stack.ListPage(ctx, s.ListStacks, q)
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

	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "tester",
		DisplayName:    "Tester",
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}, nil
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

// ListObjects gives an administrator every object and any other principal none.
func (stubAuthorizer) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
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

// ListObjects gives an administrator every object and any other principal none.
func (selectiveAuthorizer) ListObjects(
	_ context.Context,
	p identity.Principal,
	_ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
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

func newTestHandler(t *testing.T) *pulumi.Handler {
	t.Helper()

	return newTestHandlerWithBaseURL(t, baseurl.Resolver{})
}

func newTestHandlerWithBaseURL(t *testing.T, base baseurl.Resolver) *pulumi.Handler {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: stubNamespaceStore{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stacks, err := stack.NewService(
		stack.Config{
			Store:       stubStackStore{},
			Namespaces:  namespaces,
			Now:         nil,
			State:       nil,
			Retention:   0,
			ManualLocks: nil,
		},
	)
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	handler, err := pulumi.NewHandler(pulumi.Config{
		Placer:                  nil,
		StateGuard:              stategate.Off("the test keeps no gates"),
		SessionPolicy:           nil,
		DisablePulumiJournaling: false,
		Stacks:                  stacks,
		Updates:                 newUnusedUpdates(t, namespaces),
		Secrets:                 newTestSecrets(t, namespaces),
		Namespaces:              namespaces,
		Authenticator:           stubAuthenticator{},
		Authorizer:              stubAuthorizer{},
		Sessions:                nil,
		AddressGate:             nil,
		Logger:                  slog.New(slog.DiscardHandler),
		BaseURL:                 base,
		MaxBodyBytesInFlight:    0,
		Gate:                    nil,
		Audit:                   newTestAudit(),
		OnAuditFailure:          nil,
		CheckpointDeltaCutoff:   0,
		DenialLimiter:           nil,
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

	return newAuditedEnforcingHandler(t, authz, nil)
}

// newAuditedEnforcingHandler is newEnforcingHandler with an audit store for the
// entries of refused requests.
func newAuditedEnforcingHandler(
	t *testing.T, authz identity.Authorizer, entries audit.Store,
) (*pulumi.Handler, *recordingStackStore) {
	t.Helper()

	return newGuardedEnforcingHandler(t, authz, entries, stategate.Off("the test keeps no gates"))
}

// newGuardedEnforcingHandler is newAuditedEnforcingHandler with guard as the
// state guard of the handler.
func newGuardedEnforcingHandler(
	t *testing.T, authz identity.Authorizer, entries audit.Store, guard stategate.Guard,
) (*pulumi.Handler, *recordingStackStore) {
	t.Helper()

	return newPlacingHandler(t, authz, entries, guard, newFake().placer())
}

// newPlacingHandler is newGuardedEnforcingHandler with placer as the Placer of
// the handler.
func newPlacingHandler(
	t *testing.T, authz identity.Authorizer, entries audit.Store, guard stategate.Guard, placer *pulumi.Placer,
) (*pulumi.Handler, *recordingStackStore) {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: homedNamespaceStore{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stores := &recordingStackStore{
		placed:    nil,
		placeErr:  nil,
		created:   nil,
		renamed:   nil,
		tags:      nil,
		deleted:   nil,
		forced:    nil,
		entries:   nil,
		deleteErr: nil,
	}

	stacks, err := stack.NewService(
		stack.Config{Store: stores, Namespaces: namespaces, Now: nil, State: nil, Retention: 0, ManualLocks: nil},
	)
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	handler, err := pulumi.NewHandler(pulumi.Config{
		StateGuard:              guard,
		Placer:                  placer,
		SessionPolicy:           nil,
		DisablePulumiJournaling: false,
		Stacks:                  stacks,
		Updates:                 newEnforcingUpdates(t, namespaces),
		Secrets:                 newTestSecrets(t, namespaces),
		Namespaces:              namespaces,
		Authenticator:           tokenAuthenticator{},
		Authorizer:              authz,
		Sessions:                nil,
		AddressGate:             nil,
		Logger:                  slog.New(slog.DiscardHandler),
		BaseURL:                 baseurl.Resolver{},
		MaxBodyBytesInFlight:    0,
		Gate:                    nil,
		Audit:                   orTestAudit(entries),
		OnAuditFailure:          nil,
		DenialLimiter:           nil,
		CheckpointDeltaCutoff:   0,
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
		if pattern == "GET /health/live" || pattern == "GET /health/ready" {
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
	case pulumi.ScopeUpdateLease:
		checkLeaseRoute(t, route, public)
	default:
		t.Fatalf("route %q declares no scope", pattern)
	}

	checkRouteHandler(t, route)
}

// checkRouteHandler fails for a route with the wrong handler: a lease route
// sets only Lease, so it never runs without a parsed lease, and every other
// route sets only Serve.
func checkRouteHandler(t *testing.T, route pulumi.Route) {
	t.Helper()

	pattern := route.Method + " " + route.Path

	if route.Scope == pulumi.ScopeUpdateLease {
		if route.Lease == nil || route.Serve != nil {
			t.Fatalf("lease route %q: want Lease set and Serve nil", pattern)
		}

		return
	}

	if route.Serve == nil || route.Lease != nil {
		t.Fatalf("route %q: want Serve set and Lease nil", pattern)
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

// checkLeaseRoute fails for a lease route that is on the public list or
// that declares no action or no resolver. The exact action is asserted by
// TestUpdateRoutesDeclareUnitWrite: every lease route declares unit:write.
func checkLeaseRoute(t *testing.T, route pulumi.Route, public map[string]bool) {
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
		"{update}", "update0123456789abcdef",
		"{version}", "1",
		"{at}", "1760000000000",
		"{urn}", "urn:pulumi:dev::web::custom:resource:Type::res1",
	).Replace(route.Path)

	target := path

	var body *strings.Reader

	switch {
	case route.Method == http.MethodGet && route.Path == "/api/user/stacks":
		target += "?organization=acme"
	case route.Method == http.MethodPost && strings.HasSuffix(route.Path, "/rename"):
		body = strings.NewReader(`{"newName":"renamed","newProject":"web"}`)
	case route.Method == http.MethodPost:
		body = strings.NewReader(`{"stackName":"prod"}`)
	case route.Method == http.MethodPatch:
		body = strings.NewReader(`{"team":"e2e"}`)
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
// routes. Update routes need live update state, so they are skipped here
// and enforced by TestUpdateRoutesEnforce instead. Secrets routes need live
// stack state, so they are skipped here and enforced by
// TestSecretsEnforceAuthorization instead.
func TestRoutesEnforceAuthorization(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	for _, route := range handler.Routes() {
		if slices.Contains(updatePatterns(), route.Method+" "+route.Path) {
			continue
		}

		if slices.Contains(secretPatterns(), route.Method+" "+route.Path) {
			continue
		}

		if slices.Contains(statePatterns(), route.Method+" "+route.Path) {
			continue
		}

		if slices.Contains(revealPatterns(), route.Method+" "+route.Path) {
			continue
		}

		if slices.Contains(journalPatterns(), route.Method+" "+route.Path) {
			continue
		}

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

	if rec.Code != wantSuccess(route) {
		t.Fatalf("allowed request answers %d, want %d", rec.Code, wantSuccess(route))
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

// wantSuccess returns the status an allowed request to route answers: the
// final delete answers 204 with no content, every other route answers 200.
func wantSuccess(route pulumi.Route) int {
	if route.Method == http.MethodDelete {
		return http.StatusNoContent
	}

	return http.StatusOK
}

// TestRoutesAuthorizeDeclaredObject proves every authorized route asks for
// its declared actions on the object its resolver returns: the recording
// Authorizer sees each declared action on the resolved object, and denying
// that object answers 403. Update routes need live update state, so they are
// skipped here and covered by TestUpdateRoutesAuthorizeDeclaredObject
// instead. Secrets routes need live stack state, so they are skipped here
// and covered by TestSecretRoutesAuthorizeDeclaredObject instead.
func TestRoutesAuthorizeDeclaredObject(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	for _, route := range handler.Routes() {
		if slices.Contains(updatePatterns(), route.Method+" "+route.Path) {
			continue
		}

		if slices.Contains(secretPatterns(), route.Method+" "+route.Path) {
			continue
		}

		if slices.Contains(statePatterns(), route.Method+" "+route.Path) {
			continue
		}

		if slices.Contains(revealPatterns(), route.Method+" "+route.Path) {
			continue
		}

		if slices.Contains(journalPatterns(), route.Method+" "+route.Path) {
			continue
		}

		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			t.Parallel()
			checkRouteObject(t, route)
		})
	}
}

// checkRouteObject proves one route authorizes its declared actions on its
// resolved object.
func checkRouteObject(t *testing.T, route pulumi.Route) {
	t.Helper()

	if route.Scope == pulumi.ScopePublic {
		allowed, _ := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})

		rec := httptest.NewRecorder()
		allowed.ServeHTTP(rec, routeRequest(t, route, ""))

		if rec.Code != http.StatusOK {
			t.Fatalf("public route answers %d, want %d", rec.Code, http.StatusOK)
		}

		return
	}

	expected := resolveExpected(t, route)

	recorder := &recordingAuthorizer{calls: nil}
	allowed, _ := newEnforcingHandler(t, recorder)

	rec := httptest.NewRecorder()
	allowed.ServeHTTP(rec, routeRequest(t, route, "test-token"))

	if rec.Code != wantSuccess(route) {
		t.Fatalf("allowed request answers %d, want %d (%s)", rec.Code, wantSuccess(route), rec.Body.String())
	}

	for _, action := range route.Actions {
		if !slices.Contains(recorder.calls, recordedCall{action: action, object: expected}) {
			t.Fatalf("route never asks %s on %q (calls %v)", action, expected, recorder.calls)
		}
	}

	denied, _ := newEnforcingHandler(t, denyObjectAuthorizer{deny: expected})

	rec = httptest.NewRecorder()
	denied.ServeHTTP(rec, routeRequest(t, route, "test-token"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("request denied on %q answers %d, want %d", expected, rec.Code, http.StatusForbidden)
	}
}

// resolveExpected returns the wire form of the object the route resolves for
// its valid test request. Path placeholders are filled as routeRequest fills
// them, since resolvers read path values that only routing sets.
func resolveExpected(t *testing.T, route pulumi.Route) string {
	t.Helper()

	req := routeRequest(t, route, "test-token")
	req.SetPathValue("namespace", "acme")
	req.SetPathValue("project", "web")
	req.SetPathValue("stack", "prod")

	rec := httptest.NewRecorder()

	obj, err := route.Resolve(rec, req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	return obj.String()
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

// TestUpdateStackTagsThroughWrapper proves a tag update works end to end
// through the registration wrapper: the resolver authorizes the stack in the
// path and the handler replaces its tags with the sent map.
func TestUpdateStackTagsThroughWrapper(t *testing.T) {
	t.Parallel()

	handler, stores := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPatch,
		"/api/stacks/acme/web/prod/tags",
		strings.NewReader(`{"team":"e2e"}`),
	)
	req.Header.Set("Authorization", "token test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("update tags answers %d, want %d", rec.Code, http.StatusOK)
	}

	if len(stores.tags) != 1 {
		t.Fatalf("updated tags %d times, want 1", len(stores.tags))
	}

	got := stores.tags[0]
	if got.ref.Namespace.String() != "acme" || got.ref.Project.String() != "web" || got.ref.Stack.String() != "prod" {
		t.Fatalf("updated stack %v, want acme/web/prod", got.ref)
	}

	if len(got.tags) != 1 || got.tags["team"] != "e2e" {
		t.Fatalf("updated tags %v, want map[team:e2e]", got.tags)
	}
}

// TestUpdateStackTagsThroughWrapperInvalidBody proves an invalid tag body
// answers 400 and updates nothing.
func TestUpdateStackTagsThroughWrapperInvalidBody(t *testing.T) {
	t.Parallel()

	handler, stores := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPatch,
		"/api/stacks/acme/web/prod/tags",
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

	if len(stores.tags) != 0 {
		t.Fatalf("updated tags %d times, want 0", len(stores.tags))
	}
}

// TestRenameStackThroughWrapper proves a stack rename still works end to end
// through the registration wrapper: the wrapper authorizes the old unit and
// the handler authorizes the new unit before it renames.
func TestRenameStackThroughWrapper(t *testing.T) {
	t.Parallel()

	handler, stores := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/stacks/acme/web/prod/rename",
		strings.NewReader(`{"newName":"renamed","newProject":"web"}`),
	)
	req.Header.Set("Authorization", "token test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("rename answers %d, want %d", rec.Code, http.StatusOK)
	}

	if len(stores.renamed) != 1 {
		t.Fatalf("renamed %d stacks, want 1", len(stores.renamed))
	}

	got := stores.renamed[0]
	if got.old.Stack.String() != "prod" || got.new.String() != "renamed" {
		t.Fatalf("renamed stack %v to %q, want prod to renamed", got.old, got.new)
	}
}

// testRenameEntry returns a valid audit entry for a rename of ref in ns.
func testRenameEntry(ns namespace.Name, ref stack.Ref) audit.Entry {
	return audit.Entry{
		At:            time.UnixMilli(1_700_000_300_000).UTC(),
		Namespace:     ns,
		Subject:       "tester",
		IDPSubject:    "",
		TokenID:       "",
		Action:        string(identity.ActionUnitManage),
		Object:        "pulumi:" + ns.String() + "/" + ref.Project.String() + "/" + ref.Stack.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		PrincipalKind: "",
		JobID:         "",
		Reason:        "",
	}
}

// TestRenameStackWritesAuditEntry proves the rename route passes one change
// entry for unit:manage on the source unit to the service, with the time of
// the rename.
func TestRenameStackWritesAuditEntry(t *testing.T) {
	t.Parallel()

	handler, stores := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/stacks/acme/web/prod/rename",
		strings.NewReader(`{"newName":"renamed","newProject":"web"}`),
	)
	req.Header.Set("Authorization", "token test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("rename answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if len(stores.renamed) != 1 {
		t.Fatalf("renamed %d stacks, want 1", len(stores.renamed))
	}

	got := stores.renamed[0].entry
	if got.Kind != audit.KindChange || got.Action != string(identity.ActionUnitManage) ||
		got.Object != "pulumi:acme/web/prod" || got.Namespace != "acme" ||
		got.Subject != "tester" || got.Outcome != audit.OutcomeSuccess || got.At.IsZero() {
		t.Fatalf("audit entry = %+v, want a timed change by unit:manage on pulumi:acme/web/prod", got)
	}
}

// TestRenameStackThroughWrapperInvalid proves a rename rejects an invalid
// body, an invalid name and a project change with 400 and renames nothing.
func TestRenameStackThroughWrapperInvalid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "invalid JSON", body: `{`, want: "invalid JSON body"},
		{name: "invalid name", body: `{"newName":"../x"}`, want: "invalid project or stack name"},
		{
			name: "project change",
			body: `{"newName":"renamed","newProject":"other"}`,
			want: "invalid project or stack name",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler, stores := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				"/api/stacks/acme/web/prod/rename",
				strings.NewReader(tc.body),
			)
			req.Header.Set("Authorization", "token test-token")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("rename answers %d, want %d", rec.Code, http.StatusBadRequest)
			}

			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("rename answers %q, want %q", rec.Body.String(), tc.want)
			}

			if len(stores.renamed) != 0 {
				t.Fatalf("renamed %d stacks, want 0", len(stores.renamed))
			}
		})
	}
}

// TestRenameAuthorizesDestination proves a rename needs unit:manage on the
// destination as well as the source: with the source allowed but the new
// name denied it answers 403 and renames nothing, and with both allowed it
// renames.
func TestRenameAuthorizesDestination(t *testing.T) {
	t.Parallel()

	const (
		source = "pulumi:acme/web/prod"
		dest   = "pulumi:acme/web/renamed"
		body   = `{"newName":"renamed","newProject":"web"}`
	)

	// rename returns the status code and the number of stacks renamed.
	rename := func(t *testing.T, authz identity.Authorizer) (int, int) {
		t.Helper()

		handler, stores := newEnforcingHandler(t, authz)

		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/api/stacks/acme/web/prod/rename",
			strings.NewReader(body),
		)
		req.Header.Set("Authorization", "token test-token")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		return rec.Code, len(stores.renamed)
	}

	sourceOnly := identitytest.NewPolicyAuthorizer("tester", map[identity.Action][]string{
		identity.ActionUnitManage: {source},
	})

	if code, renamed := rename(t, sourceOnly); code != http.StatusForbidden {
		t.Fatalf("rename without destination grant answers %d, want %d", code, http.StatusForbidden)
	} else if renamed != 0 {
		t.Fatalf("renamed %d stacks, want 0", renamed)
	}

	both := identitytest.NewPolicyAuthorizer("tester", map[identity.Action][]string{
		identity.ActionUnitManage: {source, dest},
	})

	if code, renamed := rename(t, both); code != http.StatusOK {
		t.Fatalf("rename with both grants answers %d, want %d", code, http.StatusOK)
	} else if renamed != 1 {
		t.Fatalf("renamed %d stacks, want 1", renamed)
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
	}{
		{"namespace only", "/api/user/stacks?organization=acme"},
		{"namespace and project", "/api/user/stacks?organization=acme&project=web"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			obj, err := resolve(rec, listRequest(t, tc.target))
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}

			if obj.String() != "acme" || obj.IsUnit() {
				t.Fatalf("object = %q (unit %v), want namespace %q", obj.String(), obj.IsUnit(), "acme")
			}
		})
	}
}

// TestResolveListStacksWithoutOrganization proves that omitting organization
// resolves the caller itself, for the list across every readable namespace.
func TestResolveListStacksWithoutOrganization(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "GET /api/user/stacks")

	for _, target := range []string{"/api/user/stacks", "/api/user/stacks?project=web"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			obj, err := resolve(rec, listRequest(t, target))
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}

			if obj.String() != "@caller" || obj.IsUnit() || !obj.IsCaller() {
				t.Fatalf("object = %q (unit %v caller %v), want caller", obj.String(), obj.IsUnit(), obj.IsCaller())
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
		{"invalid organization", "/api/user/stacks?organization=../x", namespace.ErrInvalidName},
		{"invalid project", "/api/user/stacks?organization=acme&project=../x", stack.ErrInvalidName},
		{"invalid project without organization", "/api/user/stacks?project=../x", stack.ErrInvalidName},
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

// exportPanicsStore panics on any checkpoint read: the final delete must
// never load a deployment, so a delete test that reaches it fails.
type exportPanicsStore struct {
	stack.UpdateStore
	stack.EventStore
}

// LatestCheckpoint panics: the delete route must not read state.
func (exportPanicsStore) LatestCheckpoint(_ context.Context, _ stack.Ref) (stack.Checkpoint, error) {
	panic("delete must not export the checkpoint")
}

// CheckpointByVersion panics: the delete route must not read state.
func (exportPanicsStore) CheckpointByVersion(
	_ context.Context, _ stack.Ref, _ int64,
) (stack.Checkpoint, error) {
	panic("delete must not export the checkpoint")
}

// PutCheckpoint panics: the delete route never writes state.
func (exportPanicsStore) PutCheckpoint(
	_ context.Context, _ stack.Checkpoint, _ namespace.Fence,
) (bool, error) {
	panic("delete must not write a checkpoint")
}

// CheckpointByUpdate panics: the delete route must not read state.
func (exportPanicsStore) CheckpointByUpdate(
	_ context.Context, _ stack.Ref, _ stack.UpdateID,
) (stack.Checkpoint, error) {
	panic("delete must not read a checkpoint")
}

// ReferencedBlobKeys panics: the delete route must not read state.
func (exportPanicsStore) ReferencedBlobKeys(
	_ context.Context, _ stack.ID,
) ([]blob.Key, error) {
	panic("delete must not read checkpoint keys")
}

// unusedStackResolver panics: routes that never save checkpoints never
// resolve stacks.
type unusedStackResolver struct{}

func (unusedStackResolver) GetStack(_ context.Context, _ stack.Ref) (stack.Stack, error) {
	panic("GetStack is not used by this route")
}

// newDeleteHandler builds a Handler whose delete removes from stores.
// deleteErr is what the stack store answers. It returns the blob store too,
// so tests prove what a delete leaves behind.
func newDeleteHandler(
	t *testing.T, deleteErr error,
) (*pulumi.Handler, *recordingStackStore, *memBlob) {
	t.Helper()

	return newDeleteHandlerWithAudit(t, deleteErr, nil)
}

// newDeleteHandlerWithAudit builds the delete handler of newDeleteHandler
// with an audit store.
func newDeleteHandlerWithAudit(
	t *testing.T, deleteErr error, auditStore audit.Store,
) (*pulumi.Handler, *recordingStackStore, *memBlob) {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: homedNamespaceStore{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stores := &recordingStackStore{
		placed:    nil,
		placeErr:  nil,
		created:   nil,
		renamed:   nil,
		tags:      nil,
		deleted:   nil,
		forced:    nil,
		entries:   nil,
		deleteErr: deleteErr,
	}

	stacks, err := stack.NewService(
		stack.Config{Store: stores, Namespaces: namespaces, Now: nil, State: nil, Retention: 0, ManualLocks: nil},
	)
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	blobs := newMemBlob()

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:             secrettest.NewMemorySealer(),
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Updates:            exportPanicsStore{UpdateStore: nil, EventStore: nil},
		Tokens:             nil,
		Checkpoints:        exportPanicsStore{UpdateStore: nil, EventStore: nil},
		Events:             exportPanicsStore{UpdateStore: nil, EventStore: nil},
		Namespaces:         namespaces,
		Stacks:             unusedStackResolver{},
		Blobs:              blobs,
		Refusals:           stacktest.NewRefusals(),
		Now:                nil,
		Logger:             nil,
		MaxCheckpointBytes: 0,
		Journal:            nil,
		Replayer:           nil,
	})
	if err != nil {
		t.Fatalf("updates service: %v", err)
	}

	handler, err := pulumi.NewHandler(pulumi.Config{
		Placer:                  nil,
		StateGuard:              stategate.Off("the test keeps no gates"),
		SessionPolicy:           nil,
		DisablePulumiJournaling: false,
		Stacks:                  stacks,
		Updates:                 updates,
		Secrets:                 newTestSecrets(t, namespaces),
		Namespaces:              namespaces,
		Authenticator:           tokenAuthenticator{},
		Sessions:                nil,
		Authorizer:              selectiveAuthorizer{deny: map[identity.Action]bool{}},
		AddressGate:             nil,
		Logger:                  slog.New(slog.DiscardHandler),
		BaseURL:                 baseurl.Resolver{},
		MaxBodyBytesInFlight:    0,
		Gate:                    nil,
		Audit:                   orTestAudit(auditStore),
		OnAuditFailure:          nil,
		CheckpointDeltaCutoff:   0,
		DenialLimiter:           nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler, stores, blobs
}

func deleteRequest(t *testing.T, target string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, target, http.NoBody)
	req.Header.Set("Authorization", "token test-token")

	return req
}

func TestDeleteStack(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		target     string
		deleteErr  error
		want       int
		wantDelete bool
		wantForced bool
	}{
		{
			name:       "without force",
			target:     "/api/stacks/acme/web/prod",
			deleteErr:  nil,
			want:       http.StatusNoContent,
			wantDelete: true,
			wantForced: false,
		},
		{
			name:       "with force",
			target:     "/api/stacks/acme/web/prod?force=true",
			deleteErr:  nil,
			want:       http.StatusNoContent,
			wantDelete: true,
			wantForced: true,
		},
		{
			name:       "with force=1",
			target:     "/api/stacks/acme/web/prod?force=1",
			deleteErr:  nil,
			want:       http.StatusNoContent,
			wantDelete: true,
			wantForced: true,
		},
		{
			name:       "with force=false",
			target:     "/api/stacks/acme/web/prod?force=false",
			deleteErr:  nil,
			want:       http.StatusNoContent,
			wantDelete: true,
			wantForced: false,
		},
		{
			name:       "with force=TRUE",
			target:     "/api/stacks/acme/web/prod?force=TRUE",
			deleteErr:  nil,
			want:       http.StatusNoContent,
			wantDelete: true,
			wantForced: true,
		},
		{
			name:       "resources without force",
			target:     "/api/stacks/acme/web/prod",
			deleteErr:  stack.ErrHasResources,
			want:       http.StatusBadRequest,
			wantDelete: true,
			wantForced: false,
		},
		{
			name:       "resources with force still deletes",
			target:     "/api/stacks/acme/web/prod?force=true",
			deleteErr:  nil,
			want:       http.StatusNoContent,
			wantDelete: true,
			wantForced: true,
		},
		{
			name:       "missing stack",
			target:     "/api/stacks/acme/web/prod",
			deleteErr:  stack.ErrNotFound,
			want:       http.StatusNotFound,
			wantDelete: true,
			wantForced: false,
		},
		{
			name:       "running update",
			target:     "/api/stacks/acme/web/prod?force=true",
			deleteErr:  stack.ErrUpdateConflict,
			want:       http.StatusConflict,
			wantDelete: true,
			wantForced: true,
		},
		{
			name:       "invalid name",
			target:     "/api/stacks/acme/web/..%2Fx",
			deleteErr:  nil,
			want:       http.StatusBadRequest,
			wantDelete: false,
			wantForced: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler, stores, _ := newDeleteHandler(t, tc.deleteErr)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, deleteRequest(t, tc.target))

			if rec.Code != tc.want {
				t.Fatalf("delete answers %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}

			calls := len(stores.deleted) + len(stores.forced)
			if got := calls == 1; got != tc.wantDelete {
				t.Fatalf("delete called %d stores, want delete=%t", calls, tc.wantDelete)
			}

			if tc.wantDelete && (len(stores.forced) == 1) != tc.wantForced {
				t.Fatalf("force delete = %t, want %t", len(stores.forced) == 1, tc.wantForced)
			}
		})
	}
}

// TestDeleteStackWithResourcesMessage proves the refusal carries exactly the
// message the Pulumi CLI recognizes as a stack that still manages resources.
func TestDeleteStackWithResourcesMessage(t *testing.T) {
	t.Parallel()

	handler, _, _ := newDeleteHandler(t, stack.ErrHasResources)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, deleteRequest(t, "/api/stacks/acme/web/prod"))

	var got struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode error body: %v", err)
	}

	const want = "Bad Request: Stack still contains resources."
	if got.Message != want || got.Code != http.StatusBadRequest {
		t.Fatalf("error body = %+v, want code %d message %q", got, http.StatusBadRequest, want)
	}
}

// TestDeleteStackKeepsCheckpointBlobs proves a stack delete keeps the
// checkpoint blobs of the stack: a soft delete keeps the state for a restore
// until the final delete (ADR 47).
func TestDeleteStackKeepsCheckpointBlobs(t *testing.T) {
	t.Parallel()

	handler, _, blobs := newDeleteHandler(t, nil)

	key, err := blob.ParseKey("pulumi/stk-test/upd-test/3")
	if err != nil {
		t.Fatalf("parse blob key: %v", err)
	}

	if err := blobs.Put(t.Context(), key, strings.NewReader("checkpoint")); err != nil {
		t.Fatalf("write checkpoint blob: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, deleteRequest(t, "/api/stacks/acme/web/prod"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete answers %d, want %d (body %s)", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	if _, err := blobs.Get(t.Context(), key); err != nil {
		t.Fatalf("checkpoint blob after delete: %v, want it kept", err)
	}
}

// TestDeleteStackPassesChangeEntry proves a stack delete hands one audit
// entry of the kind change for the unit, stamped with the time of the delete,
// to the store, which appends it in the delete transaction (ADR 60, fail
// closed). A store that fails the append answers 500, and the handler then
// appends no entry of its own.
func TestDeleteStackPassesChangeEntry(t *testing.T) {
	t.Parallel()

	cases := []struct {
		deleteErr error
		name      string
		wantCode  int
	}{
		{name: "delete", deleteErr: nil, wantCode: http.StatusNoContent},
		{name: "refused delete", deleteErr: stack.ErrUpdateConflict, wantCode: http.StatusConflict},
		{name: "failed append", deleteErr: errAppendBoom, wantCode: http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			auditStore := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}
			handler, stores, _ := newDeleteHandlerWithAudit(t, tc.deleteErr, auditStore)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, deleteRequest(t, "/api/stacks/acme/web/prod"))

			if rec.Code != tc.wantCode {
				t.Fatalf("delete answers %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
			}

			if len(auditStore.entries) != 0 {
				t.Fatalf("entries appended by the handler = %d, want 0: the store appends", len(auditStore.entries))
			}

			checkPassedChangeEntry(t, stores.entries)
		})
	}
}

// errAppendBoom is the error of a store whose audit append fails.
var errAppendBoom = errors.New("append audit entry: boom")

// checkPassedChangeEntry checks that the store got exactly one entry: a
// change by unit:manage on pulumi:acme/web/prod with the time of the delete.
func checkPassedChangeEntry(t *testing.T, entries []audit.Entry) {
	t.Helper()

	if len(entries) != 1 {
		t.Fatalf("entries passed to the store = %d, want 1", len(entries))
	}

	got := entries[0]
	if got.Kind != audit.KindChange || got.Action != string(identity.ActionUnitManage) ||
		got.Object != "pulumi:acme/web/prod" || got.Namespace != "acme" ||
		got.Outcome != audit.OutcomeSuccess || got.At.IsZero() {
		t.Fatalf("audit entry = %+v, want a timed change by unit:manage on pulumi:acme/web/prod", got)
	}
}

// TestResolveRenameStack proves the rename route resolves the source unit in
// the path: the stack being renamed. The handler authorizes the destination
// unit in the body separately.
func TestResolveRenameStack(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "POST /api/stacks/{namespace}/{project}/{stack}/rename")

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/stacks/acme/web/prod/rename",
		strings.NewReader(`{"newName":"renamed","newProject":"web"}`),
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

// TestResolveUpdateStackTags proves the tags route resolves the unit in the
// path.
func TestResolveUpdateStackTags(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "PATCH /api/stacks/{namespace}/{project}/{stack}/tags")

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPatch,
		"/api/stacks/acme/web/prod/tags",
		strings.NewReader(`{"team":"e2e"}`),
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

// unusedUpdateStores satisfies the update ports for routes that never write
// updates. GetStack reads the active update, so ActiveUpdate answers none.
// Any other call panics, which a test would report.
type unusedUpdateStores struct {
	stack.UpdateStore
	stack.CheckpointStore
	stack.EventStore
}

// ActiveUpdate reports no running update.
func (unusedUpdateStores) ActiveUpdate(_ context.Context, _ stack.Ref) (stack.Update, error) {
	return stack.Update{}, stack.ErrUpdateNotFound
}

// newUnusedUpdates returns an Updates service for handlers whose tested routes
// never read or write updates.
func newUnusedUpdates(t *testing.T, namespaces *namespace.Service) *stack.Updates {
	t.Helper()

	stores := unusedUpdateStores{UpdateStore: nil, CheckpointStore: nil, EventStore: nil}

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:             secrettest.NewMemorySealer(),
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Updates:            stores,
		Tokens:             nil,
		Checkpoints:        stores,
		Events:             stores,
		Namespaces:         namespaces,
		Stacks:             unusedStackResolver{},
		Blobs:              newMemBlob(),
		Refusals:           stacktest.NewRefusals(),
		Now:                nil,
		Logger:             nil,
		MaxCheckpointBytes: 0,
		Journal:            nil,
		Replayer:           nil,
	})
	if err != nil {
		t.Fatalf("updates service: %v", err)
	}

	return updates
}

// emptyCheckpointStore reports no state and no running update for every
// stack, so the final delete of a stack without resources proceeds and stack
// reads answer an empty active update. Any other call panics, which a test
// would report.
type emptyCheckpointStore struct {
	stack.UpdateStore
	stack.EventStore
}

// ActiveUpdate reports no running update.
func (emptyCheckpointStore) ActiveUpdate(_ context.Context, _ stack.Ref) (stack.Update, error) {
	return stack.Update{}, stack.ErrUpdateNotFound
}

func (emptyCheckpointStore) PutCheckpoint(
	_ context.Context, _ stack.Checkpoint, _ namespace.Fence,
) (bool, error) {
	panic("PutCheckpoint is not used by the delete route")
}

func (emptyCheckpointStore) LatestCheckpoint(
	_ context.Context, _ stack.Ref,
) (stack.Checkpoint, error) {
	return stack.Checkpoint{}, stack.ErrNoState
}

func (emptyCheckpointStore) CheckpointByVersion(
	_ context.Context, _ stack.Ref, _ int64,
) (stack.Checkpoint, error) {
	return stack.Checkpoint{}, stack.ErrNoState
}

func (emptyCheckpointStore) CheckpointByUpdate(
	_ context.Context, _ stack.Ref, _ stack.UpdateID,
) (stack.Checkpoint, error) {
	return stack.Checkpoint{}, stack.ErrNoState
}

func (emptyCheckpointStore) ReferencedBlobKeys(
	_ context.Context, _ stack.ID,
) ([]blob.Key, error) {
	return nil, nil
}

// newEnforcingUpdates returns an Updates service whose export finds no state,
// for handlers whose tested routes delete stacks without resources.
func newEnforcingUpdates(t *testing.T, namespaces *namespace.Service) *stack.Updates {
	t.Helper()

	stores := emptyCheckpointStore{UpdateStore: nil, EventStore: nil}

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:             secrettest.NewMemorySealer(),
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Updates:            stores,
		Tokens:             nil,
		Checkpoints:        stores,
		Events:             stores,
		Namespaces:         namespaces,
		Stacks:             unusedStackResolver{},
		Blobs:              newMemBlob(),
		Refusals:           stacktest.NewRefusals(),
		Now:                nil,
		Logger:             nil,
		MaxCheckpointBytes: 0,
		Journal:            nil,
		Replayer:           nil,
	})
	if err != nil {
		t.Fatalf("updates service: %v", err)
	}

	return updates
}

// SetOverrides is not used by this fake.
func (stubNamespaceStore) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}
