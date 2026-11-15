//go:build e2e

package e2e

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	tofuhttpapi "opendustpan.org/opendustpan/internal/api/tofuhttp"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

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
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         nil,
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

func (coverageTofuHTTPWorkspaces) Create(
	_ context.Context,
	_ workspace.Ref,
	_ workspace.Placement,
) (workspace.Workspace, error) {
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

func (coverageTofuHTTPWorkspaces) RepeatsRelease(_ workspace.Workspace, _ workspace.ReleaseRepeat) bool {
	return false
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
