package tfe_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// switchedNamespaceStore holds the acme namespace with its switches on.
type switchedNamespaceStore struct {
	stubNamespaceStore
}

func (switchedNamespaceStore) GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	ns, err := stubNamespaceStore{}.GetNamespace(ctx, name)
	if err != nil {
		return namespace.Namespace{}, err
	}

	ns.Overrides = namespace.Overrides{Forced: true, Unlocked: true}

	return ns, nil
}

// switchedWorkspaces holds the test unit with its switches on, records the
// override that each declare receives, and records each switch change.
type switchedWorkspaces struct {
	stubWorkspaces

	mu      *sync.Mutex
	seen    *[]namespace.Overrides
	changes *int
}

func (w switchedWorkspaces) GetByID(ctx context.Context, id workspace.ID) (workspace.Workspace, error) {
	ws, err := w.stubWorkspaces.GetByID(ctx, id)
	if err != nil {
		return workspace.Workspace{}, err
	}

	ws.Overrides = namespace.Overrides{Forced: true, Unlocked: true}

	return ws, nil
}

func (w switchedWorkspaces) BeginState(
	_ context.Context, _ workspace.ID, _ string, in workspace.NewState, auth workspace.Authority,
) (workspace.PendingState, error) {
	w.mu.Lock()
	*w.seen = append(*w.seen, auth.Allow)
	w.mu.Unlock()

	if in.Force && !auth.Allow.Forced {
		return workspace.PendingState{}, fmt.Errorf(
			"new state: %w: a forced write needs the forced-write switch on for the namespace (`PUT /api/server/namespaces/{namespace}/override-switches`) and for the workspace (`PUT /api/v2/workspaces/{id}/override-switches`), and the permission to write forced state (`unit:write-forced`)",
			workspace.ErrOverrideRefused,
		)
	}

	return workspace.PendingState{}, workspace.ErrNotLocked
}

func (w switchedWorkspaces) SetOverrides(
	ctx context.Context, id workspace.ID, ov namespace.Overrides,
) (workspace.Workspace, error) {
	w.mu.Lock()
	*w.changes++
	w.mu.Unlock()

	ws, err := w.GetByID(ctx, id)
	if err != nil {
		return workspace.Workspace{}, err
	}

	ws.Overrides = ov

	return ws, nil
}

// newSwitchedHandler returns a handler over the switched fakes and the
// authorizer that the test chooses.
func newSwitchedHandler(
	t *testing.T, authz selectiveAuthorizer, ws switchedWorkspaces,
) *tfe.Handler {
	t.Helper()

	svc, err := namespace.NewService(
		namespace.Config{
			Store:        switchedNamespaceStore{stubNamespaceStore: stubNamespaceStore{}},
			Now:          nil,
			Installation: "test-region",
		},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     authz,
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     svc,
		Workspaces:     ws,
		OutputChecker:  nil,
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

	return handler
}

const forcedCreateBody = `{"data":{"type":"state-versions","attributes":{"serial":4,` +
	`"lineage":"seed-lineage","md5":"d41d8cd98f00b204e9800998ecf8427e","force":true}}}`

// TestForcedDeclareSwitchOnActionDeniedIsRefused proves that a forced declare
// with both switches on still needs the unit:write-forced action: the domain
// receives no forced override and the 403 names the action (ADR 44, decision 4).
func TestForcedDeclareSwitchOnActionDeniedIsRefused(t *testing.T) {
	t.Parallel()

	var (
		mu      sync.Mutex
		seen    []namespace.Overrides
		changes int
	)

	authz := selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionUnitWriteForced: true}}
	handler := newSwitchedHandler(t, authz, switchedWorkspaces{
		stubWorkspaces: stubWorkspaces{}, mu: &mu, seen: &seen, changes: &changes,
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, createStateRequest(t, "/api/v2/workspaces/ws-test123/state-versions", forcedCreateBody))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("forced create answers %d, want 403: %s", rec.Code, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), "unit:write-forced") {
		t.Fatalf("403 does not name the action: %s", rec.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()

	if len(seen) != 1 || seen[0].Forced {
		t.Fatalf("domain received %+v, want one declare with no forced override", seen)
	}
}

// TestForcedDeclareSwitchOnActionAllowedReachesDomain proves that the same
// forced declare reaches the domain with the forced override when the action
// is allowed and both switches are on.
func TestForcedDeclareSwitchOnActionAllowedReachesDomain(t *testing.T) {
	t.Parallel()

	var (
		mu      sync.Mutex
		seen    []namespace.Overrides
		changes int
	)

	handler := newSwitchedHandler(t, allowAll(), switchedWorkspaces{
		stubWorkspaces: stubWorkspaces{}, mu: &mu, seen: &seen, changes: &changes,
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, createStateRequest(t, "/api/v2/workspaces/ws-test123/state-versions", forcedCreateBody))

	if rec.Code == http.StatusForbidden {
		t.Fatalf("forced create with the action and switches answers 403: %s", rec.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()

	if len(seen) != 1 || !seen[0].Forced {
		t.Fatalf("domain received %+v, want one declare with the forced override", seen)
	}
}

// TestOverrideSwitchesChangeNeedsBothFields proves that a PUT of the unit
// switches answers 400 for every body that does not name both switches, and
// that such a body reaches no switch change (ADR 44, decision 6).
func TestOverrideSwitchesChangeNeedsBothFields(t *testing.T) {
	t.Parallel()

	bodies := []struct {
		name string
		body string
	}{
		{name: "empty object", body: `{}`},
		{name: "null", body: `null`},
		{name: "forced switch only", body: `{"allow-forced-writes":true}`},
		{name: "unlocked switch only", body: `{"allow-unlocked-writes":true}`},
		{name: "unknown field", body: `{"allow-forced-writes":true,"allow-unlocked-writes":true,"x":1}`},
		{name: "two objects", body: `{"allow-forced-writes":true,"allow-unlocked-writes":true}{}`},
	}

	for _, tt := range bodies {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				mu      sync.Mutex
				seen    []namespace.Overrides
				changes int
			)

			handler := newSwitchedHandler(t, allowAll(), switchedWorkspaces{
				stubWorkspaces: stubWorkspaces{}, mu: &mu, seen: &seen, changes: &changes,
			})

			req := workspaceRequest(t, http.MethodPut, "/api/v2/workspaces/ws-test123/override-switches", tt.body)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PUT %s answers %d, want 400: %s", tt.body, rec.Code, rec.Body.String())
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeJSONAPI, http.StatusBadRequest, rec.Body.Bytes(),
				errcodetest.Want{Code: errcode.RequestBodyInvalid, Args: nil, RequestBody: nil})

			mu.Lock()
			defer mu.Unlock()

			if changes != 0 {
				t.Fatalf("PUT %s reached the switch change %d times, want 0", tt.body, changes)
			}
		})
	}
}

// TestOverrideSwitchesChangeWithBothFields proves that a PUT that names both
// switches reaches the switch change and answers 200 with them.
func TestOverrideSwitchesChangeWithBothFields(t *testing.T) {
	t.Parallel()

	var (
		mu      sync.Mutex
		seen    []namespace.Overrides
		changes int
	)

	handler := newSwitchedHandler(t, allowAll(), switchedWorkspaces{
		stubWorkspaces: stubWorkspaces{}, mu: &mu, seen: &seen, changes: &changes,
	})

	req := workspaceRequest(t, http.MethodPut, "/api/v2/workspaces/ws-test123/override-switches",
		`{"allow-forced-writes":true,"allow-unlocked-writes":false}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"allow-forced-writes":true`) {
		t.Fatalf("PUT with both fields answers %d %s, want 200 with forced on", rec.Code, rec.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()

	if changes != 1 {
		t.Fatalf("switch change ran %d times, want 1", changes)
	}
}
