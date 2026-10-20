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

	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
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

// TestRefusedOverrideIsAudited proves the domain layer of the denial audit: a
// forced declare that the workspace refuses answers 403 and writes a denied
// entry with the forced-write action, the unit and the kind override.
func TestRefusedOverrideIsAudited(t *testing.T) {
	t.Parallel()

	var seen namespace.Overrides

	entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}

	handler, err := tfe.NewHandler(tfe.Config{
		Authenticator:  tokenAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     forcedDeclareWorkspaces{stubWorkspaces: stubWorkspaces{}, seen: &seen},
		BaseURL:        baseurl.Resolver{},
		Audit:          entries,
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

	entries.mu.Lock()
	defer entries.mu.Unlock()

	var denied []audit.Entry

	for _, e := range entries.entries {
		if e.Outcome == audit.OutcomeDenied {
			denied = append(denied, e)
		}
	}

	if len(denied) != 1 {
		t.Fatalf("denied entries = %+v, want one", denied)
	}

	if e := denied[0]; e.Action != string(identity.ActionUnitWriteForced) || e.Kind != audit.KindOverride ||
		e.Subject != "tester" || e.Object == "" {
		t.Fatalf("entry = %+v, want the forced-write action, kind override, the caller and the unit", e)
	}
}
