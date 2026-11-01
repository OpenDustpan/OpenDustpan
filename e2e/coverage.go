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
	driftapi "opendustpan.org/opendustpan/internal/api/drift"
	inventoryapi "opendustpan.org/opendustpan/internal/api/inventory"
	issuerapi "opendustpan.org/opendustpan/internal/api/issuer"
	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	notificationsapi "opendustpan.org/opendustpan/internal/api/notifications"
	contextapi "opendustpan.org/opendustpan/internal/api/objectcontext"
	profileapi "opendustpan.org/opendustpan/internal/api/profile"
	pulumiapi "opendustpan.org/opendustpan/internal/api/pulumi"
	registryapi "opendustpan.org/opendustpan/internal/api/registry"
	serverapi "opendustpan.org/opendustpan/internal/api/server"
	settingsapi "opendustpan.org/opendustpan/internal/api/settings"
	sourcefilterapi "opendustpan.org/opendustpan/internal/api/sourcefilter"
	tfeapi "opendustpan.org/opendustpan/internal/api/tfe"
	tofuhttpapi "opendustpan.org/opendustpan/internal/api/tofuhttp"
	triggersapi "opendustpan.org/opendustpan/internal/api/triggers"
	trustrulesapi "opendustpan.org/opendustpan/internal/api/trustrules"
	"opendustpan.org/opendustpan/internal/authgate/authgatetest"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/issuersvc"
	"opendustpan.org/opendustpan/internal/outputsharesvc"
	registryservice "opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/internal/storecheck"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/internal/unitsettingssvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
	"opendustpan.org/opendustpan/pkg/objectcontext"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/savedview"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/source"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/trigger"
	"opendustpan.org/opendustpan/pkg/trustrule"
	"opendustpan.org/opendustpan/pkg/unitsettings"
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
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil, All: false}, nil
}

// coveragePulumiRoutes returns the Pulumi CLI adapter's route table.
func coveragePulumiRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	stacks, err := stack.NewService(
		stack.Config{
			Store:       coverageStackStore{},
			Namespaces:  namespaces,
			Now:         nil,
			State:       nil,
			Retention:   0,
			ManualLocks: nil,
		},
	)
	if err != nil {
		tb.Fatalf("stack service: %v", err)
	}

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		ManualLocks: nil,
		// The updates service never deletes a blob here, so it has no guard.
		BlobGuard: nil,
		// The store never serves a request: only ActiveUpdate runs, and it
		// answers not found. The embedded stores stay nil on purpose.
		Updates:            coverageUpdateStore{UpdateStore: nil, CheckpointStore: nil, EventStore: nil},
		Tokens:             nil,
		Checkpoints:        coverageUpdateStore{UpdateStore: nil, CheckpointStore: nil, EventStore: nil},
		Events:             coverageUpdateStore{UpdateStore: nil, CheckpointStore: nil, EventStore: nil},
		Namespaces:         namespaces,
		Stacks:             coverageStackStore{},
		Blobs:              coverageBlobs{},
		Refusals:           stacktest.NewRefusals(),
		Now:                nil,
		Logger:             nil,
		MaxCheckpointBytes: 0,
		Journal:            nil,
		Replayer:           nil,
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
		SessionPolicy:           nil,
		DisablePulumiJournaling: false,
		Stacks:                  stacks,
		Updates:                 updates,
		Secrets:                 secrets,
		Namespaces:              namespaces,
		Authenticator:           coverageAuthenticator{},
		Authorizer:              coverageAuthorizer{},
		Sessions:                nil,
		AddressGate:             nil,
		Logger:                  slog.New(slog.DiscardHandler),
		BaseURL:                 baseurl.Resolver{},
		CheckpointDeltaCutoff:   0,
		// The gate is irrelevant here: no request is ever served.
		MaxBodyBytesInFlight: 0,
		Gate:                 nil,
		Audit:                coverageAuditStore{},
		OnAuditFailure:       nil,
		DenialLimiter:        nil,
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

func (coverageStackStore) DeleteStack(
	_ context.Context,
	_ stack.Ref,
	_ namespace.Fence,
	_ stack.DeleteGuard,
	_ coreinventory.Deletion,
) error {
	return stack.ErrNotFound
}

func (coverageStackStore) ForceDeleteStack(
	_ context.Context,
	_ stack.Ref,
	_ namespace.Fence,
	_ coreinventory.Deletion,
) error {
	return stack.ErrNotFound
}

// RestoreStack implements stack.Store; this fake holds no deleted stack.
func (coverageStackStore) RestoreStack(
	_ context.Context, _ stack.ID, _ stack.Name, _ namespace.Fence, _ audit.Entry,
) (stack.Ref, error) {
	return stack.Ref{}, stack.ErrNotFound
}

// PurgeStack implements stack.Store; this fake holds no deleted stack.
func (coverageStackStore) PurgeStack(
	_ context.Context,
	_ stack.ID,
	_ namespace.Fence,
	_ time.Time,
	_ audit.Entry,
) error {
	return stack.ErrNotFound
}

func (coverageStackStore) ListStacks(_ context.Context, _ stack.Filter) ([]stack.Stack, error) {
	return nil, nil
}

// QueryStacks implements stack.Store over the lists of ListStacks.
func (coverageStackStore) QueryStacks(ctx context.Context, q stack.PageQuery) ([]stack.Stack, error) {
	return stack.ListPage(ctx, coverageStackStore{}.ListStacks, q)
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

// ListPage implements blob.Store: it cuts the page that req selects from the
// objects of list.
func (x coverageBlobs) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if err := req.Validate(); err != nil {
		return blob.Page{}, err
	}

	objects, err := x.list(ctx, req.Prefix)
	if err != nil {
		return blob.Page{}, err
	}

	slices.SortFunc(objects, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return blob.PageOf(objects, req), nil
}

func (coverageBlobs) Ping(_ context.Context) error {
	return nil
}

func (coverageBlobs) list(_ context.Context, _ string) ([]blob.Object, error) {
	return nil, nil
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
		Sessions:       nil,
		SessionPolicy:  nil,
		StateGuard:     nil,
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     namespaces,
		Workspaces:     coverageWorkspaces{},
		OutputChecker:  nil,
		BaseURL:        baseurl.Resolver{},
		Gate:           nil,
		MaxStateBytes:  0,
		Audit:          coverageAuditStore{},
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Registry: &tfeapi.RegistryConfig{
			Service:     coverageRegistryService{},
			Host:        "",
			DefaultPort: "",
			URLKey:      make([]byte, tfeapi.MinRegistryURLKeyBytes),
			URLTTL:      0,
			Now:         nil,
		},
	})
	if err != nil {
		tb.Fatalf("tfe handler: %v", err)
	}

	return tfeRefs(handler.Routes())
}

// coverageTofuHTTPRoutes builds the OpenTofu http backend handler with stub
// dependencies and returns its route table.
func coverageTofuHTTPRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	handler, err := tofuhttpapi.NewHandler(tofuhttpapi.Config{
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Logger:         slog.New(slog.DiscardHandler),
		Workspaces:     coverageTofuHTTPWorkspaces{},
		Namespaces:     namespaces,
		Audit:          coverageAuditStore{},
		Gate:           nil,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
	})
	if err != nil {
		tb.Fatalf("tofu http backend handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// coverageTofuHTTPWorkspaces answers no workspace and no state. It exists
// only to build the http backend handler for its route table; no request is
// ever served.
type coverageTofuHTTPWorkspaces struct{}

func (coverageTofuHTTPWorkspaces) Get(_ context.Context, _ workspace.Ref) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrNotFound
}

func (coverageTofuHTTPWorkspaces) GetByID(_ context.Context, _ workspace.ID) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrNotFound
}

func (coverageTofuHTTPWorkspaces) Create(_ context.Context, _ workspace.Ref) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrNotFound
}

func (coverageTofuHTTPWorkspaces) AdmitFirstWrite(_ context.Context, _ workspace.Ref, _ workspace.StreamInput) error {
	return workspace.ErrNotFound
}

func (coverageTofuHTTPWorkspaces) AdmitFirstLock(_ context.Context, _ workspace.Ref, _ workspace.LockRequest) error {
	return workspace.ErrNotFound
}

func (coverageTofuHTTPWorkspaces) LockWithIDForJob(
	_ context.Context, _ workspace.ID, _ workspace.LockRequest,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrNotFound
}

func (coverageTofuHTTPWorkspaces) UnlockLock(
	_ context.Context, _ workspace.ID, _, _ string,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrNotFound
}

//
//nolint:revive // The port takes the lock ID beside the caller, the allowance and the audit record.
//nolint:revive // The signature of the workspace port that the handler reads.
func (coverageTofuHTTPWorkspaces) ForceUnlockLock(
	_ context.Context, _ workspace.ID, _ workspace.Lock, _ string,
	_ namespace.Overrides, _ func() error, _ audit.Entry,
) (workspace.Workspace, bool, error) {
	return workspace.Workspace{}, false, workspace.ErrNotFound
}

func (coverageTofuHTTPWorkspaces) Current(_ context.Context, _ workspace.ID) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, workspace.ErrNoState
}

func (coverageTofuHTTPWorkspaces) Open(_ context.Context, _ workspace.StateVersion) (io.ReadCloser, error) {
	return nil, workspace.ErrNoState
}

func (coverageTofuHTTPWorkspaces) WriteState(
	_ context.Context, _ workspace.ID, _ workspace.StreamInput,
	_ workspace.StreamAuthority, _ io.Reader,
) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, workspace.ErrNotFound
}

// coverageWorkspaces answers no workspace and no state. It exists only to
// build a Handler for its route table; no request is ever served.
type coverageWorkspaces struct{}

func (coverageWorkspaces) Delete(_ context.Context, _ workspace.Ref, _ audit.Entry) error {
	return workspace.ErrNotFound
}

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

func (coverageWorkspaces) Query(ctx context.Context, q workspace.ListQuery) (workspace.ListPage, error) {
	all, err := coverageWorkspaces{}.List(ctx, q.Namespace)
	if err != nil {
		return workspace.ListPage{}, err
	}

	return workspace.PageWorkspaces(all, q), nil
}

func (coverageWorkspaces) LockForJob(
	_ context.Context, _ workspace.ID, _, _, _ string,
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

//nolint:revive // The signature of the workspace port that the handler reads.
func (rcoverageWorkspaces coverageWorkspaces) ForceUnlockLock(
	ctx context.Context,
	id workspace.ID,
	_ workspace.Lock,
	by string,
	_ namespace.Overrides, _ func() error, _ audit.Entry,
) (workspace.Workspace, bool, error) {
	w, err := rcoverageWorkspaces.forceUnlockUnfenced(ctx, id, by)

	return w, false, err
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

func (coverageWorkspaces) ReadStateAt(
	_ context.Context, _ workspace.ID, _ workspace.StateAtRequest,
) (workspace.StateAtRead, error) {
	return workspace.StateAtRead{}, workspace.ErrNoStateAt
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
	_ context.Context, _ workspace.ID, _ string, _ workspace.NewState, _ workspace.Authority,
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

func (coverageWorkspaces) CheckUpload(
	_ context.Context, _ workspace.StateVersionID, _ token.Token,
) (namespace.Name, error) {
	return "", workspace.ErrStateVersionNotFound
}

func (coverageWorkspaces) CheckJSONRenderingUpload(
	_ context.Context, _ workspace.StateVersionID, _ token.Token,
) (namespace.Name, error) {
	return "", workspace.ErrStateVersionNotFound
}

// coverageInventoryRoutes returns the inventory adapter's route table.
func coverageInventoryRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	collections, err := collection.NewService(collection.Config{
		Store:      coverageCollectionStore{},
		Namespaces: coverageNamespaceStore{},
		Now:        nil,
	})
	if err != nil {
		tb.Fatalf("collection service: %v", err)
	}

	placements := coveragePlacements(tb)

	locks, err := manuallock.NewService(manuallock.Config{
		Store:      coverageLockStore{},
		Namespaces: coverageNamespaceStore{},
		Authorizer: identity.AdminOnly{},
		Now:        nil,
		Phases:     nil,
	})
	if err != nil {
		tb.Fatalf("manual lock service: %v", err)
	}

	sharing, err := outputsharesvc.New(outputsharesvc.Config{
		Settings: coverageOutputSharing{}, Namespaces: coverageNamespaceStore{}, Authorizer: identity.AdminOnly{},
		Now: nil,
	})
	if err != nil {
		tb.Fatalf("output sharing service: %v", err)
	}

	outputs, err := outputsharesvc.NewReader(outputsharesvc.ReaderConfig{
		States:  coverageStates{},
		Checker: coverageOutputChecker{},
		Sources: map[string]outputshare.Outputs{"pulumi": coverageOutputSource{}},
	})
	if err != nil {
		tb.Fatalf("output reader: %v", err)
	}

	handler, err := inventoryapi.NewHandler(inventoryapi.Config{
		Placements:    placements,
		OutputSharing: sharing,
		Outputs:       outputs,
		Views:         coverageViews(tb),
		Locks:         locks,
		PublicURL:     "",
		Moves:         nil,
		Namespaces:    namespaces,
		Units:         coverageReader{},
		Collections:   collections,
		Deleted:       coverageReader{},
		History:       nil,
		Lifecycles:    map[string]coreinventory.Lifecycle{"pulumi": coverageLifecycle{}},
		ReadScope:     coverageScope{},
		Authenticator: coverageAuthenticator{},
		Authorizer:    coverageAuthorizer{},
		Sessions:      nil,
		Logger:        slog.New(slog.DiscardHandler),
		Now:           nil,
		Audit:         coverageAuditStore{}, OnAuditFailure: nil, DenialLimiter: nil,
		StateGuard: nil,
	})
	if err != nil {
		tb.Fatalf("inventory handler: %v", err)
	}

	return inventoryRefs(handler.Routes())
}

// coverageOutputSharing is an outputshare.Settings that reads nobody and
// stores nothing. The coverage check only lists the routes.
type coverageOutputSharing struct{}

func (coverageOutputSharing) Setting(_ context.Context, _ namespace.Name) (outputshare.Default, error) {
	return outputshare.DefaultNobody, nil
}

func (coverageOutputSharing) SetSetting(
	_ context.Context, _ namespace.Name, _ outputshare.Default, _ namespace.Fence, _ audit.Entry,
) error {
	return nil
}

// coverageStates, coverageOutputChecker and coverageOutputSource let the
// coverage check build the reader of outputs. They answer nothing: the check
// only lists the routes.
type (
	coverageStates        struct{}
	coverageOutputChecker struct{}
	coverageOutputSource  struct{}
)

func (coverageStates) Latest(_ context.Context, _ identity.Object) (outputshare.State, error) {
	return outputshare.State{ID: "", ClientEncrypted: false}, nil
}

//nolint:gocritic // The port passes the request by value.
func (coverageOutputChecker) CheckRead(_ context.Context, _ outputshare.ReadRequest) (outputshare.Decision, error) {
	return outputshare.Decision{Kind: "", Rule: "", Secrets: false}, outputshare.ErrUnknownProducer
}

func (coverageOutputSource) Outputs(
	_ context.Context, _ identity.Object, _ string,
) ([]outputshare.Output, error) {
	return nil, outputshare.ErrUnknownProducer
}

// coverageContextRoutes returns the context adapter's route table.
func coverageContextRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	writer, err := objectcontext.NewWriter(objectcontext.WriterConfig{
		Store: coverageContextStore{}, Namespaces: coverageNamespaceStore{}, Now: nil,
	})
	if err != nil {
		tb.Fatalf("context writer: %v", err)
	}

	handler, err := contextapi.NewHandler(contextapi.Config{
		Namespaces:    namespaces,
		Units:         coverageReader{},
		Deleted:       coverageReader{},
		Collections:   nil,
		Drift:         nil,
		Entries:       coverageContextStore{},
		Writer:        writer,
		Authenticator: coverageAuthenticator{},
		Authorizer:    coverageAuthorizer{},
		Sessions:      nil,
		Logger:        slog.New(slog.DiscardHandler),
		Audit:         coverageAuditStore{}, OnAuditFailure: nil, DenialLimiter: nil,
	})
	if err != nil {
		tb.Fatalf("context handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// coverageJobRoutes returns the jobs adapter's route table.
func coverageJobRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	handler, err := jobsapi.NewHandler(jobsapi.Config{
		Namespaces:     namespaces,
		Workers:        nil,
		Jobs:           struct{ job.Store }{nil},
		Starter:        struct{ job.Starter }{nil},
		Pools:          nil,
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Audit:          coverageAuditStore{},
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
	})
	if err != nil {
		tb.Fatalf("jobs handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// coverageSourceFilterRoutes returns the source filter adapter's route table.
func coverageSourceFilterRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	handler, err := sourcefilterapi.NewHandler(sourcefilterapi.Config{
		Namespaces:     namespaces,
		Filters:        struct{ source.Filters }{nil},
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Audit:          coverageAuditStore{},
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
		Now:            nil,
	})
	if err != nil {
		tb.Fatalf("source filter handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// coverageTrustRuleRoutes returns the trust rule adapter's route table.
func coverageTrustRuleRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	rules, err := trustrulesvc.NewService(trustrulesvc.Config{
		Store: struct{ trustrule.Store }{nil}, Namespaces: namespaces,
		Principals: struct{ trustrulesvc.Principals }{nil}, Authorizer: coverageAuthorizer{},
		InstallationURLs: nil, OwnIssuer: nil, CheckURL: nil, Now: nil,
	})
	if err != nil {
		tb.Fatalf("trust rule service: %v", err)
	}

	exchanger, err := trustrulesvc.NewExchanger(trustrulesvc.ExchangeConfig{
		Rules: struct{ trustrule.Store }{nil}, Exchanges: struct{ trustrule.ExchangeStore }{nil},
		Namespaces: namespaces, Principals: struct{ trustrulesvc.Principals }{nil},
		Tokens: struct{ trustrulesvc.TokenIssuer }{nil}, Audit: coverageAuditStore{},
		ServerAudit: struct{ audit.ServerStore }{nil}, Refusals: nil, Keys: nil, InstallationURLs: nil,
		OwnIssuer: nil, Now: nil,
	})
	if err != nil {
		tb.Fatalf("token exchange: %v", err)
	}

	handler, err := trustrulesapi.NewHandler(trustrulesapi.Config{
		Exchanger:      exchanger,
		Namespaces:     namespaces,
		Rules:          rules,
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Audit:          coverageAuditStore{},
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
		Now:            nil,
	})
	if err != nil {
		tb.Fatalf("trust rule handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// coverageTriggerRoutes returns the trigger adapter's route table.
func coverageTriggerRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	manager, err := trigger.NewManager(trigger.ManagerConfig{
		Store: struct{ trigger.Store }{nil}, Dispatcher: struct{ trigger.Dispatcher }{nil},
		Authorizer: coverageAuthorizer{}, Savers: struct{ trigger.SaverCheck }{nil},
		Units: struct{ trigger.Units }{nil}, Fences: struct{ trigger.Fences }{nil}, Now: nil, NewID: nil,
	})
	if err != nil {
		tb.Fatalf("trigger manager: %v", err)
	}

	handler, err := triggersapi.NewHandler(triggersapi.Config{
		Namespaces:     namespaces,
		Workers:        nil,
		Units:          coverageReader{},
		Store:          struct{ trigger.Store }{nil},
		Manager:        manager,
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Audit:          coverageAuditStore{},
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
	})
	if err != nil {
		tb.Fatalf("triggers handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// coverageNoPools is the runner pool port of a handler that the coverage check
// builds only to list its routes.
type coverageNoPools struct{}

func (coverageNoPools) Pool(context.Context, string) (runner.Pool, error) {
	return runner.Pool{}, runner.ErrNotFound
}

func (coverageNoPools) Pools(context.Context, namespace.Name) ([]runner.Pool, error) { return nil, nil }

// coverageSettingsRoutes returns the unit settings adapter's route table.
func coverageSettingsRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	service, err := unitsettingssvc.New(unitsettingssvc.Config{
		Store: struct{ unitsettings.Store }{nil}, Fences: struct{ unitsettingssvc.Fences }{nil},
		Connections: struct{ unitsettingssvc.Connections }{nil}, Pools: coverageNoPools{},
		Now: nil,
	})
	if err != nil {
		tb.Fatalf("unit settings service: %v", err)
	}

	handler, err := settingsapi.NewHandler(settingsapi.Config{
		Namespaces:     namespaces,
		Units:          coverageReader{},
		Service:        service,
		Suggestions:    nil,
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Audit:          coverageAuditStore{},
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
		Idempotency:    nil,
		Records:        nil,
	})
	if err != nil {
		tb.Fatalf("unit settings handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for i := range routes {
		route := &routes[i]
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// coverageDriftRoutes returns the drift adapter's route table.
func coverageDriftRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	handler, err := driftapi.NewHandler(driftapi.Config{
		Namespaces:     namespaces,
		Workers:        nil,
		Units:          coverageReader{},
		Results:        struct{ drift.Store }{nil},
		Checker:        struct{ drift.Checker }{nil},
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Audit:          coverageAuditStore{},
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
	})
	if err != nil {
		tb.Fatalf("drift handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// coverageIssuerRoutes returns the routes of the issuer. The stores are empty
// shells: they exist only to build a Handler for its route table.
func coverageIssuerRoutes(tb testing.TB) []RouteRef {
	tb.Helper()

	svc, err := issuersvc.NewService(issuersvc.Config{
		Store:         struct{ profile.IssuerStore }{nil},
		Namespaces:    coverageNamespaceStore{},
		Sealer:        struct{ profile.Sealer }{nil},
		Authorizer:    coverageAuthorizer{},
		Notifier:      nil,
		Fetcher:       nil,
		IssuerBaseURL: "",
		PublicURL:     "",
		Now:           nil,
		Log:           nil,
	})
	if err != nil {
		tb.Fatalf("issuer service: %v", err)
	}

	handler, err := issuerapi.NewHandler(issuerapi.Config{
		Issuer:         svc,
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Audit:          coverageAuditStore{},
		Namespaces:     nil,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		Logger:         slog.New(slog.DiscardHandler),
	})
	if err != nil {
		tb.Fatalf("issuer handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// coverageProfileRoutes returns the routes of the profile API. The stores
// are empty shells: they exist only to build a Handler for its route table.
func coverageProfileRoutes(tb testing.TB) []RouteRef {
	tb.Helper()

	svc, err := profile.NewService(profile.Config{
		Store:      struct{ profile.Store }{nil},
		Namespaces: coverageNamespaceStore{},
		Sealer:     struct{ profile.Sealer }{nil},
		Audit:      struct{ audit.Store }{nil},
		Authorizer: coverageAuthorizer{},
		Now:        nil,
	})
	if err != nil {
		tb.Fatalf("profile service: %v", err)
	}

	handler, err := profileapi.NewHandler(profileapi.Config{
		Profiles:      svc,
		Authenticator: coverageAuthenticator{},
		Authorizer:    coverageAuthorizer{},
		Sessions:      nil,
		SessionPolicy: nil,
		Idempotency:   nil,
		Logger:        slog.New(slog.DiscardHandler),
		Now:           nil,
		Audit:         coverageAuditStore{}, Namespaces: nil, OnAuditFailure: nil, DenialLimiter: nil,
	})
	if err != nil {
		tb.Fatalf("profile handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// coverageSinkRoutes returns the routes of the notification sink API. The
// stores are empty shells: they exist only to build a Handler for its route
// table.
func coverageSinkRoutes(tb testing.TB) []RouteRef {
	tb.Helper()

	svc, err := sinks.NewService(sinks.Config{
		Store:      struct{ notify.SinkStore }{nil},
		Sealer:     notify.SinkSealer{Namespaces: struct{ notify.ScopeSealer }{nil}, Installation: nil},
		Namespaces: coverageNamespaces(tb),
		Senders:    nil,
		Ports:      egress.Ports{},
		Counter:    authgatetest.NewMemory(nil),
		Now:        nil,
	})
	if err != nil {
		tb.Fatalf("sink service: %v", err)
	}

	handler, err := notificationsapi.NewHandler(notificationsapi.Config{
		Sinks:          svc,
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Sessions:       nil,
		Idempotency:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Audit:          coverageAuditStore{},
		Namespaces:     nil,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
	})
	if err != nil {
		tb.Fatalf("sink handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// coverageContextStore holds no context. It exists only to build a Handler
// for its route table; no request is ever served.
type coverageContextStore struct{}

func (coverageContextStore) Entries(_ context.Context, _ []objectcontext.Ref) ([]objectcontext.Entry, error) {
	return nil, nil
}

//nolint:revive // The signature of the objectcontext.Store port.
func (coverageContextStore) SetAPI(
	_ context.Context, _ objectcontext.Ref, _ objectcontext.Patch, _ string, _ namespace.Fence, _ audit.Entry,
) (objectcontext.Entry, error) {
	return objectcontext.Entry{}, objectcontext.ErrNotFound
}

func (coverageContextStore) DeleteAPI(_ context.Context, _ objectcontext.Ref, _ namespace.Fence, _ audit.Entry) error {
	return objectcontext.ErrNotFound
}

func (coverageContextStore) ReplaceOutput(
	_ context.Context, _ objectcontext.Ref, _ objectcontext.OutputItem, _ []objectcontext.Entry, _ namespace.Fence,
) error {
	return nil
}

func (coverageContextStore) DropUnit(_ context.Context, _ objectcontext.Ref, _ namespace.Fence) error {
	return nil
}

func (coverageContextStore) ByOwner(
	_ context.Context, _ namespace.Name, _, _ string, _ int,
) ([]objectcontext.Entry, string, error) {
	return nil, "", nil
}

// coverageCollectionStore holds no collection. It exists only so that the
// inventory handler serves its collection routes in the route table; no
// request is ever served.
type coverageCollectionStore struct{}

func (coverageCollectionStore) CreateCollection(
	_ context.Context, _ collection.Collection, _ namespace.Fence, _ audit.Entry,
) error {
	return nil
}

func (coverageCollectionStore) GetCollection(_ context.Context, _ collection.Ref) (collection.Collection, error) {
	return collection.Collection{CreatedAt: time.Time{}, ID: "", Ref: collection.Ref{Namespace: "", Name: ""}}, nil
}

func (coverageCollectionStore) ListCollections(_ context.Context, _ namespace.Name) ([]collection.Collection, error) {
	return nil, nil
}

func (coverageCollectionStore) DeleteCollection(
	_ context.Context, _ collection.Ref, _ namespace.Fence, _ audit.Entry,
) error {
	return nil
}

func (coverageCollectionStore) SetUnitCollection(
	_ context.Context, _ collection.Move, _ namespace.Fence, _ audit.Entry,
) error {
	return nil
}

func (coverageCollectionStore) UnitCollection(_ context.Context, _ collection.UnitRef) (collection.Ref, error) {
	return collection.Ref{Namespace: "", Name: ""}, nil
}

// coveragePlacements returns a placement rule service over the stub store. It
// exists only so that the inventory handler serves the placement rule routes in
// the route table.
func coveragePlacements(tb testing.TB) *collection.PlacementService {
	tb.Helper()

	placements, err := collection.NewPlacementService(collection.PlacementConfig{
		Store:      coveragePlacementStore{},
		Namespaces: coverageNamespaceStore{},
		Now:        nil,
	})
	if err != nil {
		tb.Fatalf("placement service: %v", err)
	}

	return placements
}

// coveragePlacementStore holds no placement rule. It exists only so that the
// inventory handler serves its placement rule routes in the route table; no
// request is ever served.
type coveragePlacementStore struct{}

func (coveragePlacementStore) CreatePlacementRule(
	_ context.Context, _ collection.Rule, _ namespace.Fence, _ audit.Entry,
) error {
	return nil
}

func (coveragePlacementStore) UpdatePlacementRule(
	_ context.Context, _ collection.Rule, _ namespace.Fence, _ audit.Entry,
) error {
	return nil
}

func (coveragePlacementStore) DeletePlacementRule(
	_ context.Context, _ namespace.Name, _ string, _ namespace.Fence, _ audit.Entry,
) error {
	return nil
}

func (coveragePlacementStore) GetPlacementRule(
	_ context.Context, _ namespace.Name, _ string,
) (collection.Rule, error) {
	return collection.Rule{
		CreatedAt: time.Time{}, ID: "", Namespace: "", Tool: "", Project: "", Stack: "", Collection: "",
		CreatedBy: "", Position: 0,
	}, nil
}

func (coveragePlacementStore) ListPlacementRules(_ context.Context, _ namespace.Name) ([]collection.Rule, error) {
	return nil, nil
}

// coverageLockStore holds no manual lock. It exists only so that the inventory
// handler serves its lock routes in the route table; no request is ever served.
type coverageLockStore struct{}

func (coverageLockStore) PutLock(_ context.Context, _ manuallock.Lock, _ namespace.Fence, _ audit.Entry) error {
	return nil
}

func (coverageLockStore) RemoveLock(
	_ context.Context, _ manuallock.Target, _ string, _ namespace.Fence, _ audit.Entry,
) (manuallock.Lock, error) {
	return manuallock.Lock{}, manuallock.ErrNotLocked
}

func (coverageLockStore) ForceRemoveLock(
	_ context.Context, _ manuallock.Target, _ namespace.Fence, _ audit.Entry,
) (manuallock.Lock, error) {
	return manuallock.Lock{}, manuallock.ErrNotLocked
}

func (coverageLockStore) GetLock(_ context.Context, _ manuallock.Target) (manuallock.Lock, error) {
	return manuallock.Lock{}, manuallock.ErrNotLocked
}

func (coverageLockStore) CoveringLock(_ context.Context, _ collection.UnitRef) (manuallock.Lock, error) {
	return manuallock.Lock{}, manuallock.ErrNotLocked
}

func (coverageLockStore) CoveringLocks(_ context.Context, _ collection.UnitRef) ([]manuallock.Lock, error) {
	return nil, manuallock.ErrNotLocked
}

// coverageReader answers no unit. It exists only to build a Handler for its
// route table; no request is ever served.
type coverageReader struct{}

func (coverageReader) ListUnits(_ context.Context, _ namespace.Name) ([]coreinventory.Entry, error) {
	return nil, nil
}

// QueryUnits implements coreinventory.Reader over the entries of ListUnits.
func (coverageReader) QueryUnits(ctx context.Context, q coreinventory.UnitQuery) ([]coreinventory.Entry, error) {
	all, err := coverageReader{}.ListUnits(ctx, q.Namespace)
	if err != nil {
		return nil, err
	}

	return coreinventory.PageUnits(all, q), nil
}

func (coverageReader) GetUnit(_ context.Context, _ string) (coreinventory.Entry, error) {
	return coreinventory.Entry{}, coreinventory.ErrUnitNotFound
}

func (coverageReader) ListDeletedUnits(_ context.Context, _ namespace.Name) ([]coreinventory.DeletedEntry, error) {
	return nil, nil
}

func (coverageReader) QueryDeletedUnits(
	_ context.Context, _ coreinventory.DeletedQuery,
) (coreinventory.DeletedPage, error) {
	return coreinventory.DeletedPage{Next: nil, Entries: nil, Total: 0}, nil
}

func (coverageReader) GetDeletedUnit(
	_ context.Context, _ namespace.Name, _, _ string,
) (coreinventory.DeletedEntry, error) {
	return coreinventory.DeletedEntry{}, coreinventory.ErrNotFound
}

// coverageScope lets no caller read a deleted unit. It exists only to build a
// Handler for its route table; no request is ever served.
type coverageScope struct{}

func (coverageScope) ReadableUnits(
	_ context.Context, _ identity.Principal, _ namespace.Name,
) (bool, []string, error) {
	return false, nil, nil
}

// coverageLifecycle restores and purges nothing. It exists only to build a
// Handler for its route table; no request is ever served.
type coverageLifecycle struct{}

func (coverageLifecycle) Restore(
	_ context.Context, _ namespace.Name, _, _ string, _ audit.Entry,
) (string, error) {
	return "", coreinventory.ErrNotFound
}

func (coverageLifecycle) Purge(_ context.Context, _ namespace.Name, _ string, _ audit.Entry) error {
	return coreinventory.ErrNotFound
}

// coverageAuditRoutes returns the audit adapter's route table.
func coverageAuditRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	handler, err := auditapi.NewHandler(auditapi.Config{
		Namespaces:     namespaces,
		Entries:        coverageAuditStore{},
		EntryReader:    nil,
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
		OnAuditFailure: nil, DenialLimiter: nil,
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

func (coverageAuditStore) AppendServer(_ context.Context, _ audit.Entry) (audit.Entry, error) {
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
	return audit.CheckResult{Checked: 0, BadSequence: 0, Unchained: 0, Valid: true}, nil
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

// coverageIPAllowlistStore holds no rule: the route table needs an IP
// allowlist service, and the coverage gate never serves a request.
type coverageIPAllowlistStore struct{}

func (coverageIPAllowlistStore) NamespaceList(_ context.Context, _ namespace.Name) (ipallow.List, error) {
	return ipallow.List{}, nil
}

func (coverageIPAllowlistStore) SetNamespaceList(
	_ context.Context, _ namespace.Name, _ ipallow.List, _ namespace.Fence, _ audit.Entry,
) error {
	return nil
}

// coverageIPAllowlists returns the IP allowlist service of the route table.
func coverageIPAllowlists(tb testing.TB) *ipallow.Service {
	tb.Helper()

	svc, err := ipallow.NewService(ipallow.Config{
		Store: coverageIPAllowlistStore{}, Namespaces: coverageNamespaces(tb), Now: nil, Enabled: true,
	})
	if err != nil {
		tb.Fatalf("ip allowlist service: %v", err)
	}

	return svc
}

// coverageGateConfig holds no gate, no access limit, no approval default and no approval rule: the route
// table needs a gate configuration service, and the coverage gate never
// serves a request.
type coverageGateConfig struct{}

func (coverageGateConfig) ApprovalDefault(context.Context, gate.Scope) (gate.ApprovalDefault, error) {
	return gate.ApprovalDefault{}, gate.ErrNotFound
}

func (coverageGateConfig) PutApprovalDefault(
	context.Context, gate.ApprovalDefault, int64, audit.Entry,
) (gate.ApprovalDefault, error) {
	return gate.ApprovalDefault{}, gate.ErrNotFound
}

func (coverageGateConfig) DeleteApprovalDefault(context.Context, gate.Scope, int64, audit.Entry) error {
	return gate.ErrNotFound
}

func (coverageGateConfig) ApprovalRules(context.Context, gate.Scope) ([]gate.ApprovalRule, error) {
	return nil, nil
}

func (coverageGateConfig) ApprovalRule(context.Context, gate.Scope, gate.ID) (gate.ApprovalRule, error) {
	return gate.ApprovalRule{}, gate.ErrNotFound
}

func (coverageGateConfig) PutApprovalRule(
	context.Context, gate.ApprovalRule, int64, audit.Entry,
) (gate.ApprovalRule, error) {
	return gate.ApprovalRule{}, gate.ErrNotFound
}

func (coverageGateConfig) DeleteApprovalRule(context.Context, gate.ObjectRef, audit.Entry) error {
	return gate.ErrNotFound
}

func (coverageGateConfig) Gates(context.Context, gate.Scope) ([]gate.Gate, error) {
	return nil, nil
}

func (coverageGateConfig) Gate(context.Context, gate.Scope, gate.ID) (gate.Gate, error) {
	return gate.Gate{}, gate.ErrNotFound
}

func (coverageGateConfig) PutGate(context.Context, gate.Gate, int64, string, audit.Entry) (gate.Gate, error) {
	return gate.Gate{}, gate.ErrNotFound
}

func (coverageGateConfig) DeleteGate(context.Context, gate.ObjectRef, string, audit.Entry) error {
	return gate.ErrNotFound
}

func (coverageGateConfig) AccessLimits(context.Context, gate.Scope) ([]gate.AccessLimit, error) {
	return nil, nil
}

func (coverageGateConfig) AccessLimit(context.Context, gate.Scope, gate.ID) (gate.AccessLimit, error) {
	return gate.AccessLimit{}, gate.ErrNotFound
}

func (coverageGateConfig) PutAccessLimit(
	context.Context, gate.AccessLimit, int64, audit.Entry,
) (gate.AccessLimit, error) {
	return gate.AccessLimit{}, gate.ErrNotFound
}

func (coverageGateConfig) DeleteAccessLimit(context.Context, gate.ObjectRef, audit.Entry) error {
	return gate.ErrNotFound
}

// coverageStoreCheckRecords holds no store check row: the route table needs a
// store check service to mount the routes, and the coverage gate never serves
// a request.
type coverageStoreCheckRecords struct{}

func (coverageStoreCheckRecords) StartCheck(
	context.Context, storecheck.Record, durable.StartRequest, time.Time,
) error {
	return storecheck.ErrRunning
}

func (coverageStoreCheckRecords) GetCheck(context.Context, storecheck.ID) (storecheck.Record, error) {
	return storecheck.Record{}, storecheck.ErrNotFound
}

func (coverageStoreCheckRecords) ListChecks(context.Context) ([]storecheck.Record, error) {
	return nil, nil
}

func (coverageStoreCheckRecords) FinishCheck(
	context.Context, durable.StepFence, storecheck.ID, storecheck.Finish,
) error {
	return storecheck.ErrNotFound
}

func (coverageStoreCheckRecords) FailEndedChecks(context.Context, time.Time) (int, error) {
	return 0, nil
}

// coverageStoreChecks returns the store check service of the route table.
func coverageStoreChecks(tb testing.TB) *storecheck.Service {
	tb.Helper()

	blobs, err := blobfs.Open(tb.TempDir())
	if err != nil {
		tb.Fatalf("blob store: %v", err)
	}

	tb.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			tb.Errorf("close blob store: %v", closeErr)
		}
	})

	svc, err := storecheck.NewService(storecheck.Config{
		Records: coverageStoreCheckRecords{}, Operations: durabletest.NewMem().Store(), Blobs: blobs, Backup: nil,
		Audit: coverageAuditStore{}, Admit: nil,
		Limits: storecheck.Limits{MaxObjects: 0, MaxTotalBytes: 0, MaxObjectBytes: 0, Timeout: 0, Cooldown: 0},
		Now:    nil, Owner: "", Lease: 0, Logger: nil,
	})
	if err != nil {
		tb.Fatalf("store check service: %v", err)
	}

	return svc
}

// coverageSessionPolicyStore holds the default session policy: the route
// table needs a session policy service, and the coverage gate never serves
// a request.
type coverageSessionPolicyStore struct{}

func (coverageSessionPolicyStore) GetSessionPolicy(_ context.Context) (session.Policy, error) {
	return session.DefaultPolicy(), nil
}

func (coverageSessionPolicyStore) UpdateSessionPolicy(
	_ context.Context, _ session.PolicyUpdate,
) (session.Policy, error) {
	return session.DefaultPolicy(), nil
}

// coverageSessionPolicies returns the session policy service of the route
// table.
func coverageSessionPolicies(tb testing.TB) *session.Policies {
	tb.Helper()

	svc, err := session.NewPolicies(session.PolicyConfig{Store: coverageSessionPolicyStore{}, Now: nil})
	if err != nil {
		tb.Fatalf("session policy service: %v", err)
	}

	return svc
}

// coverageServerRoutes returns the installation adapter's route table: the
// namespace route (ADR 39), the backup routes (ADR 30), the restore routes
// (ADR 38), the metrics route (ADR 32), the maintenance routes (ADR 38) and
// the check routes (ADR 39 decisions 3): backup check and upgrade check.
func coverageServerRoutes(tb testing.TB) []RouteRef {
	tb.Helper()

	handler, err := serverapi.NewHandler(serverapi.Config{
		Preferences:      nil,
		Settings:         nil,
		GitHost:          coverageGitHost(tb),
		Idempotency:      nil,
		Instances:        nil,
		Workers:          nil,
		Namespaces:       coverageNamespaces(tb),
		Backups:          nil,
		Restores:         nil,
		Maintenance:      coverageMaintenance{},
		Metrics:          coverageMetrics{},
		TempAdmins:       nil,
		Version:          "test",
		Schema:           coverageSchema{},
		Principals:       nil,
		Access:           nil,
		DeletedUnits:     nil,
		AuditChain:       nil,
		AuditSinks:       nil,
		IPAllowlists:     coverageIPAllowlists(tb),
		Gates:            coverageGateConfig{},
		SessionPolicy:    coverageSessionPolicies(tb),
		SignIn:           nil,
		CheckpointEpochs: nil,
		InstallationKind: "",
		StoreChecks:      coverageStoreChecks(tb),
		Now:              nil,
		Authenticator:    coverageAuthenticator{},
		Authorizer:       coverageAuthorizer{},
		Logger:           slog.New(slog.DiscardHandler),
		Journal:          nil,
		Tokens:           nil,
		Audit:            coverageAuditStore{},
		OnAuditFailure:   nil,
		DenialLimiter:    nil,
	})
	if err != nil {
		tb.Fatalf("server handler: %v", err)
	}

	return append(serverRefs(handler.Routes()), coverageSignInRoutes(tb)...)
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
		savedViewAllowEntries(),
		gitHostAllowEntries(),
		registryAllowEntries(),
		savedViewAllowEntries(),
		sinkAllowEntries(),
		sourceFilterAllowEntries(),
		trustRuleAllowEntries(),
		apiDescriptionAllowEntries(),
		issuerAllowEntries(),
	)
}

// issuerAllowEntries covers the routes of the OIDC issuer. No CLI calls them:
// a relying party reads the two protocol routes, and the console and the
// client of the API call the management routes. The steps of TestIssuerAPI
// make the keys, read the documents with no credential, rotate, check the
// copy, change the settings and export, against the real server.
func issuerAllowEntries() []AllowEntry {
	const (
		base   = "/api/dustpan/v1/namespaces/{namespace}/issuer"
		reason = "Client route: no CLI calls the issuer. The steps of TestIssuerAPI call it against the real server."
	)

	routes := []struct{ method, path string }{
		{http.MethodGet, base},
		{http.MethodPatch, base},
		{http.MethodGet, base + "/export"},
		{http.MethodPost, base + "/keys"},
		{http.MethodPost, base + "/keys/rotate"},
		{http.MethodPost, base + "/keys/check-copy"},
		{http.MethodGet, "/oidc/{nsid}/.well-known/openid-configuration"},
		{http.MethodGet, "/oidc/{nsid}/jwks"},
	}

	entries := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		entries = append(entries, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: "TestIssuerAPI", Reason: reason,
		})
	}

	return entries
}

// apiDescriptionAllowEntries covers the route of the description of the API.
// No CLI reads it: the generators of the SDKs and the clients do. The steps
// of TestAPIDescription read it with no credential, with a wrong credential
// and with the token.
func apiDescriptionAllowEntries() []AllowEntry {
	return []AllowEntry{{
		Method: http.MethodGet, Path: "/api/dustpan/v1/openapi.json", CoveringTest: "TestAPIDescription",
		Reason: "Client route: no CLI reads the description of the API. The harness steps of " +
			"TestAPIDescription read it with and without a credential and check one known path.",
	}}
}

// devCoveringTest names the test that proves the routes of the dev command
// instead of a CLI: no CLI reads the capabilities document or signs in with
// the code of the first run.
const devCoveringTest = "TestDevCommand"

// devAllowEntries returns the capabilities document and the sign-in with the
// one-time code of the dev command. The steps of devCoveringTest start the
// real binary on a pseudo terminal, play the browser with the code from the
// banner, and read the document with and without the token.
func devAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/capabilities", CoveringTest: devCoveringTest,
			Reason: "Console and client route: no CLI reads the capabilities document. The harness " +
				"step reads it from a dev server with and without the token.",
		},
		{
			Method: http.MethodPost, Path: "/api/dustpan/v1/sign-in/dev-code", CoveringTest: devCoveringTest,
			Reason: "Console route: only the one-time link of the dev command signs in with the code. " +
				"The harness step plays the browser with the code from the banner of the real binary.",
		},
	}
}

// auditSinkCoveringTest names the test that proves the audit sink list
// instead of a CLI: no CLI reads the delivery state of the audit sinks.
const auditSinkCoveringTest = "TestAuditSinks"

// auditSinkAllowEntries returns the operator route of the audit sinks. The
// harness steps of auditSinkCoveringTest drive it through a server with a
// file sink on each store.
func auditSinkAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/server/audit/sinks",
			CoveringTest: auditSinkCoveringTest,
			Reason: "Operator route: no CLI lists the audit sinks. The harness " +
				"step starts a server with a file sink, writes one audited change " +
				"and reads the list.",
		},
	}
}

// namespacePrincipalCoveringTest names the test that proves the routes of
// the principals of a namespace instead of a CLI: no CLI calls them.
const namespacePrincipalCoveringTest = "TestNamespacePrincipals"

// namespacePrincipalAllowEntries returns the operator and console routes of
// the principals of a namespace. The harness steps of
// namespacePrincipalCoveringTest drive each one through the real binary on
// each store.
func namespacePrincipalAllowEntries() []AllowEntry {
	reason := "Operator and console route: no CLI manages the principals of a namespace. " +
		"The harness step calls it through the real binary and checks the answer."

	const (
		list = "/api/server/namespaces/{namespace}/principals"
		one  = list + "/{id}"
	)

	routes := []struct{ method, path string }{
		{http.MethodPost, list},
		{http.MethodGet, list},
		{http.MethodGet, one},
		{http.MethodPatch, one},
		{http.MethodDelete, one},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: namespacePrincipalCoveringTest, Reason: reason,
		})
	}

	return out
}

// delegationCoveringTest names the test that proves the routes of the
// tokens, the grants and the export of one service principal instead of a
// CLI: no CLI calls them.
const delegationCoveringTest = "TestServicePrincipalTokens"

// delegationAllowEntries returns the operator and console routes of one
// service principal: its tokens, the grants on it and its export. The
// harness steps of delegationCoveringTest drive each one through the real
// binary on each store.
func delegationAllowEntries() []AllowEntry {
	reason := "Operator and console route: no CLI manages the tokens of a service principal or the grants on it. " +
		"The harness step calls it through the real binary and checks the answer."

	const (
		root   = "/api/server/principals/{principal}"
		tokens = root + "/tokens"
		grants = root + "/grants"
	)

	routes := []struct{ method, path string }{
		{http.MethodPost, tokens},
		{http.MethodGet, tokens},
		{http.MethodPost, tokens + "/{id}/rotate"},
		{http.MethodDelete, tokens + "/{id}"},
		{http.MethodGet, grants},
		{http.MethodPost, grants},
		{http.MethodDelete, grants + "/{id}"},
		{http.MethodGet, root + "/access-review"},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: delegationCoveringTest, Reason: reason,
		})
	}

	return out
}

// principalCoveringTest names the test that proves the principal and group
// routes instead of a CLI: no CLI calls them (ADR 52).
const principalCoveringTest = "TestPrincipalsAndGroups"

// principalAllowEntries returns the operator and console routes of the
// principals and groups of the installation. The harness steps of
// principalCoveringTest drive each one through the real binary on each store.
func principalAllowEntries() []AllowEntry {
	reason := "Operator and console route (ADR 52): no CLI manages principals or groups. " +
		"The harness step calls it through the real binary and checks the answer."

	const (
		principals = "/api/server/principals"
		principal  = principals + "/{id}"
		groups     = "/api/server/groups"
		group      = groups + "/{id}"
		members    = group + "/members"
	)

	routes := []struct{ method, path string }{
		{http.MethodPost, principals},
		{http.MethodGet, principals},
		{http.MethodGet, principal},
		{http.MethodPatch, principal},
		{http.MethodDelete, principal},
		{http.MethodGet, principal + "/groups"},
		{http.MethodPost, groups},
		{http.MethodGet, groups},
		{http.MethodGet, group},
		{http.MethodPatch, group},
		{http.MethodDelete, group},
		{http.MethodGet, members},
		{http.MethodPost, members},
		{http.MethodDelete, members + "/{principal}"},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: principalCoveringTest, Reason: reason,
		})
	}

	return out
}

// accessCoveringTest names the test that proves the role and grant routes
// instead of a CLI: no CLI calls them (ADR 52).
const accessCoveringTest = "TestRolesAndGrants"

// accessAllowEntries returns the operator and console routes of the roles
// and grants of the installation. The harness steps of accessCoveringTest
// drive each one through the real binary on each store.
func accessAllowEntries() []AllowEntry {
	reason := "Operator and console route (ADR 52): no CLI manages roles or grants. " +
		"The harness step calls it through the real binary and checks the answer."

	const (
		roles = "/api/server/roles"
		role  = roles + "/{id}"
	)

	grantPaths := []string{
		"/api/server/grants",
		"/api/server/namespaces/{namespace}/grants",
		"/api/server/namespaces/{namespace}/collections/{collection}/grants",
		"/api/server/namespaces/{namespace}/unit-grants",
	}

	roleRoutes := []struct{ method, path string }{
		{http.MethodGet, roles},
		{http.MethodGet, role},
		{http.MethodPost, roles},
		{http.MethodPatch, role},
		{http.MethodDelete, role},
	}

	const perGrantPath = 3 // a list, a grant and a revoke

	routes := make([]struct{ method, path string }, 0, len(roleRoutes)+perGrantPath*len(grantPaths))
	routes = append(routes, roleRoutes...)

	for _, grants := range grantPaths {
		routes = append(routes,
			struct{ method, path string }{http.MethodGet, grants},
			struct{ method, path string }{http.MethodPost, grants},
			struct{ method, path string }{http.MethodDelete, grants + "/{id}"},
		)
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: accessCoveringTest, Reason: reason,
		})
	}

	return out
}

// tokenCoveringTest names the test that proves the access token routes
// instead of a CLI: no CLI calls them (ADR 53).
const tokenCoveringTest = "TestAccessTokens"

// tokenAllowEntries returns the operator and console routes of the access
// tokens. The harness steps of tokenCoveringTest drive each one through the
// real binary on each store, and sign in with the tokens they issue.
func tokenAllowEntries() []AllowEntry {
	reason := "Operator and console route (ADR 53): no CLI manages access tokens. " +
		"The harness step calls it through the real binary and signs in with the token it issues."

	const (
		list = "/api/server/tokens"
		one  = list + "/{id}"
	)

	routes := []struct{ method, path string }{
		{http.MethodGet, list},
		{http.MethodGet, one},
		{http.MethodDelete, one},
		{http.MethodPost, "/api/server/service-tokens"},
		{http.MethodPost, "/api/server/service-tokens/{id}/rotate"},
		{http.MethodGet, "/api/server/caller/tokens"},
		{http.MethodPost, "/api/server/caller/tokens"},
		{http.MethodDelete, "/api/server/caller/tokens/{id}"},
		{http.MethodPost, "/api/server/caller/tokens/{id}/rotate"},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: tokenCoveringTest, Reason: reason,
		})
	}

	return out
}

// tokenPolicyAllowEntries returns the operator and console routes of the
// token policy and the principal enable and disable (ADR 53). The harness steps of
// TestTokenPolicy drive each one through the real binary on each store.
func tokenPolicyAllowEntries() []AllowEntry {
	reason := "Operator and console route (ADR 53): no CLI changes the token policy or enables or disables a principal. " +
		"The harness step calls it through the real binary and checks the answer."

	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/server/token-policy"},
		{http.MethodPatch, "/api/server/token-policy"},
		{http.MethodPost, "/api/server/token-policy/sweep"},
		{http.MethodPost, "/api/server/principals/{id}/enable"},
		{http.MethodPost, "/api/server/principals/{id}/disable"},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: "TestTokenPolicy", Reason: reason,
		})
	}

	return out
}

// signInAllowEntries returns the console sign-in routes. The steps of
// TestSignIn drive each one in the browser flow against a test OpenID
// provider, on each store.
func signInAllowEntries() []AllowEntry {
	reason := "Console route: no CLI signs in through a browser. " +
		"The harness plays the browser with a cookie jar against a test OpenID provider and checks each answer."

	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/server/sign-in/{provider}"},
		{http.MethodGet, "/api/server/sign-in/{provider}/callback"},
		{http.MethodGet, "/api/server/session"},
		{http.MethodPost, "/api/server/sign-out"},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: "TestSignIn", Reason: reason,
		})
	}

	return out
}

// signInProviderAllowEntries returns the operator and console routes of
// the sign-in providers. The steps of TestSignIn drive each one through the
// real binary on each store: a create, the list, a read, a change, a
// refused delete and a delete.
func signInProviderAllowEntries() []AllowEntry {
	reason := "Operator and console route: no CLI manages sign-in providers. " +
		"The harness calls each route through the real binary and checks the answer."

	const list, one = "/api/server/sign-in-providers", "/api/server/sign-in-providers/{id}"

	routes := []struct{ method, path string }{
		{http.MethodGet, list},
		{http.MethodPost, list},
		{http.MethodGet, one},
		{http.MethodPatch, one},
		{http.MethodDelete, one},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: "TestSignIn", Reason: reason,
		})
	}

	return out
}

// sessionPolicyAllowEntries returns the operator and console routes of the
// session policy. The harness steps of TestSessionPolicy drive each one
// through the real binary on each store.
func sessionPolicyAllowEntries() []AllowEntry {
	reason := "Operator and console route: no CLI reads or changes the session policy. " +
		"The harness step calls it through the real binary and checks the answer."

	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/server/session-policy"},
		{http.MethodPatch, "/api/server/session-policy"},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: "TestSessionPolicy", Reason: reason,
		})
	}

	return out
}

// ipAllowlistCoveringTest names the test that proves the IP allowlist routes
// instead of a CLI: no CLI manages IP allowlists.
const ipAllowlistCoveringTest = "TestIPAllowlists"

// ipAllowlistAllowEntries returns the operator and console routes of the IP
// allowlists of the namespaces. The harness steps of
// ipAllowlistCoveringTest drive each one through the real binary on each store.
func ipAllowlistAllowEntries() []AllowEntry {
	reason := "Operator and console route: no CLI manages IP allowlists. " +
		"The harness step calls it through the real binary and checks the answer."

	const list = "/api/server/namespaces/{namespace}/ip-allowlist"

	routes := []struct{ method, path string }{
		{http.MethodGet, list},
		{http.MethodPut, list},
		{http.MethodDelete, list},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: ipAllowlistCoveringTest, Reason: reason,
		})
	}

	return out
}

// approvalRuleCoveringTest names the test that proves the approval default and
// approval rule routes instead of a CLI: no CLI sets an approval rule.
const approvalRuleCoveringTest = "TestApprovalRules"

// approvalRuleAllowEntries returns the routes of the approval defaults and the
// approval rules of a namespace. The steps of approvalRuleCoveringTest drive
// each one through the real server on each store.
func approvalRuleAllowEntries() []AllowEntry {
	reason := "Operator and console route: no CLI manages approval rules. " +
		"The harness step calls it through the real server and checks the answer."

	const (
		defaultPath = "/api/dustpan/v1/namespaces/{namespace}/approval-default"
		rulesPath   = "/api/dustpan/v1/namespaces/{namespace}/approval-rules"
	)

	routes := []struct{ method, path string }{
		{http.MethodGet, defaultPath},
		{http.MethodPut, defaultPath},
		{http.MethodDelete, defaultPath},
		{http.MethodGet, rulesPath},
		{http.MethodGet, rulesPath + "/{id}"},
		{http.MethodPut, rulesPath + "/{id}"},
		{http.MethodDelete, rulesPath + "/{id}"},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: approvalRuleCoveringTest, Reason: reason,
		})
	}

	return out
}

// gateObjectCoveringTest names the test that proves the gate and access limit
// routes instead of a CLI: no CLI sets a gate.
const gateObjectCoveringTest = "TestGateRoutes"

// gateObjectAllowEntries returns the routes of the gates and the access limits
// of a namespace, of a collection and of a unit. The steps of
// gateObjectCoveringTest drive each one through the real server on each store.
func gateObjectAllowEntries() []AllowEntry {
	reason := "Operator and console route: no CLI manages gates or access limits. " +
		"The harness step calls it through the real server and checks the answer."

	const namespacePath = "/api/dustpan/v1/namespaces/{namespace}"

	bases := []string{
		namespacePath,
		namespacePath + "/collections/{collection}",
		namespacePath + "/units/by-name/{tool}/{name}",
	}

	const item = "/{id}"

	out := make([]AllowEntry, 0, 2*4*len(bases))

	for _, base := range bases {
		for _, kind := range []string{"/gates", "/access-limits"} {
			for _, r := range []struct{ method, suffix string }{
				{http.MethodGet, ""}, {http.MethodGet, item}, {http.MethodPut, item}, {http.MethodDelete, item},
			} {
				out = append(out, AllowEntry{
					Method:       r.method,
					Path:         base + kind + r.suffix,
					CoveringTest: gateObjectCoveringTest,
					Reason:       reason,
				})
			}
		}
	}

	return out
}

// tofuHTTPAllowEntries returns the allowlist rows of the OpenTofu http
// backend. The real CLI reaches all four routes in TestTofuHTTPBackend, on
// every store and every OpenTofu version. That test writes no matrix report,
// so the coverage check cannot see its traffic.
func tofuHTTPAllowEntries() []AllowEntry {
	const (
		path         = "/tofu/state/{namespace}/{workspace}"
		coveringTest = "TestTofuHTTPBackend"
		reason       = "The real OpenTofu CLI reaches the route through the http backend in its own test, " +
			"which writes no matrix report."
	)

	return []AllowEntry{
		{Method: http.MethodGet, Path: path, CoveringTest: coveringTest, Reason: reason},
		{Method: http.MethodPost, Path: path, CoveringTest: coveringTest, Reason: reason},
		{Method: "LOCK", Path: path, CoveringTest: coveringTest, Reason: reason},
		{Method: "UNLOCK", Path: path, CoveringTest: coveringTest, Reason: reason},
	}
}

// journalAllowEntries returns the allowlist row for the journal entry route.
// The release build answers journal version 0, so no CLI reaches the route
// there. The recording build (journalrecording tag) reaches it through
// make e2e-journal.
func journalAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodPatch, Path: "/api/stacks/{namespace}/{project}/{stack}/update/{update}/journalentries",
			CoveringTest: "TestPulumiJournalingOn (make e2e-journal, journalrecording build)",
			Reason: "The release build answers journal version 0, so no CLI sends journal batches. " +
				"The recording build answers version 1, and its test proves the route.",
		},
	}
}

// deletedUnitCoveringTest names the harness steps that reach the deleted-unit
// routes.
const deletedUnitCoveringTest = "TestCLIs (pulumiRestoreSteps, tofuRestoreSteps)"

// deletedUnitAllowEntries returns the allowlist rows for the deleted-unit
// routes of the inventory API (ADR 47). No CLI calls them; the harness steps
// call them between CLI commands that delete and read the units.
func deletedUnitAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/namespaces/{namespace}/deleted-units",
			CoveringTest: deletedUnitCoveringTest,
			Reason: "Console-only: no CLI lists deleted units. The harness " +
				"steps list them after the CLI deletes a stack and a workspace.",
		},
		{
			Method: http.MethodPost, Path: "/api/inventory/deleted-units/{tool}/{unitID}/restore",
			CoveringTest: deletedUnitCoveringTest,
			Reason: "Console-only: no CLI restores a deleted unit. The harness " +
				"steps restore one, and the CLI then reads the restored unit.",
		},
		{
			Method: http.MethodDelete, Path: "/api/inventory/deleted-units/{tool}/{unitID}",
			CoveringTest: deletedUnitCoveringTest,
			Reason: "Console-only: no CLI purges a deleted unit. The harness " +
				"steps purge one after the CLI deletes it.",
		},
		{
			Method: http.MethodPost, Path: "/api/server/deleted-units/sweep",
			CoveringTest: deletedUnitCoveringTest,
			Reason: "Operator route: no CLI runs the retention sweep. The harness " +
				"steps run it while a unit is in retention and after a purge.",
		},
	}
}

// auditReadAllowEntries returns the allowlist rows for the audit read
// routes, which only the console and audit sink receivers call.
func auditReadAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/audit/entries",
			CoveringTest: "TestCLIs (checkAuditLog)",
			Reason: "Console-only: no CLI reads the audit entries. The " +
				"harness step lists them after a CLI command that calls the " +
				"decrypt audit route and checks the chain.",
		},
		{
			Method: http.MethodGet, Path: "/api/audit/entries/{sequence}",
			CoveringTest: "TestCLIs (checkAuditLog), TestServerAuditScope",
			Reason: "Console-only and for audit sink receivers: no CLI reads " +
				"one audit entry. The harness steps read an entry of the " +
				"namespace and of @server by its sequence.",
		},
		{
			Method: http.MethodGet, Path: "/api/audit/check",
			CoveringTest: "TestCLIs (checkAuditLog)",
			Reason: "Console-only: no CLI checks the audit chain. The " +
				"harness step checks it after a CLI command that calls the " +
				"decrypt audit route.",
		},
		{
			Method: http.MethodGet, Path: "/api/audit/export",
			CoveringTest: "TestCLIs (checkAuditExport)",
			Reason: "Console-only and for auditors: no CLI exports the audit " +
				"log. The harness step exports the log after a CLI command " +
				"that calls the decrypt audit route and checks the hashes " +
				"of the lines with no help from the server.",
		},
	}
}

// consoleAllowEntries returns the allowlist rows for console-only routes and
// routes the CLI never calls.
func consoleAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/namespaces/{namespace}/units",
			CoveringTest: "TestListUnitsBothTools",
			Reason:       "Console-only: no CLI reads the cross-tool unit list.",
		},
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/namespaces/-/units/{unitID}",
			CoveringTest: "TestCLIs (checkInventoryUnitByID)",
			Reason: "Console-only: no CLI reads a unit by its ID. The harness " +
				"steps read a stack and a workspace by ID.",
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
			Method: http.MethodGet, Path: "/api/server/components",
			CoveringTest: "TestBothToolsSameServer (checkServerComponents)",
			Reason:       "Operator route: no CLI calls it.",
		},
		{
			Method: http.MethodGet, Path: "/api/server/worker",
			CoveringTest: "TestBothToolsSameServer (checkServerComponents)",
			Reason:       "Operator route: no CLI calls it.",
		},
		{
			Method: http.MethodPost, Path: "/api/server/audit/checkpoint-epochs",
			CoveringTest: "TestNewCheckpointEpochIsAChange",
			Reason: "Operator route: no CLI calls it. An administrator starts a new " +
				"checkpoint epoch after a restore or a failed check of the signed checkpoints.",
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

// tofuStateAtAllowEntries returns the OpenTofu console route of the state at a
// time, which no CLI calls. The harness step of the masked scenario drives it.
func tofuStateAtAllowEntries() []AllowEntry {
	const covering = "TestCLIs (tofuMaskedStateScenario)"

	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/v2/workspaces/{workspace_id}/state-at/{at}",
			CoveringTest: covering,
			Reason: "Console route: the CLI never reads the state at a time. " +
				"The harness step reads it between two applies and before the first, " +
				"and checks the state version, the masking and the 404 text.",
		},
	}
}

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
			Method: http.MethodGet, Path: "/api/stacks/{namespace}/{project}/{stack}/state-at/{at}/resources",
			CoveringTest: "TestCLIs (checkMaskedStateAt)",
			Reason: "Console route: the CLI never reads the state at a time. " +
				"The harness step reads the list between two ups and before the first, " +
				"checks the 404 text, and reads it while a held update runs.",
		},
		{
			Method: http.MethodPost, Path: "/api/stacks/{namespace}/{project}/{stack}/state/{version}/reveal",
			CoveringTest: "TestCLIs (checkMaskedReveal)",
			Reason: "Console route (ADR 41): the CLI never reveals a secret value. " +
				"The harness step reveals one masked output after two ups, checks the value, " +
				"no-store and the reveal audit entry, and checks that a path outside the " +
				"masked set answers 404.",
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
			Method: http.MethodPost, Path: "/api/v2/state-versions/{state_version_id}/actions/reveal",
			CoveringTest: "TestCLIs (maskedRevealCheck)",
			Reason: "Console route (ADR 41): the CLI never reveals a secret value. " +
				"The harness step reveals the sensitive input after two applies, checks the value, " +
				"no-store and the reveal audit entry, and checks that a path outside the " +
				"masked set answers 404.",
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

// namespaceAllowEntries returns the operator namespace routes that no CLI
// calls. The harness creates the first namespace through the create on an
// empty installation, and the scenario steps prove the duplicate, invalid
// and unauthenticated cases. The harness lists the namespaces through the
// list, which no CLI calls either.
func namespaceAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodPost, Path: "/api/server/namespaces",
			CoveringTest: namespaceCoveringTest,
			Reason: "Operator route: no CLI creates a namespace. The harness " +
				"creates the first one on an empty installation and the scenario " +
				"checks the duplicate, invalid and unauthenticated cases.",
		},
		{
			Method: http.MethodGet, Path: "/api/server/namespaces",
			CoveringTest: "TestRolesAndGrants",
			Reason: "Operator and console route: no CLI lists namespaces. The harness " +
				"step lists them as the admin and as the member and pages through them.",
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

// storeCheckCoveringTest names the test that proves the store check routes
// with the binary command: no CLI of the matrix calls them.
const storeCheckCoveringTest = "TestStoreCheck"

// storeCheckAllowEntries returns the store check routes. The opendustpan
// store check binary calls them with Go-http-client, which never counts as CLI
// traffic, so the steps of storeCheckCoveringTest drive them.
func storeCheckAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodPost, Path: "/api/dustpan/v1/store-checks",
			CoveringTest: storeCheckCoveringTest,
			Reason: "Operator route: no CLI of the matrix starts a store check. The opendustpan store " +
				"check binary starts one, and the harness steps check the cooldown and the refusal of a target " +
				"that is not on the list.",
		},
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/store-checks",
			CoveringTest: storeCheckCoveringTest,
			Reason: "Operator route: no CLI of the matrix lists store checks. The harness step lists them " +
				"newest first.",
		},
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/store-checks/{id}",
			CoveringTest: storeCheckCoveringTest,
			Reason: "Operator route: no CLI of the matrix reads a store check. The opendustpan store check " +
				"binary polls it until the check ends, and the harness step reads the report.",
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

// auditChainCoveringTest names the test that proves the audit chain routes
// instead of a CLI: no tool CLI calls them.
const auditChainCoveringTest = "TestAuditChain"

// auditChainAllowEntries returns the operator audit chain routes that no
// tool CLI calls. The steps of auditChainCoveringTest restart the server
// binary with each setting, read the waiting change and approve it.
func auditChainAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/server/audit/chain",
			CoveringTest: auditChainCoveringTest,
			Reason: "Operator route: no tool CLI reads the audit chain. " +
				"The harness step reads the waiting change after each restart.",
		},
		{
			Method: http.MethodPost, Path: "/api/server/audit/chain",
			CoveringTest: auditChainCoveringTest,
			Reason: "Operator route: no tool CLI approves an audit chain change. " +
				"The harness step approves the change to off, then to on.",
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
		{
			Method: http.MethodPost, Path: "/api/server/restores/{id}/cancel",
			CoveringTest: "TestCancelRestoreBeforeTheSwap",
			Reason: "Operator route: no CLI command cancels a restore. The tests of the installation API " +
				"cancel a restore before the swap, after the swap and after the end.",
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

// SetOverrides is not used by this fake.
func (coverageWorkspaces) SetOverrides(
	context.Context, workspace.ID, namespace.Overrides,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrNotFound
}

func (coverageWorkspaces) forceUnlockUnfenced(
	_ context.Context,
	_ workspace.ID,
	_ string,
) (workspace.Workspace, error) {
	var none workspace.Workspace

	return none, workspace.ErrNotFound
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

// reviewCoveringTest names the test that proves the access-review and
// caller-access routes instead of a CLI: no CLI calls them (ADR 52).
const reviewCoveringTest = "TestAccessReview"

// reviewAllowEntries returns the operator and console routes of the access
// review and the access of the caller. The harness steps of
// reviewCoveringTest drive each one through the real binary on each store.
func reviewAllowEntries() []AllowEntry {
	reason := "Operator and console route (ADR 52): no CLI reads the access review or the caller access. " +
		"The harness step calls it through the real binary and checks the answer."

	paths := []string{
		"/api/server/access-review",
		"/api/server/namespaces/{namespace}/access-review",
		"/api/server/namespaces/{namespace}/collections/{collection}/access-review",
		"/api/server/namespaces/{namespace}/unit-access-review",
		"/api/server/caller/access",
		"/api/server/caller/grants",
	}

	out := make([]AllowEntry, 0, len(paths))
	for _, path := range paths {
		out = append(out, AllowEntry{
			Method: http.MethodGet, Path: path, CoveringTest: reviewCoveringTest, Reason: reason,
		})
	}

	return out
}

// manualLockAllowEntries returns the lock routes of a namespace. The real CLIs
// never call them: the steps of TestManualLockOnNamespaceStopsTheChangeRoutes
// take, read, remove and force-remove the lock through the API, and drive both
// CLIs against it. That test writes no matrix report, so the coverage check must
// not depend on the order in which the tests finish. The routes of a unit and
// of a collection need no entry: the matrix scenario of the OpenTofu suite
// reaches them.
func manualLockAllowEntries() []AllowEntry {
	const (
		path = "/api/dustpan/v1/namespaces/{namespace}/lock"
		test = "TestManualLockOnNamespaceStopsTheChangeRoutes"
	)

	reason := "Console and client route: no CLI takes a manual lock. The harness steps of " +
		test + " take, read and end the lock through the API and run both CLIs against it."

	return []AllowEntry{
		{Method: http.MethodPut, Path: path, CoveringTest: test, Reason: reason},
		{Method: http.MethodGet, Path: path, CoveringTest: test, Reason: reason},
		{Method: http.MethodDelete, Path: path, CoveringTest: test, Reason: reason},
		{Method: http.MethodPost, Path: path + "/force-unlock", CoveringTest: test, Reason: reason},
	}
}

// jobAllowEntries covers the routes that start and read a job. No CLI calls
// them: the console and the clients of the API do. The harness steps of
// TestJobsAPI start a plan job, start it again with the same key, refuse
// starts that are not valid, and read a job, a job that the caller may not
// read and a job that does not exist through the API.
func jobAllowEntries() []AllowEntry {
	const (
		test = "TestJobsAPI"
		why  = "Console and client route: no CLI calls it. The harness steps of TestJobsAPI call it through the API."
	)

	return []AllowEntry{
		{Method: http.MethodPost, Path: "/api/dustpan/v1/namespaces/{namespace}/jobs", CoveringTest: test, Reason: why},
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}",
			CoveringTest: test, Reason: why,
		},
	}
}

// triggerAllowEntries covers the routes of the run triggers. No CLI calls them:
// the console and the clients of the API do. The steps of TestRunTriggers call
// each of them with the personal token of a person, on a unit that the real
// OpenTofu CLI made.
func triggerAllowEntries() []AllowEntry {
	const (
		base = "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers"
		one  = base + "/{trigger_id}"
	)

	return allowRoutes(
		"TestRunTriggers",
		"Console and client route: no CLI calls it. The steps of TestRunTriggers call it with the token of a person.",
		RouteRef{Method: http.MethodGet, Path: base},
		RouteRef{Method: http.MethodPost, Path: base},
		RouteRef{Method: http.MethodGet, Path: one},
		RouteRef{Method: http.MethodPut, Path: one},
		RouteRef{Method: http.MethodDelete, Path: one},
		RouteRef{Method: http.MethodPost, Path: one + "/fire"},
	)
}

// unitSettingsAllowEntries covers the routes of the settings of a unit. No CLI
// calls them: the console and the clients of the API do. The steps of
// TestUnitSettingsAPI call each of them with the token of a person, on a unit
// that the real OpenTofu CLI made.
func unitSettingsAllowEntries() []AllowEntry {
	const (
		unit = "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/settings"
		base = "/api/dustpan/v1/namespaces/{namespace}"
	)

	return allowRoutes(
		"TestUnitSettingsAPI",
		"Console and client route: no CLI calls it. The steps of TestUnitSettingsAPI call it with the token of a person.",
		RouteRef{Method: http.MethodGet, Path: unit},
		RouteRef{Method: http.MethodPut, Path: unit},
		RouteRef{Method: http.MethodDelete, Path: unit},
		RouteRef{Method: http.MethodGet, Path: unit + "/suggestions"},
		RouteRef{Method: http.MethodGet, Path: base + "/settings/unit-defaults"},
		RouteRef{Method: http.MethodPut, Path: base + "/settings/unit-defaults"},
		RouteRef{Method: http.MethodPost, Path: base + "/unit-settings/bulk-put"},
	)
}

// placementAllowEntries returns the routes of the placement rules of a
// namespace. No CLI reads or writes a rule: the console and the clients of the
// API do. The steps of TestPlacementRuleAPI call each of them with the token of
// a person, and a denial step proves the action that each route needs.
func placementAllowEntries() []AllowEntry {
	const (
		rules = "/api/dustpan/v1/namespaces/{namespace}/placement-rules"
		one   = rules + "/{rule}"
	)

	return allowRoutes(
		"TestPlacementRuleAPI",
		"Console and client route: no CLI manages placement rules. The steps of TestPlacementRuleAPI call it with the token of a person.",
		RouteRef{Method: http.MethodGet, Path: rules},
		RouteRef{Method: http.MethodPost, Path: rules},
		RouteRef{Method: http.MethodGet, Path: one},
		RouteRef{Method: http.MethodPut, Path: one},
		RouteRef{Method: http.MethodDelete, Path: one},
	)
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

// driftAllowEntries covers the routes that start a drift check and read the
// latest drift result of a unit. No CLI calls them: the console and the clients
// of the API do. The harness steps of TestDriftAPI call each of them through
// the API: a read before any check, a start, the same start again, the refusals
// of a start, and the reads of a person who may not read the unit.
func driftAllowEntries() []AllowEntry {
	const (
		test = "TestDriftAPI"
		why  = "Console and client route: no CLI calls it. The harness steps of TestDriftAPI call it through the API."
	)

	return []AllowEntry{
		{
			Method: http.MethodPost, Path: "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift-checks",
			CoveringTest: test, Reason: why,
		},
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift",
			CoveringTest: test, Reason: why,
		},
	}
}

// outputSharingAllowEntries covers the routes of the output sharing setting
// of a namespace. No CLI calls them: the console and the clients of the API
// do. The setting step of TestOutputSharingRules reads the setting of a new
// namespace, refuses a change by a viewer and a value that is not valid, and
// changes it as the admin. The reader steps of the same test read the outputs
// of a unit with a principal that holds unit:read-outputs only.
func outputSharingAllowEntries() []AllowEntry {
	const (
		path = "/api/dustpan/v1/namespaces/{namespace}/output-sharing"
		test = "TestOutputSharingRules"
		why  = "Console and client route: no CLI calls it. The setting step of " + test +
			" reads and changes the setting through the API."
	)

	return []AllowEntry{
		{Method: http.MethodGet, Path: path, CoveringTest: test, Reason: why},
		{Method: http.MethodPut, Path: path, CoveringTest: test, Reason: why},
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/outputs",
			CoveringTest: test, Reason: "Console and client route: no CLI calls it. The reader steps of " + test +
				" read the outputs of a stack and of a workspace through the API.",
		},
	}
}

// sourceFilterAllowEntries covers the routes of the source filter of a
// namespace. No CLI calls them: the console and the clients of the API do. The
// steps of TestSourceFilterAPI call both with the token of an administrator.
func sourceFilterAllowEntries() []AllowEntry {
	const (
		path = "/api/dustpan/v1/namespaces/{namespace}/source-filter"
		test = "TestSourceFilterAPI"
		why  = "Console and client route: no CLI calls it. The steps of TestSourceFilterAPI call it through the API."
	)

	return []AllowEntry{
		{Method: http.MethodGet, Path: path, CoveringTest: test, Reason: why},
		{Method: http.MethodPut, Path: path, CoveringTest: test, Reason: why},
	}
}

// trustRuleAllowEntries covers the routes of the workload trust rules of a
// namespace. No CLI calls them: the console and the clients of the API do.
// The steps of TestTrustRuleAPI call each of them with the token of an
// administrator.
func trustRuleAllowEntries() []AllowEntry {
	const (
		rules = "/api/dustpan/v1/namespaces/{namespace}/trust-rules"
		rule  = rules + "/{rule_id}"
		test  = "TestTrustRuleAPI"
		why   = "Console and client route: no CLI calls it. The steps of TestTrustRuleAPI call it through the API."
	)

	return []AllowEntry{
		{Method: http.MethodGet, Path: rules, CoveringTest: test, Reason: why},
		{Method: http.MethodPost, Path: rules, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: rule, CoveringTest: test, Reason: why},
		{Method: http.MethodDelete, Path: rule, CoveringTest: test, Reason: why},
		{
			Method: http.MethodPost, Path: rules + "/exchange", CoveringTest: "TestWorkloadTokenExchange",
			Reason: "Workload route: a CI job calls it with the token of its platform. The steps of " +
				"TestWorkloadTokenExchange call it with tokens of a fake issuer.",
		},
	}
}

// savedViewAllowEntries covers the routes of the saved views. No CLI calls
// them: the console and the clients of the API do. The steps of
// TestSavedViewsAPI call each of them with the personal token of a person.
func savedViewAllowEntries() []AllowEntry {
	const (
		base = "/api/dustpan/v1/namespaces/{namespace}/views"
		test = "TestSavedViewsAPI"
		why  = "Console and client route: no CLI calls it. The steps of TestSavedViewsAPI call it with the token of a person."
	)

	return []AllowEntry{
		{Method: http.MethodPost, Path: base, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: base, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: base + "/{view_id}", CoveringTest: test, Reason: why},
		{Method: http.MethodDelete, Path: base + "/{view_id}", CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: base + "/{view_id}/units", CoveringTest: test, Reason: why},
	}
}

// sinkAllowEntries covers the routes of the notification sinks. No CLI calls
// them: the console and the clients of the API do. The steps of
// TestNotificationSinksAPI call each of them with the token of an administrator.
func sinkAllowEntries() []AllowEntry {
	const (
		base = "/api/dustpan/v1/namespaces/{namespace}/sinks"
		test = "TestNotificationSinksAPI"
		why  = "Console and client route: no CLI calls it. The steps of TestNotificationSinksAPI call it through the API."
	)

	return []AllowEntry{
		{Method: http.MethodPost, Path: base, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: base, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: base + "/{sink_id}", CoveringTest: test, Reason: why},
		{Method: http.MethodPut, Path: base + "/{sink_id}", CoveringTest: test, Reason: why},
		{Method: http.MethodDelete, Path: base + "/{sink_id}", CoveringTest: test, Reason: why},
		{Method: http.MethodPost, Path: base + "/{sink_id}/test", CoveringTest: test, Reason: why},
	}
}

// coverageViews returns the saved view config for the route table. The
// services are empty shells: they exist only to build a Handler.
func coverageViews(tb testing.TB) *inventoryapi.ViewsConfig {
	tb.Helper()

	service, err := savedview.NewService(savedview.Config{
		Store: struct{ savedview.Store }{nil}, Namespaces: coverageNamespaceStore{}, Now: nil,
	})
	if err != nil {
		tb.Fatalf("saved view service: %v", err)
	}

	return &inventoryapi.ViewsConfig{
		Service: service, Principals: struct{ inventoryapi.ViewPrincipals }{nil}, Idempotency: nil,
	}
}

// gitHostAllowEntries covers the routes of the Git host connections. No CLI
// calls them: the console and the clients of the API do. The steps of
// TestGitHostConnections call each of them as the operator and as a namespace
// administrator.
func gitHostAllowEntries() []AllowEntry {
	const (
		app  = "/api/dustpan/v1/git-host-apps/{kind}"
		inst = "/api/dustpan/v1/git-host-installations"
		conn = "/api/dustpan/v1/namespaces/{namespace}/git-host-connections"
		test = "TestGitHostConnections"
		why  = "Console and client route: no CLI calls it. The steps of TestGitHostConnections call it."
	)

	return []AllowEntry{
		{Method: http.MethodGet, Path: app, CoveringTest: test, Reason: why},
		{Method: http.MethodPut, Path: app, CoveringTest: test, Reason: why},
		{Method: http.MethodDelete, Path: app, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: inst, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: inst + "/{installation_id}", CoveringTest: test, Reason: why},
		{Method: http.MethodPut, Path: inst + "/{installation_id}", CoveringTest: test, Reason: why},
		{Method: http.MethodDelete, Path: inst + "/{installation_id}", CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: conn, CoveringTest: test, Reason: why},
		{Method: http.MethodPost, Path: conn, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: conn + "/{id}", CoveringTest: test, Reason: why},
		{Method: http.MethodDelete, Path: conn + "/{id}", CoveringTest: test, Reason: why},
		{Method: http.MethodPost, Path: conn + "/{id}/make-default", CoveringTest: test, Reason: why},
		{Method: http.MethodPost, Path: conn + "/{id}/check", CoveringTest: test, Reason: why},
	}
}

// coverageGitHost returns a Git host service for the route table. Its parts
// are empty shells: they exist only to build a Handler.
func coverageGitHost(tb testing.TB) *forge.Service {
	tb.Helper()

	svc, err := forge.NewService(forge.Config{
		Store: struct{ forge.Store }{nil}, Namespaces: coverageNamespaceStore{},
		Secrets: struct{ forge.SecretSealer }{nil}, Audit: struct{ audit.Store }{nil},
		Openers:  []forge.Opener{coverageOpener{Opener: nil}},
		CheckURL: func(string) error { return nil }, OnAuditFailure: nil, OnRefused: nil, CheckLimits: nil, Now: nil,
	})
	if err != nil {
		tb.Fatalf("Git host service: %v", err)
	}

	return svc
}

// coverageOpener is an Opener that only names its kind.
type coverageOpener struct{ forge.Opener }

func (coverageOpener) Kind() forge.Kind { return forge.KindGitHub }

// coverageRegistryService answers nothing. It exists only to build a Handler
// for its route table.
type coverageRegistryService struct{}

func (coverageRegistryService) ServableReleases(context.Context, namespace.Name, string) ([]registry.Release, error) {
	return nil, registry.ErrNotFound
}

func (coverageRegistryService) Release(context.Context, namespace.Name, string, string) (registry.Release, error) {
	return registry.Release{}, registry.ErrNotFound
}

func (coverageRegistryService) SigningKey(context.Context, registry.Release) (registry.TrustedKey, error) {
	return registry.TrustedKey{}, registry.ErrKeyNotFound
}

func (coverageRegistryService) OpenFile(
	context.Context, namespace.Name, string, string, string,
) (int64, io.ReadCloser, error) {
	return 0, nil, registry.ErrNotFound
}

func (coverageRegistryService) ModuleReleases(
	context.Context, registry.Address,
) ([]registry.Release, error) {
	return nil, registry.ErrNotFound
}

func (coverageRegistryService) ModuleRelease(
	context.Context, namespace.Name, string, string, string,
) (registry.Release, error) {
	return registry.Release{}, registry.ErrNotFound
}

func (coverageRegistryService) OpenModuleFile(
	context.Context, registry.Address, string, string,
) (int64, io.ReadCloser, error) {
	return 0, nil, registry.ErrNotFound
}

// coverageRegistryRoutes returns the route table of the registry API. The
// service is an empty shell: it exists only to build a Handler.
func coverageRegistryRoutes(tb testing.TB) []RouteRef {
	tb.Helper()

	store := struct{ registry.Store }{nil}
	keys := struct{ registry.KeyStore }{nil}
	verifier := struct {
		registry.SignatureVerifier
		registry.KeyInspector
	}{nil, nil}

	svc, err := registryservice.New(registryservice.Config{
		Store: store, Keys: keys, Verifier: verifier, Inspector: verifier, Blobs: struct{ blob.Store }{nil},
		Namespaces: coverageRegistryNamespaces{}, Now: nil, MaxArchiveBytes: 0, MaxReleaseBytes: 0,
		MaxModuleArchiveBytes: 0,
	})
	if err != nil {
		tb.Fatalf("registry service: %v", err)
	}

	handler, err := registryapi.NewHandler(registryapi.Config{
		Service:        svc,
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		MaxUploadBytes: registryservice.DefaultMaxArchiveBytes,
		Audit:          coverageAuditStore{},
		Namespaces:     nil,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Idempotency:    nil,
		Logger:         slog.New(slog.DiscardHandler),
	})
	if err != nil {
		tb.Fatalf("registry handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// coverageRegistryNamespaces holds no namespace.
type coverageRegistryNamespaces struct{}

func (coverageRegistryNamespaces) GetNamespace(context.Context, namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{}, namespace.ErrNotFound
}

// registryAllowEntries covers the routes of the registry API. No CLI calls
// them: a CI job or a client of the API does. The steps of TestRegistryTofuInit
// call each of them, and OpenTofu then installs what they published.
func registryAllowEntries() []AllowEntry {
	const (
		base = "/api/dustpan/v1/namespaces/{namespace}/registry"
		test = "TestRegistryTofuInit"
		why  = "Client route: no CLI calls it. The steps of TestRegistryTofuInit call it, and OpenTofu installs what it published."

		moduleTest = "TestRegistryModuleTofuInit"
		moduleWhy  = "Client route: no CLI calls it. The steps of TestRegistryModuleTofuInit call it, and OpenTofu downloads what it published."

		moduleProtocol    = "/v1/modules/{namespace}/{name}/{system}"
		moduleProtocolWhy = "OpenTofu calls it on init, but only on the registry host. The steps of TestRegistryModuleTofuInit run the real CLI against it."

		protocol    = "/v1/providers/{namespace}/{type}"
		protocolWhy = "OpenTofu calls it on init, but only on the registry host. The steps of TestRegistryTofuInit run the real CLI against it."
	)

	return []AllowEntry{
		{Method: http.MethodPost, Path: base + "/uploads", CoveringTest: test, Reason: why},
		{Method: http.MethodPut, Path: base + "/providers/{name}/versions/{version}", CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: base + "/providers", CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: base + "/providers/{name}/versions", CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: base + "/providers/{name}/versions/{version}", CoveringTest: test, Reason: why},
		{
			Method:       http.MethodPut,
			Path:         base + "/modules/{name}/{system}/versions/{version}",
			CoveringTest: moduleTest,
			Reason:       moduleWhy,
		},
		{Method: http.MethodGet, Path: base + "/modules", CoveringTest: moduleTest, Reason: moduleWhy},
		{
			Method:       http.MethodGet,
			Path:         base + "/modules/{name}/{system}/versions",
			CoveringTest: moduleTest,
			Reason:       moduleWhy,
		},
		{
			Method:       http.MethodGet,
			Path:         base + "/modules/{name}/{system}/versions/{version}",
			CoveringTest: moduleTest,
			Reason:       moduleWhy,
		},
		{Method: http.MethodPost, Path: base + "/keys", CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: base + "/keys", CoveringTest: test, Reason: why},
		{Method: http.MethodDelete, Path: base + "/keys/{fingerprint}", CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: protocol + "/versions", CoveringTest: test, Reason: protocolWhy},
		{
			Method:       http.MethodGet,
			Path:         protocol + "/{version}/download/{os}/{arch}",
			CoveringTest: test,
			Reason:       protocolWhy,
		},
		{Method: http.MethodGet, Path: protocol + "/{version}/files/{sha256}", CoveringTest: test, Reason: protocolWhy},
		{
			Method:       http.MethodGet,
			Path:         moduleProtocol + "/versions",
			CoveringTest: moduleTest,
			Reason:       moduleProtocolWhy,
		},
		{
			Method:       http.MethodGet,
			Path:         moduleProtocol + "/{version}/download",
			CoveringTest: moduleTest,
			Reason:       moduleProtocolWhy,
		},
		{
			Method:       http.MethodGet,
			Path:         moduleProtocol + "/{version}/files/{sha256}",
			CoveringTest: moduleTest,
			Reason:       moduleProtocolWhy,
		},
	}
}
