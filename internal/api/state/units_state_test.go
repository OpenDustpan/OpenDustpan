package state_test

// Hostile tests of the handler layer of the masked read of the state of a
// unit. Each guard of the handler has one table here, and each case sends its
// input to the handler as if every other layer failed: the fake services
// answer what the test sets, and do not check the input again.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/state"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// The wire forms of the units of the tests.
const (
	stackUnit     = "pulumi:acme/web/dev"
	workspaceUnit = "opentofu:acme/network"
)

// TestRouteTable proves the rules of the route table: the table and the mux
// list the same pattern, the route is authorized, resolves its unit, has a
// handler, and declares unit:read, and its operation lists namespace:read on
// the namespace and unit:read on the unit.
func TestRouteTable(t *testing.T) {
	t.Parallel()

	e := newEnv(t, setup{authz: readerOf(), units: newUnits(), limiter: nil, timeout: 0})

	routes := e.handler.Routes()
	if len(routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(routes))
	}

	rt := routes[0]
	if rt.Scope == 0 || rt.Resolve == nil || rt.Serve == nil ||
		!slices.Equal(rt.Actions, []identity.Action{identity.ActionUnitRead}) {
		t.Fatalf("route %s is not complete: %v", rt.Pattern(), rt.Actions)
	}

	pattern := http.MethodGet + " " + apidesc.UnitsStatePath
	if !slices.Equal(e.handler.RegisteredPatterns(), []string{pattern}) {
		t.Fatalf("registered = %v, want %q", e.handler.RegisteredPatterns(), pattern)
	}
}

// TestOperationsDeclareTheActions proves the operation of the route lists
// namespace:read on the namespace and unit:read on the unit.
func TestOperationsDeclareTheActions(t *testing.T) {
	t.Parallel()

	e := newEnv(t, setup{authz: readerOf(), units: newUnits(), limiter: nil, timeout: 0})

	ops := e.handler.Operations()
	if len(ops) != 1 {
		t.Fatalf("operations = %d, want 1", len(ops))
	}

	if err := ops[0].Validate(); err != nil {
		t.Fatalf("operation: %v", err)
	}

	want := map[string]string{"namespace:read": apidesc.KindNamespace, "unit:read": apidesc.KindUnit}
	if !slices.Equal(ops[0].Actions, []string{"namespace:read", "unit:read"}) ||
		len(ops[0].ActionObjects) != len(want) {
		t.Fatalf("actions = %v %v, want %v", ops[0].Actions, ops[0].ActionObjects, want)
	}

	for action, kind := range want {
		if ops[0].ActionObjects[action] != kind {
			t.Errorf("object of %s = %q, want %q", action, ops[0].ActionObjects[action], kind)
		}
	}
}

// TestReadAnswersMarkers proves the read of each tool answers the markers and
// the value paths of the page, with an access token and unit:read on the unit.
func TestReadAnswersMarkers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, target, unit, version string
		markers                     int
	}{
		{name: "stack", target: stackURL, unit: stackUnit, version: "1", markers: 2},
		{name: "workspace", target: workspaceURL, unit: workspaceUnit, version: "sv-1", markers: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, setup{authz: readerOf(tc.unit), units: newUnits(), limiter: nil, timeout: 0})

			status, body := e.get(t, tc.target)
			if status != http.StatusOK {
				t.Fatalf("status = %d (%s), want 200", status, body)
			}

			page := decodePage(t, body)
			if page.Unit != tc.unit || page.Version != tc.version || len(page.Markers) != tc.markers ||
				len(page.ValuePaths) != tc.markers {
				t.Fatalf("page = %+v, want unit %s version %s with %d markers", page, tc.unit, tc.version, tc.markers)
			}
		})
	}
}

// decodePage decodes the body of a page and fails the test for a marker that
// is not a secret with a value path.
func decodePage(t *testing.T, body []byte) apidesc.UnitStatePage {
	t.Helper()

	var page apidesc.UnitStatePage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	for _, m := range page.Markers {
		if !m.Secret || m.Path == "" {
			t.Errorf("marker %+v, want a secret with a value path", m)
		}
	}

	return page
}

// TestReadDeniedWithoutGrant proves the authorization at the point of use: a
// caller with no grant on the unit, or with a grant on a sibling unit or on
// the same name in another namespace, gets 403 unit.forbidden. A caller that
// cannot read the namespace gets the 404 of an unknown unit. No refusal
// reaches the services.
func TestReadDeniedWithoutGrant(t *testing.T) {
	t.Parallel()

	otherNamespace := grants{"acme": {identity.ActionNamespaceRead}, "pulumi:team/web/dev": {identity.ActionUnitRead}}
	noNamespace := grants{stackUnit: {identity.ActionUnitRead}}

	cases := []struct {
		name   string
		authz  grants
		status int
		code   string
	}{
		{"no grant on the unit", readerOf(), http.StatusForbidden, errcode.UnitForbidden},
		{"grant on a sibling unit", readerOf("pulumi:acme/web/prod"), http.StatusForbidden, errcode.UnitForbidden},
		{"grant on the same name in another namespace", otherNamespace, http.StatusForbidden, errcode.UnitForbidden},
		{"grant on the unit of another tool", readerOf(workspaceUnit), http.StatusForbidden, errcode.UnitForbidden},
		{"no read of the namespace", noNamespace, http.StatusNotFound, errcode.UnitNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, setup{authz: tc.authz, units: newUnits(), limiter: nil, timeout: 0})

			status, body := e.get(t, stackURL)
			wantError(t, status, body, tc.status, tc.code)

			if e.units.count() != 0 {
				t.Fatalf("reads = %d, want 0: a refused caller must not reach the state", e.units.count())
			}

			if e.audit.count() == 0 {
				t.Fatal("audit entries = 0, want the entry of the refusal")
			}
		})
	}
}

// TestReadBoundaryRefusals proves the boundary check of the handler: a unit
// reference that does not parse, a version or a cursor that fails its
// allowlist, and a query name that the route does not read are each refused
// before any state is read, with an answer that repeats no part of the input.
func TestReadBoundaryRefusals(t *testing.T) {
	t.Parallel()

	const units = "/api/dustpan/v1/units/"

	long := strings.Repeat("a", 600)
	badPath := errcode.RequestInvalidPath

	cases := []struct {
		name, target, code, echo string
	}{
		{"a namespace, not a unit", units + "acme/state", badPath, ""},
		{"a unit of an unknown tool", units + "evil:acme%2Fweb/state", badPath, "evil"},
		{"a collection", units + "acme%2Fprod/state", badPath, ""},
		{"an overlong reference", units + "pulumi:acme%2F" + long + "/state", badPath, long},
		{"a control byte in the reference", units + "pulumi:acme%2Fa%0Ab/state", badPath, ""},
		{"a stack version with a sign", stackURL + "?version=%2B1", errcode.StateBadPath, ""},
		{"a negative stack version", stackURL + "?version=-1", errcode.StateBadPath, ""},
		{"a stack version of zero", stackURL + "?version=0", errcode.StateBadPath, ""},
		{"a stack version above the range", stackURL + "?version=99999999999999", errcode.StateBadPath, ""},
		{"a stack version that is text", stackURL + "?version=" + secretText, errcode.StateBadPath, ""},
		{"a workspace version with no prefix", workspaceURL + "?version=1", errcode.StateBadPath, ""},
		{"a workspace version with a traversal", workspaceURL + "?version=sv-..%2Fx", errcode.StateBadPath, ""},
		{"a workspace version in capitals", workspaceURL + "?version=SV-1", errcode.StateBadPath, ""},
		{"a cursor over the cap", stackURL + "?cursor=" + strings.Repeat("c", 513), errcode.StateBadPath, "ccc"},
		{"a query name the route does not read", stackURL + "?filter=" + secretText, errcode.RequestInvalidQuery, ""},
		{"a query that does not parse", stackURL + "?version=%zz", errcode.RequestInvalidQuery, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			both := readerOf(stackUnit, workspaceUnit)
			e := newEnv(t, setup{authz: both, units: newUnits(), limiter: nil, timeout: 0})

			status, body := e.get(t, tc.target)
			wantError(t, status, body, http.StatusBadRequest, tc.code)

			if tc.echo != "" && bytes.Contains(body, []byte(tc.echo)) {
				t.Fatalf("the answer repeats the input: %s", body)
			}

			if e.units.count() != 0 {
				t.Fatalf("reads = %d, want 0: a refused input must not reach the state", e.units.count())
			}
		})
	}
}

// failingUnits returns fake services whose reads of both tools return err.
func failingUnits(err error) *units {
	u := newUnits()
	u.readStack = func(context.Context, stack.ReadMaskedArgs) (stack.MaskedPage, error) {
		return stack.MaskedPage{}, err
	}
	u.readTofu = func(context.Context, workspace.ReadMaskedArgs) (workspace.MaskedPage, error) {
		return workspace.MaskedPage{}, err
	}

	return u
}

// TestReadMapsTheRefusalsOfTheTools proves the answer of each refusal of the
// domain packages: a bad path answers 400, an oversized version and a state past the work bound 413, a read
// past its deadline 504, an unknown version 404, and an error that no class
// knows 500. No answer holds a byte of the error text.
func TestReadMapsTheRefusalsOfTheTools(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"stack bad path", stack.ErrBadPath, http.StatusBadRequest, errcode.StateBadPath},
		{"workspace bad path", workspace.ErrBadPath, http.StatusBadRequest, errcode.StateBadPath},
		{"stack too large", stack.ErrTooLarge, http.StatusRequestEntityTooLarge, errcode.StateVersionTooLarge},
		{"workspace too large", workspace.ErrTooLarge, http.StatusRequestEntityTooLarge, errcode.StateVersionTooLarge},
		{"stack too complex", stack.ErrStateBound, http.StatusRequestEntityTooLarge, errcode.StackStateTooComplex},
		{
			"workspace too complex", workspace.ErrStateBound,
			http.StatusRequestEntityTooLarge, errcode.StateVersionTooComplex,
		},
		{"stack deadline", stack.ErrReadDeadline, http.StatusGatewayTimeout, errcode.StateReadTimeout},
		{"workspace deadline", workspace.ErrReadDeadline, http.StatusGatewayTimeout, errcode.StateReadTimeout},
		{"context deadline", context.DeadlineExceeded, http.StatusGatewayTimeout, errcode.StateReadTimeout},
		{"unknown stack version", stack.ErrNoState, http.StatusNotFound, errcode.StateVersionNotFound},
		{
			"unknown workspace version", workspace.ErrStateVersionNotFound,
			http.StatusNotFound, errcode.StateVersionNotFound,
		},
		{"stack unreadable shape", stack.ErrUnreadableState, http.StatusInternalServerError, errcode.ServerInternal},
		{
			"workspace unreadable shape", workspace.ErrUnreadableState,
			http.StatusInternalServerError, errcode.ServerInternal,
		},
		{"an error that no class knows", errUnknown, http.StatusInternalServerError, errcode.ServerInternal},
	}

	for _, tc := range cases {
		for _, target := range []string{stackURL + "?version=7", workspaceURL + "?version=sv-7"} {
			t.Run(tc.name+" "+target, func(t *testing.T) {
				t.Parallel()

				failing := failingUnits(fmt.Errorf("read %s: %w", secretText, tc.err))
				both := readerOf(stackUnit, workspaceUnit)
				e := newEnv(t, setup{authz: both, units: failing, limiter: nil, timeout: 0})

				status, body := e.get(t, target)
				wantError(t, status, body, tc.status, tc.code)
			})
		}
	}
}

// TestReadUnknownUnitAndNoState proves a stack that does not exist answers
// 404 unit.not-found, and a unit with no state answers 404 unit.no-state.
func TestReadUnknownUnitAndNoState(t *testing.T) {
	t.Parallel()

	missing := newUnits()
	missing.stackError = stack.ErrNotFound

	e := newEnv(t, setup{authz: readerOf(stackUnit), units: missing, limiter: nil, timeout: 0})

	status, body := e.get(t, stackURL)
	wantError(t, status, body, http.StatusNotFound, errcode.UnitNotFound)

	e = newEnv(t, setup{authz: readerOf(stackUnit), units: failingUnits(stack.ErrNoState), limiter: nil, timeout: 0})

	status, body = e.get(t, stackURL)
	wantError(t, status, body, http.StatusNotFound, errcode.UnitNoState)
}

// TestReadRunsUnderTheTimeout proves the read runs with a deadline no later
// than the timeout, and that a read that passes it answers 504 with no bytes
// of the state.
func TestReadRunsUnderTheTimeout(t *testing.T) {
	t.Parallel()

	slow := newUnits()
	slow.readStack = func(ctx context.Context, _ stack.ReadMaskedArgs) (stack.MaskedPage, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > state.ReadTimeout {
			return stack.MaskedPage{}, errNoDeadline
		}

		<-ctx.Done()

		return stack.MaskedPage{Version: secretText, Markers: nil, ValuePaths: nil, NextCursor: ""}, ctx.Err()
	}

	e := newEnv(t, setup{authz: readerOf(stackUnit), units: slow, limiter: nil, timeout: 20 * time.Millisecond})

	status, body := e.get(t, stackURL)
	wantError(t, status, body, http.StatusGatewayTimeout, errcode.StateReadTimeout)
}

// TestReadRefusalsReachTheLimiter proves the refusals that the domain package
// reports reach the denial limiter of the server configuration: five refused
// reads of one caller on one unit hold one key in the limiter and write one
// audit entry in its window.
func TestReadRefusalsReachTheLimiter(t *testing.T) {
	t.Parallel()

	fixed := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	limiter := denylimit.New(denylimit.Config{
		Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: func() time.Time { return fixed },
	})

	var (
		mu      sync.Mutex
		reasons []string
	)

	refusing := newUnits()
	refusing.readStack = func(_ context.Context, args stack.ReadMaskedArgs) (stack.MaskedPage, error) {
		mu.Lock()
		reasons = append(reasons, stack.RefusalBadPath)
		mu.Unlock()

		args.Sink(args.Principal, args.Object, stack.RefusalBadPath)

		return stack.MaskedPage{}, stack.ErrBadPath
	}

	e := newEnv(t, setup{authz: readerOf(stackUnit), units: refusing, limiter: limiter, timeout: 0})

	for range 5 {
		status, body := e.get(t, stackURL+"?cursor=abc")
		wantError(t, status, body, http.StatusBadRequest, errcode.StateBadPath)
	}

	if len(reasons) != 5 {
		t.Fatalf("refusals = %d, want 5", len(reasons))
	}

	if got := limiter.Len(); got != 1 {
		t.Fatalf("keys held by the limiter = %d, want 1 for one caller and one unit", got)
	}

	if got := e.audit.count(); got != 1 {
		t.Fatalf("audit entries = %d, want 1 in the window of the 5 refusals", got)
	}
}

// TestNewHandlerNeedsItsDependencies proves that a Config without a required
// field, with a short cursor key or with a timeout above the limit returns
// ErrMissingDependency and no handler.
func TestNewHandlerNeedsItsDependencies(t *testing.T) {
	t.Parallel()

	base := setup{authz: readerOf(), units: newUnits(), limiter: nil, timeout: 0}

	cases := []struct {
		name   string
		change func(c *state.Config)
	}{
		{"no namespace reader", func(c *state.Config) { c.Namespaces = nil }},
		{"no stack service", func(c *state.Config) { c.Stacks = nil }},
		{"no stack state reader", func(c *state.Config) { c.StackStates = nil }},
		{"no workspace service", func(c *state.Config) { c.Workspaces = nil }},
		{"no authenticator", func(c *state.Config) { c.Authenticator = nil }},
		{"no authorizer", func(c *state.Config) { c.Authorizer = nil }},
		{"no audit store", func(c *state.Config) { c.Audit = nil }},
		{"no logger", func(c *state.Config) { c.Logger = nil }},
		{"a short cursor key", func(c *state.Config) { c.CursorKey = []byte("short") }},
		{"a timeout above the limit", func(c *state.Config) { c.Timeout = time.Minute }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := config(base, &memAudit{mu: sync.Mutex{}, entries: nil})
			tc.change(&cfg)

			h, err := state.NewHandler(cfg)
			if !errors.Is(err, state.ErrMissingDependency) || h != nil {
				t.Fatalf("NewHandler = %v, %v, want ErrMissingDependency and no handler", h, err)
			}
		})
	}
}
