//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	auditapi "opendustpan.org/opendustpan/internal/api/audit"
	inventoryapi "opendustpan.org/opendustpan/internal/api/inventory"
	pulumiapi "opendustpan.org/opendustpan/internal/api/pulumi"
	serverapi "opendustpan.org/opendustpan/internal/api/server"
	tfeapi "opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/workspace"
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
	inventory := coverageInventoryRoutes(tb, namespaces)
	auditRoutes := coverageAuditRoutes(tb, namespaces)
	server := coverageServerRoutes(tb)

	out := make([]RouteRef, 0, len(pulumi)+len(tfe)+len(inventory)+len(auditRoutes)+len(server)+probeRouteCount)
	out = append(out, pulumi...)
	out = append(out, tfe...)
	out = append(out, inventory...)
	out = append(out, auditRoutes...)
	out = append(out, server...)

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
// homes every namespace in the test region, so handler construction passes
// the home-region check.
func coverageNamespaces(tb testing.TB) *namespace.Service {
	tb.Helper()

	svc, err := namespace.NewService(
		namespace.Config{Store: coverageNamespaceStore{}, Now: nil, Region: "test-region"},
	)
	if err != nil {
		tb.Fatalf("namespace service: %v", err)
	}

	return svc
}

// coverageNamespaceStore homes every namespace in the test region. It
// exists only to build handlers for their route tables; no request is
// ever served.
type coverageNamespaceStore struct{}

func (coverageNamespaceStore) CreateNamespace(_ context.Context, _ namespace.Namespace) error {
	return nil
}

func (coverageNamespaceStore) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{
		CreatedAt:       time.Time{},
		Name:            name,
		HomeRegion:      "test-region",
		FailoverVersion: 1,
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

// coveragePulumiRoutes returns the Pulumi CLI adapter's route table.
func coveragePulumiRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	stacks, err := stack.NewService(
		stack.Config{Store: coverageStackStore{}, Namespaces: namespaces, Now: nil, State: nil},
	)
	if err != nil {
		tb.Fatalf("stack service: %v", err)
	}

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		// The store never serves a request: only ActiveUpdate runs, and it
		// answers not found. The embedded stores stay nil on purpose.
		Updates:            coverageUpdateStore{UpdateStore: nil, CheckpointStore: nil, EventStore: nil},
		Checkpoints:        coverageUpdateStore{UpdateStore: nil, CheckpointStore: nil, EventStore: nil},
		Events:             coverageUpdateStore{UpdateStore: nil, CheckpointStore: nil, EventStore: nil},
		Namespaces:         namespaces,
		Stacks:             coverageStackStore{},
		Blobs:              coverageBlobs{},
		Now:                nil,
		Logger:             nil,
		MaxCheckpointBytes: 0,
	})
	if err != nil {
		tb.Fatalf("updates service: %v", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys: coverageKeyStore{}, Wrapper: nil, Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
	})
	if err != nil {
		tb.Fatalf("secret service: %v", err)
	}

	handler, err := pulumiapi.NewHandler(pulumiapi.Config{
		Stacks:                stacks,
		Updates:               updates,
		Secrets:               secrets,
		Namespaces:            namespaces,
		Authenticator:         coverageAuthenticator{},
		Authorizer:            coverageAuthorizer{},
		Logger:                slog.New(slog.DiscardHandler),
		BaseURL:               baseurl.Resolver{},
		CheckpointDeltaCutoff: 0,
		// The gate is irrelevant here: no request is ever served.
		MaxBodyBytesInFlight: 0,
		Audit:                nil,
		OnAuditFailure:       nil,
	})
	if err != nil {
		tb.Fatalf("pulumi handler: %v", err)
	}

	return pulumiRefs(handler.Routes())
}

// coverageStackStore answers no stack. It exists only to build a Handler
// for its route table; no request is ever served.
type coverageStackStore struct{}

func (coverageStackStore) CreateStack(_ context.Context, _ stack.Stack, _ namespace.Fence) error {
	return nil
}

func (coverageStackStore) GetStack(_ context.Context, _ stack.Ref) (stack.Stack, error) {
	var none stack.Stack

	return none, stack.ErrNotFound
}

func (coverageStackStore) DeleteStack(_ context.Context, _ stack.Ref, _ namespace.Fence, _ stack.DeleteGuard) error {
	return stack.ErrNotFound
}

func (coverageStackStore) ForceDeleteStack(_ context.Context, _ stack.Ref, _ namespace.Fence) error {
	return stack.ErrNotFound
}

func (coverageStackStore) ListStacks(_ context.Context, _ stack.Filter) ([]stack.Stack, error) {
	return nil, nil
}

func (coverageStackStore) RenameStack(
	_ context.Context, _ stack.Ref, _ stack.Name, _ namespace.Fence,
) error {
	return stack.ErrNotFound
}

func (coverageStackStore) UpdateStackTags(
	_ context.Context, _ stack.Ref, _ map[string]string, _ time.Time, _ namespace.Fence,
) error {
	return nil
}

// coverageBlobs answers no object. It exists only to build an Updates
// service for the route tables; no request is ever served.
type coverageBlobs struct{}

func (coverageBlobs) Put(_ context.Context, _ blob.Key, _ io.Reader) error {
	return blob.ErrExists
}

func (coverageBlobs) Get(_ context.Context, _ blob.Key) (io.ReadCloser, error) {
	return nil, blob.ErrNotFound
}

func (coverageBlobs) Delete(_ context.Context, _ blob.Key) error {
	return nil
}

func (coverageBlobs) List(_ context.Context, _ string) ([]blob.Object, error) {
	return nil, nil
}

func (coverageBlobs) Ping(_ context.Context) error {
	return nil
}

// coverageUpdateStore answers no update and no state. It exists only to
// build a Handler for its route table; no request is ever served.
type coverageUpdateStore struct {
	stack.UpdateStore
	stack.CheckpointStore
	stack.EventStore
}

func (coverageUpdateStore) ActiveUpdate(_ context.Context, _ stack.Ref) (stack.Update, error) {
	return stack.Update{}, stack.ErrUpdateNotFound
}

// coverageKeyStore answers no key. It exists only to build a Handler for
// its route table; no request is ever served.
type coverageKeyStore struct{}

func (coverageKeyStore) CreateKey(
	_ context.Context, _ secret.WrappedKey, _ namespace.Fence,
) (secret.WrappedKey, error) {
	var none secret.WrappedKey

	return none, secret.ErrKeyNotFound
}

func (coverageKeyStore) GetKey(_ context.Context, _ secret.Scope) (secret.WrappedKey, error) {
	var none secret.WrappedKey

	return none, secret.ErrKeyNotFound
}

func (coverageKeyStore) ListWrapperIDs(_ context.Context) ([]string, error) {
	return nil, nil
}

// coverageTFERoutes returns the TFE-compatible adapter's route table.
func coverageTFERoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	handler, err := tfeapi.NewHandler(tfeapi.Config{
		Authenticator: coverageAuthenticator{},
		Authorizer:    coverageAuthorizer{},
		Logger:        slog.New(slog.DiscardHandler),
		Namespaces:    namespaces,
		Workspaces:    coverageWorkspaces{},
		BaseURL:       baseurl.Resolver{},
	})
	if err != nil {
		tb.Fatalf("tfe handler: %v", err)
	}

	return tfeRefs(handler.Routes())
}

// coverageWorkspaces answers no workspace and no state. It exists only to
// build a Handler for its route table; no request is ever served.
type coverageWorkspaces struct{}

func (coverageWorkspaces) Create(_ context.Context, _ workspace.Ref) (workspace.Workspace, error) {
	var none workspace.Workspace

	return none, workspace.ErrNotFound
}

func (coverageWorkspaces) Get(_ context.Context, _ workspace.Ref) (workspace.Workspace, error) {
	var none workspace.Workspace

	return none, workspace.ErrNotFound
}

func (coverageWorkspaces) GetByID(_ context.Context, _ workspace.ID) (workspace.Workspace, error) {
	var none workspace.Workspace

	return none, workspace.ErrNotFound
}

func (coverageWorkspaces) List(_ context.Context, _ namespace.Name) ([]workspace.Workspace, error) {
	return nil, nil
}

func (coverageWorkspaces) Lock(
	_ context.Context, _ workspace.ID, _, _ string,
) (workspace.Workspace, error) {
	var none workspace.Workspace

	return none, workspace.ErrNotFound
}

func (coverageWorkspaces) Unlock(
	_ context.Context, _ workspace.ID, _ string,
) (workspace.Workspace, error) {
	var none workspace.Workspace

	return none, workspace.ErrNotFound
}

func (coverageWorkspaces) ForceUnlock(_ context.Context, _ workspace.ID) (workspace.Workspace, error) {
	var none workspace.Workspace

	return none, workspace.ErrNotFound
}

func (coverageWorkspaces) Current(_ context.Context, _ workspace.ID) (workspace.StateVersion, error) {
	var none workspace.StateVersion

	return none, workspace.ErrNoState
}

func (coverageWorkspaces) History(
	_ context.Context, _ workspace.ID, _ workspace.Page,
) ([]workspace.StateVersion, int, error) {
	return nil, 0, nil
}

func (coverageWorkspaces) Restore(
	_ context.Context, _ workspace.ID, _ workspace.StateVersionID, _ string,
) (workspace.StateVersion, error) {
	var none workspace.StateVersion

	return none, workspace.ErrStateVersionNotFound
}

func (coverageWorkspaces) ReadState(
	_ context.Context, _ workspace.StateVersionID,
) (workspace.StateVersion, error) {
	var none workspace.StateVersion

	return none, workspace.ErrStateVersionNotFound
}

func (coverageWorkspaces) ReadResources(
	_ context.Context, _ workspace.StateVersionID, _ string, _ int, _ string,
) (workspace.StatePage, error) {
	return workspace.StatePage{}, workspace.ErrStateVersionNotFound
}

func (coverageWorkspaces) ReadResource(
	_ context.Context, _ workspace.StateVersionID, _ string,
) (workspace.StateDetail, error) {
	return workspace.StateDetail{}, workspace.ErrStateVersionNotFound
}

func (coverageWorkspaces) DiffStates(
	_ context.Context, _, _ workspace.StateVersionID, _ string, _ int,
) (workspace.StateDiff, error) {
	return workspace.StateDiff{}, workspace.ErrStateVersionNotFound
}

func (coverageWorkspaces) Open(_ context.Context, _ workspace.StateVersion) (io.ReadCloser, error) {
	return nil, workspace.ErrStateVersionNotFound
}

func (coverageWorkspaces) BeginState(
	_ context.Context, _ workspace.ID, _ string, _ workspace.NewState,
) (workspace.PendingState, error) {
	var none workspace.PendingState

	return none, workspace.ErrNotFound
}

func (coverageWorkspaces) Upload(
	_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
) (workspace.StateVersion, error) {
	var none workspace.StateVersion

	return none, workspace.ErrStateVersionNotFound
}

func (coverageWorkspaces) DiscardJSONRendering(
	_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
) error {
	return workspace.ErrStateVersionNotFound
}

// coverageInventoryRoutes returns the inventory adapter's route table.
func coverageInventoryRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	handler, err := inventoryapi.NewHandler(inventoryapi.Config{
		Namespaces:    namespaces,
		Units:         coverageReader{},
		Authenticator: coverageAuthenticator{},
		Authorizer:    coverageAuthorizer{},
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		tb.Fatalf("inventory handler: %v", err)
	}

	return inventoryRefs(handler.Routes())
}

// coverageReader answers no unit. It exists only to build a Handler for its
// route table; no request is ever served.
type coverageReader struct{}

func (coverageReader) ListUnits(_ context.Context, _ namespace.Name) ([]coreinventory.Entry, error) {
	return nil, nil
}

// coverageAuditRoutes returns the audit adapter's route table.
func coverageAuditRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	handler, err := auditapi.NewHandler(auditapi.Config{
		Namespaces:    namespaces,
		Entries:       coverageAuditStore{},
		Authenticator: coverageAuthenticator{},
		Authorizer:    coverageAuthorizer{},
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		tb.Fatalf("audit handler: %v", err)
	}

	return auditRefs(handler.Routes())
}

// coverageAuditStore answers no entry. It exists only to build a Handler
// for its route table; no request is ever served.
type coverageAuditStore struct{}

func (coverageAuditStore) Append(
	_ context.Context, _ namespace.Fence, _ audit.Entry,
) (audit.Entry, error) {
	var zero audit.Entry

	return zero, nil
}

func (coverageAuditStore) List(
	_ context.Context, _ namespace.Name, _ audit.Page,
) (audit.Listing, error) {
	var zero audit.Listing

	return zero, nil
}

func (coverageAuditStore) Check(_ context.Context, _ namespace.Name) (audit.CheckResult, error) {
	return audit.CheckResult{Checked: 0, BadSequence: 0, Valid: true}, nil
}

// coverageMetrics writes nothing: the route table needs a metrics writer,
// and the coverage gate never serves a request.
type coverageMetrics struct{}

func (coverageMetrics) Write(_ io.Writer) error {
	return nil
}

// coverageMaintenance answers normal state and cancels nothing: the route
// table needs a maintenance service, and the coverage gate never serves a
// request.
type coverageMaintenance struct{}

func (coverageMaintenance) Get(_ context.Context) (maintenance.State, error) {
	return maintenance.StateNormal, nil
}

func (coverageMaintenance) Enter(_ context.Context, _ bool) (maintenance.EnterResult, error) {
	return maintenance.EnterResult{Canceled: nil}, nil
}

func (coverageMaintenance) Leave(context.Context) error { return nil }

// coverageSchema reports no applied migration: the route table needs a
// schema reporter, and the coverage gate never serves a request.
type coverageSchema struct{}

func (coverageSchema) AppliedMigrations(_ context.Context) ([]schema.AppliedMigration, error) {
	return nil, nil
}

// coverageServerRoutes returns the installation adapter's route table: the
// namespace route (ADR 39), the backup routes (ADR 30), the restore routes
// (ADR 38), the metrics route (ADR 32), the maintenance routes (ADR 38) and
// the check routes (ADR 39 decisions 3): backup check and upgrade check.
func coverageServerRoutes(tb testing.TB) []RouteRef {
	tb.Helper()

	handler, err := serverapi.NewHandler(serverapi.Config{
		Authenticator: coverageAuthenticator{},
		Authorizer:    coverageAuthorizer{},
		Logger:        slog.New(slog.DiscardHandler),
		Namespaces:    coverageNamespaces(tb),
		Backups:       nil,
		Restores:      nil,
		Maintenance:   coverageMaintenance{},
		Metrics:       coverageMetrics{},
		Version:       "test",
		Schema:        coverageSchema{},
	})
	if err != nil {
		tb.Fatalf("server handler: %v", err)
	}

	return serverRefs(handler.Routes())
}

// routeRefs formats one adapter's table as method and path pairs. Each
// adapter declares its own Route type with Method and Path fields, so each
// table has its own small mapper.
func pulumiRefs(routes []pulumiapi.Route) []RouteRef {
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// tfeRefs formats the TFE-compatible table as method and path pairs.
func tfeRefs(routes []tfeapi.Route) []RouteRef {
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// inventoryRefs formats the inventory table as method and path pairs.
func inventoryRefs(routes []inventoryapi.Route) []RouteRef {
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// serverRefs formats the installation table as method and path pairs.
func serverRefs(routes []serverapi.Route) []RouteRef {
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// auditRefs formats the audit table as method and path pairs.
func auditRefs(routes []auditapi.Route) []RouteRef {
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
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
		backupAllowEntries(),
		namespaceAllowEntries(),
		maintenanceAllowEntries(),
		operatorAllowEntries(),
		stateAllowEntries(),
		restoreAllowEntries(),
	)
}

// consoleAllowEntries returns the allowlist rows for console-only routes and
// routes the CLI never calls.
func consoleAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/inventory/units",
			CoveringTest: "TestListUnitsBothTools",
			Reason:       "Console-only: no CLI reads the cross-tool unit list.",
		},
		{
			Method: http.MethodGet, Path: "/api/audit/entries",
			CoveringTest: "TestCLIs (checkAuditLog)",
			Reason: "Console-only: no CLI reads the audit entries. The " +
				"harness step lists them after a CLI command that calls the " +
				"decrypt audit route and checks the chain.",
		},
		{
			Method: http.MethodGet, Path: "/api/audit/check",
			CoveringTest: "TestCLIs (checkAuditLog)",
			Reason: "Console-only: no CLI checks the audit chain. The " +
				"harness step checks it after a CLI command that calls the " +
				"decrypt audit route.",
		},
		{
			Method: http.MethodGet, Path: "/api/v2/workspaces/{id}",
			CoveringTest: "TestReadWorkspaceByID",
			Reason:       "Console-only: the CLI reads workspaces by name, never by ID.",
		},
		{
			Method: http.MethodGet, Path: "/api/v2/state-version-outputs/{output_id}",
			CoveringTest: "TestTofuSeededStateRead",
			Reason: "The CLI reads one output only for a sensitive value, so the " +
				"main scenario without one never reaches it. The seeded e2e run " +
				"proves it, but it writes no matrix report.",
		},
		{
			Method: http.MethodPost, Path: "/api/stacks/{namespace}/{project}/{stack}/batch-encrypt",
			CoveringTest: "TestSecretsBatchRoundTrip",
			Reason: "The CLI encrypts one value per call through the single " +
				"encrypt route and decrypts in batches, so no matrix version " +
				"calls batch encrypt.",
		},
		{
			Method: http.MethodPost, Path: "/api/stacks/{namespace}/{project}/{stack}/update/{update}/renew_lease",
			CoveringTest: "TestRenewLeaseEchoesToken",
			Reason:       "Fast e2e updates finish before the CLI renews its lease.",
		},
		{
			Method: http.MethodPost, Path: "/api/stacks/{namespace}/{project}/{stack}/restore/{version}",
			CoveringTest: "TestCLIs (pulumiRestoreScenario)",
			Reason: "Operator and console route (ADR 28): the CLI never restores. " +
				"The harness step restores version 1 and checks it with stack export.",
		},
		{
			Method: http.MethodGet, Path: "/api/v2/state-versions",
			CoveringTest: "TestCLIs (tofuStateVersionListScenario)",
			Reason: "Operator and console route (ADR 28): the CLI never lists " +
				"state versions. The harness step lists two versions after " +
				"two applies.",
		},
		{
			Method: http.MethodPost, Path: "/api/v2/state-versions/{state_version_id}/actions/restore",
			CoveringTest: "TestCLIs (tofuRestoreScenario)",
			Reason: "Operator and console route (ADR 28): the CLI never restores. " +
				"The harness step restores a version and checks it with state pull.",
		},
	}
}

// operatorAllowEntries returns the operator routes and probes that no CLI
// calls.
func operatorAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/server/metrics",
			CoveringTest: "TestBothToolsSameServer (checkServerMetrics)",
			Reason:       "Operator route (ADR 32): no CLI calls it.",
		},
		{
			Method: http.MethodGet, Path: "/health/live",
			CoveringTest: "TestHealthLiveStillChecksNothing",
			Reason:       "Operator probe: no CLI calls it.",
		},
		{
			Method: http.MethodGet, Path: "/health/ready",
			CoveringTest: "TestHealthReadyOK",
			Reason:       "Operator probe: no CLI calls it.",
		},
	}
}

// stateCoveringTest names the test that proves the masked state routes
// instead of a CLI: no CLI version in the matrix calls them.
const stateCoveringTest = "TestCLIs (pulumiMaskedStateScenario)"

// stateAllowEntries returns the console state routes that no CLI calls. The
// harness steps of stateCoveringTest drive them directly after two ups.
func stateAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/stacks/{namespace}/{project}/{stack}/state/{version}/resources",
			CoveringTest: stateCoveringTest,
			Reason: "Console route (ADR 41): the CLI never lists masked resources. " +
				"The harness step reads the list after two ups and checks masking and changed markers.",
		},
		{
			Method: http.MethodGet, Path: "/api/stacks/{namespace}/{project}/{stack}/state/{version}/resources/{urn}",
			CoveringTest: stateCoveringTest,
			Reason: "Console route (ADR 41): the CLI never reads one masked resource. " +
				"The harness step reads it after two ups and checks masking.",
		},
		{
			Method: http.MethodGet, Path: "/api/stacks/{namespace}/{project}/{stack}/state/{version}/outputs",
			CoveringTest: stateCoveringTest,
			Reason: "Console route (ADR 41): the CLI never reads masked outputs. " +
				"The harness step reads them after two ups and checks masking.",
		},
		{
			Method: http.MethodGet, Path: "/api/stacks/{namespace}/{project}/{stack}/state/diff",
			CoveringTest: "TestCLIs (pulumiDiffScenario)",
			Reason: "Console route (ADR 41): the CLI never diffs state. " +
				"The harness step diffs two ups with an added, a removed and a changed " +
				"resource and a changed secret, and checks that no secret value appears.",
		},
		{
			Method: http.MethodGet, Path: "/api/v2/state-versions/{state_version_id}/resources",
			CoveringTest: "TestCLIs (tofuMaskedStateScenario)",
			Reason: "Console route (ADR 41): the CLI never lists masked resources. " +
				"The harness step reads the list after two applies and checks masking and changed markers.",
		},
		{
			Method: http.MethodGet, Path: "/api/v2/state-versions/{state_version_id}/resources/{address}",
			CoveringTest: "TestCLIs (tofuMaskedStateScenario)",
			Reason: "Console route (ADR 41): the CLI never reads one masked resource. " +
				"The harness step reads it after two applies and checks masking.",
		},
		{
			Method: http.MethodGet, Path: "/api/v2/state-versions/{state_version_id}/diff",
			CoveringTest: "TestCLIs (tofuDiffScenario)",
			Reason: "Console route (ADR 41): the CLI never diffs state. " +
				"The harness step diffs two applies with an added, a removed and a changed " +
				"resource and a changed secret, and checks that no secret value appears.",
		},
	}
}

// namespaceCoveringTest names the test that proves the namespace create route
// instead of a CLI: no CLI version in the matrix calls it.
const namespaceCoveringTest = "TestCLIs (checkNamespaceCreateThroughAPI)"

// namespaceAllowEntries returns the operator namespace route that no CLI
// calls. The harness creates the first namespace through it on an empty
// installation, and the scenario steps prove the duplicate, invalid and
// unauthenticated cases.
func namespaceAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodPost, Path: "/api/server/namespaces",
			CoveringTest: namespaceCoveringTest,
			Reason: "Operator route: no CLI creates a namespace. The harness " +
				"creates the first one on an empty installation and the scenario " +
				"checks the duplicate, invalid and unauthenticated cases.",
		},
	}
}

// backupCoveringTest names the test that proves the backup routes instead of
// a CLI: no CLI version in the matrix calls them.
const backupCoveringTest = "TestBackups"

// backupAllowEntries returns the operator backup routes that no CLI calls.
// The harness steps of backupCoveringTest drive them directly.
func backupAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodPost, Path: "/api/server/backups",
			CoveringTest: backupCoveringTest,
			Reason: "Operator route: no CLI starts a backup. The harness step " +
				"starts one and checks the second start conflicts.",
		},
		{
			Method: http.MethodGet, Path: "/api/server/backups",
			CoveringTest: backupCoveringTest,
			Reason: "Operator route: no CLI lists backups. The harness step " +
				"lists them newest first.",
		},
		{
			Method: http.MethodGet, Path: "/api/server/backups/{id}",
			CoveringTest: backupCoveringTest,
			Reason: "Operator route: no CLI reads a backup. The harness step " +
				"polls it until it succeeds and checks the manifest.",
		},
		{
			Method: http.MethodGet, Path: "/api/server/backups/{id}/check",
			CoveringTest: "TestBackupCheck",
			Reason: "Operator route: no CLI in the matrix calls it. The " +
				"opendustpan backup check binary calls it with Go-http-client, " +
				"which never counts as CLI traffic, and TestBackupCheck proves " +
				"it with the check handler tests in check_test.go.",
		},
		{
			Method: http.MethodGet, Path: "/api/server/upgrade-check",
			CoveringTest: "TestUpgradeCheck",
			Reason: "Operator route: no CLI in the matrix calls it. The " +
				"opendustpan upgrade check binary calls it with Go-http-client, " +
				"which never counts as CLI traffic, and TestUpgradeCheck proves " +
				"it with the upgrade handler tests in upgrade_test.go.",
		},
	}
}

// maintenanceCoveringTest names the test that proves the maintenance routes
// instead of a CLI: no CLI version in the matrix calls them.
const maintenanceCoveringTest = "TestMaintenance"

// maintenanceAllowEntries returns the operator maintenance routes that no
// CLI calls. The harness steps of maintenanceCoveringTest drive them
// directly: entering, refusing active work, force-canceling it, and leaving.
func maintenanceAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/server/maintenance",
			CoveringTest: maintenanceCoveringTest,
			Reason: "Operator route: no CLI reads the installation state. " +
				"The harness step reads it before and after the enter.",
		},
		{
			Method: http.MethodPost, Path: "/api/server/maintenance/enter",
			CoveringTest: maintenanceCoveringTest,
			Reason: "Operator route: no CLI enters maintenance mode. The " +
				"harness step refuses active work, then force-cancels it.",
		},
		{
			Method: http.MethodPost, Path: "/api/server/maintenance/leave",
			CoveringTest: maintenanceCoveringTest,
			Reason: "Operator route: no CLI leaves maintenance mode. The " +
				"harness step leaves after the 503 checks.",
		},
	}
}

// restoreCoveringTest names the test that proves the restore routes instead
// of a CLI: the restore command calls them with the Go User-Agent, which
// never counts toward route coverage.
const restoreCoveringTest = "TestBackupRestore"

// restoreAllowEntries returns the operator restore routes that only the
// restore command calls. The steps of restoreCoveringTest drive them through
// the new command: restore into an empty installation, restore over live
// data, check-only, a failed check, a fenced lease, and an undo.
func restoreAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodPost, Path: "/api/server/restores",
			CoveringTest: restoreCoveringTest,
			Reason: "Operator route: only the restore command starts a restore. " +
				"The harness step starts one through it and checks the second start conflicts.",
		},
		{
			Method: http.MethodGet, Path: "/api/server/restores",
			CoveringTest: restoreCoveringTest,
			Reason: "Operator route: only the restore command lists restores. " +
				"The harness step lists them to find the backup taken before the restore.",
		},
		{
			Method: http.MethodGet, Path: "/api/server/restores/{id}",
			CoveringTest: restoreCoveringTest,
			Reason: "Operator route: only the restore command reads a restore. " +
				"The harness step polls it until it succeeds and checks the manifest.",
		},
	}
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
