//go:build e2e

package e2e

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	inventoryapi "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/outputsharesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/savedview"
)

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
		StateGuard: stategate.Off("the test keeps no gates"),
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

func (coverageCollectionStore) ListCollectionUnitIDs(_ context.Context, _ collection.Ref, _ string) ([]string, error) {
	return nil, nil
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

// inventoryRefs formats the inventory table as method and path pairs.
func inventoryRefs(routes []inventoryapi.Route) []RouteRef {
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
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
			Method: http.MethodPost, Path: "/api/dustpan/v1/deleted-units/sweep",
			CoveringTest: deletedUnitCoveringTest,
			Reason: "Operator route: no CLI runs the retention sweep. The harness " +
				"steps run it while a unit is in retention and after a purge.",
		},
	}
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
