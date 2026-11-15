//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// harnessUserAgent is the User-Agent of requests the harness itself sends
// (direct API checks inside step checks). Only requests from real CLI
// binaries count toward route coverage.
const harnessUserAgent = "Go-http-client"

// probeRouteCount is the number of health probes ServedRoutes adds after
// the adapter tables: live and ready.
const probeRouteCount = 2

// RouteRef is one served endpoint: its method and ServeMux pattern.
type RouteRef struct {
	Method string
	Path   string
}

// String formats the route as "METHOD path".
func (r RouteRef) String() string {
	return r.Method + " " + r.Path
}

// ServedRoutes returns every route the server serves: the union of each
// adapter's route table plus the health probes. It builds every handler
// with stub dependencies and reads back Routes, so the list always matches
// the production tables: a new route appears here without any edit.
func ServedRoutes(tb testing.TB) []RouteRef {
	tb.Helper()

	namespaces := coverageNamespaces(tb)

	pulumi := coveragePulumiRoutes(tb, namespaces)
	tfe := coverageTFERoutes(tb, namespaces)
	tofuHTTP := coverageTofuHTTPRoutes(tb, namespaces)
	inventory := coverageInventoryRoutes(tb, namespaces)
	auditRoutes := coverageAuditRoutes(tb, namespaces)
	contextRoutes := coverageContextRoutes(tb, namespaces)
	profileRoutes := coverageProfileRoutes(tb)
	issuerRoutes := coverageIssuerRoutes(tb)
	sinkRoutes := coverageSinkRoutes(tb)
	jobRoutes := coverageJobRoutes(tb, namespaces)
	driftRoutes := coverageDriftRoutes(tb, namespaces)
	triggerRoutes := coverageTriggerRoutes(tb, namespaces)
	settingsRoutes := coverageSettingsRoutes(tb, namespaces)
	registryRoutes := coverageRegistryRoutes(tb)
	sourceFilterRoutes := coverageSourceFilterRoutes(tb, namespaces)
	trustRuleRoutes := coverageTrustRuleRoutes(tb, namespaces)
	server := coverageServerRoutes(tb)

	out := slices.Concat(
		pulumi,
		tfe,
		tofuHTTP,
		inventory,
		auditRoutes,
		contextRoutes,
		profileRoutes,
		issuerRoutes,
		sinkRoutes,
		jobRoutes,
		driftRoutes,
		triggerRoutes,
		settingsRoutes,
		registryRoutes,
		sourceFilterRoutes,
		trustRuleRoutes,
		server,
	)
	out = slices.Grow(out, probeRouteCount)

	// The probes live on the root mux, not in any adapter table.
	out = append(out,
		RouteRef{Method: http.MethodGet, Path: "/health/live"},
		RouteRef{Method: http.MethodGet, Path: "/health/ready"},
	)

	slices.SortFunc(out, func(a, b RouteRef) int {
		if a.Method != b.Method {
			return strings.Compare(a.Method, b.Method)
		}

		return strings.Compare(a.Path, b.Path)
	})

	return out
}

// coverageNamespaces returns a namespace service over a stub store that
// homes every namespace on the test installation, so handler construction passes
// the primary-installation check.
func coverageNamespaces(tb testing.TB) *namespace.Service {
	tb.Helper()

	svc, err := namespace.NewService(
		namespace.Config{Store: coverageNamespaceStore{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		tb.Fatalf("namespace service: %v", err)
	}

	return svc
}

// coverageNamespaceStore homes every namespace on the test installation. It
// exists only to build handlers for their route tables; no request is
// ever served.
type coverageNamespaceStore struct{}

func (coverageNamespaceStore) CreateNamespace(_ context.Context, _ namespace.Namespace) error {
	return nil
}

func (coverageNamespaceStore) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{
		CreatedAt:           time.Time{},
		ID:                  namespace.NewID(),
		Name:                name,
		PrimaryInstallation: "test-region",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

func (coverageNamespaceStore) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	return nil, nil
}

func (coverageNamespaceStore) Ping(_ context.Context) error {
	return nil
}

// coverageAuthenticator denies every token. Route tables never authenticate,
// so it never runs.
type coverageAuthenticator struct{}

func (coverageAuthenticator) AuthenticateToken(
	_ context.Context, _ string,
) (identity.Principal, error) {
	var none identity.Principal

	return none, identity.ErrUnauthenticated
}

// coverageAuthorizer denies every action. Route tables never authorize,
// so it never runs.
type coverageAuthorizer struct{}

func (coverageAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

// ListObjects lists no object. Route tables never list, so it never runs.
func (coverageAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
}

// AllowEntry is one route that no CLI version in the matrix calls, with the
// test that covers it instead. Console-only routes, operator routes and
// routes the CLI never calls live here, each with a comment naming the
// covering test, so the coverage check can tell a deliberate gap from a
// missing e2e step.
type AllowEntry struct {
	// Method is the HTTP method, for example GET.
	Method string
	// Path is the ServeMux path pattern, for example /api/user.
	Path string
	// CoveringTest names the test that proves the route instead, for
	// example TestReadWorkspaceByID.
	CoveringTest string
	// Reason says why no CLI calls the route.
	Reason string
}

// CoverageAllowlist returns the routes that no CLI calls. Every entry
// carries the test that covers it instead. A route lands here only when no
// CLI version in the matrix can reach it; when the CLI gains a caller, the
// entry goes and an e2e step proves the route.
func CoverageAllowlist() []AllowEntry {
	return slices.Concat(
		consoleAllowEntries(),
		auditReadAllowEntries(),
		deletedUnitAllowEntries(),
		backupAllowEntries(),
		storeCheckAllowEntries(),
		namespaceAllowEntries(),
		maintenanceAllowEntries(),
		auditChainAllowEntries(),
		operatorAllowEntries(),
		stateAllowEntries(),
		tofuStateAtAllowEntries(),
		restoreAllowEntries(),
		journalAllowEntries(),
		tofuHTTPAllowEntries(),
		principalAllowEntries(),
		accessAllowEntries(),
		reviewAllowEntries(),
		tokenAllowEntries(),
		tokenPolicyAllowEntries(),
		sessionPolicyAllowEntries(),
		signInAllowEntries(),
		ldapSignInAllowEntries(),
		devAllowEntries(),
		signInProviderAllowEntries(),
		ipAllowlistAllowEntries(),
		approvalRuleAllowEntries(),
		gateObjectAllowEntries(),
		delegationAllowEntries(),
		namespacePrincipalAllowEntries(),
		auditSinkAllowEntries(),
		manualLockAllowEntries(),
		jobAllowEntries(),
		driftAllowEntries(),
		triggerAllowEntries(),
		unitSettingsAllowEntries(),
		placementAllowEntries(),
		outputSharingAllowEntries(),
		preferencesAllowEntries(),
		settingsAllowEntries(),
		localUserAllowEntries(),
		passkeyAllowEntries(),
		passkeySignInAllowEntries(),
		savedViewAllowEntries(),
		gitHostAllowEntries(),
		registryAllowEntries(),
		sinkAllowEntries(),
		sourceFilterAllowEntries(),
		trustRuleAllowEntries(),
		apiDescriptionAllowEntries(),
		issuerAllowEntries(),
	)
}

// UncoveredRoutes returns every served route with no CLI-made request in
// entries, minus the allowlist. Harness requests (direct API checks inside
// step checks) carry the Go User-Agent and never count: only traffic from
// real CLI binaries proves a route. The result is sorted, so failures read
// the same on every run.
func UncoveredRoutes(served []RouteRef, allowed []AllowEntry, entries []Entry) []RouteRef {
	struck := map[RouteRef]bool{}

	for i := range entries {
		entry := &entries[i]
		if strings.HasPrefix(entry.UserAgent, harnessUserAgent) {
			continue
		}

		if route, ok := matchRoute(served, entry.Method, entry.Path); ok {
			struck[route] = true
		}
	}

	excused := map[RouteRef]bool{}
	for _, allow := range allowed {
		excused[RouteRef{Method: allow.Method, Path: allow.Path}] = true
	}

	var out []RouteRef

	for _, route := range served {
		if !struck[route] && !excused[route] {
			out = append(out, route)
		}
	}

	return out
}

// matchRoute returns the served route with method whose pattern holds path.
// A {placeholder} segment matches any single path segment; every other
// segment must match exactly. It answers false when no route holds the path,
// for example a CLI probe of an endpoint the server does not serve.
func matchRoute(served []RouteRef, method, path string) (RouteRef, bool) {
	for _, route := range served {
		if route.Method == method && matchPattern(route.Path, path) {
			return route, true
		}
	}

	return RouteRef{Method: "", Path: ""}, false
}

// matchPattern reports whether pattern holds path. Both split on "/"; a
// {placeholder} segment matches anything, other segments match exactly.
func matchPattern(pattern, path string) bool {
	want := strings.Split(pattern, "/")
	got := strings.Split(path, "/")

	if len(want) != len(got) {
		return false
	}

	for i := range want {
		segment := want[i]
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			continue
		}

		if segment != got[i] {
			return false
		}
	}

	return true
}

// SetOverrides is not used by this fake.
func (coverageNamespaceStore) SetOverrides(
	context.Context,
	namespace.Name,
	namespace.Overrides,
	namespace.Fence,
) error {
	return nil
}

// allowRoutes builds the allow entries of routes that one test covers for one
// reason.
func allowRoutes(test, reason string, routes ...RouteRef) []AllowEntry {
	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{Method: r.Method, Path: r.Path, CoveringTest: test, Reason: reason})
	}

	return out
}
