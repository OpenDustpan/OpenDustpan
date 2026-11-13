//go:build e2e

package e2e

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	tfeapi "opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// coverageTFERoutes returns the TFE-compatible adapter's route table.
func coverageTFERoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	handler, err := tfeapi.NewHandler(tfeapi.Config{
		Sessions:       nil,
		SessionPolicy:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         nil,
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

// coverageWorkspaces answers no workspace and no state. It exists only to
// build a Handler for its route table; no request is ever served.
type coverageWorkspaces struct{}

func (coverageWorkspaces) Delete(_ context.Context, _ workspace.Ref, _ audit.Entry) error {
	return workspace.ErrNotFound
}

func (coverageWorkspaces) Create(
	_ context.Context,
	_ workspace.Ref,
	_ workspace.Placement,
) (workspace.Workspace, error) {
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

func (coverageWorkspaces) ReadRollbackChange(
	_ context.Context, _ workspace.ID, _ workspace.StateVersionID,
) (workspace.RollbackChange, error) {
	var none workspace.RollbackChange

	return none, workspace.ErrStateVersionNotFound
}

func (coverageWorkspaces) RestoreExpecting(
	_ context.Context, _ workspace.ID, _ workspace.StateVersionID, _ string, _ workspace.StateVersionID,
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

// tfeRefs formats the TFE-compatible table as method and path pairs.
func tfeRefs(routes []tfeapi.Route) []RouteRef {
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

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
