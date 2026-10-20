package tfe_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// forcedDeclareWorkspaces refuses a forced declare the way the domain does when
// the caller holds no forced override, and records the override it received.
type forcedDeclareWorkspaces struct {
	stubWorkspaces

	seen *namespace.Overrides
}

func (w forcedDeclareWorkspaces) BeginState(
	_ context.Context, _ workspace.ID, _ string, in workspace.NewState, auth workspace.Authority,
) (workspace.PendingState, error) {
	*w.seen = auth.Allow

	if in.Force && !auth.Allow.Forced {
		return workspace.PendingState{}, fmt.Errorf(
			"new state: %w: a forced write needs the forced-write switch on for the namespace (`PUT /api/server/namespaces/{namespace}/override-switches`) and for the workspace (`PUT /api/v2/workspaces/{id}/override-switches`), and the permission to write forced state (`unit:write-forced`)",
			workspace.ErrOverrideRefused,
		)
	}

	return workspace.PendingState{}, workspace.ErrNotLocked
}

// TestForcedDeclareWithoutOverrideIsRefused proves a client's force attribute
// reaches the domain with no forced override while the switches are off, and
// the 403 names the action (ADR 44, decisions 4, 6 and 8).
func TestForcedDeclareWithoutOverrideIsRefused(t *testing.T) {
	t.Parallel()

	var seen namespace.Overrides

	handler, err := tfe.NewHandler(tfe.Config{
		Authenticator:  tokenAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     forcedDeclareWorkspaces{stubWorkspaces: stubWorkspaces{}, seen: &seen},
		BaseURL:        baseurl.Resolver{},
		Audit:          nil,
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	const body = `{"data":{"type":"state-versions","attributes":{"serial":4,` +
		`"lineage":"seed-lineage","md5":"d41d8cd98f00b204e9800998ecf8427e","force":true}}}`

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, createStateRequest(t, "/api/v2/workspaces/ws-test123/state-versions", body))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("forced create without the override answers %d, want 403: %s", rec.Code, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), "unit:write-forced") {
		t.Fatalf("403 does not name the action: %s", rec.Body.String())
	}

	if seen.Forced {
		t.Fatal("the domain received a forced override the caller does not hold")
	}
}
