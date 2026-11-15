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
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     forcedDeclareWorkspaces{stubWorkspaces: stubWorkspaces{}, seen: &seen},
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
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     forcedDeclareWorkspaces{stubWorkspaces: stubWorkspaces{}, seen: &seen},
		OutputChecker:  nil,
		BaseURL:        baseurl.Resolver{},
		Audit:          entries,
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

// refusingWorkspaces answers every declare with err.
type refusingWorkspaces struct {
	stubWorkspaces

	err error
}

func (w refusingWorkspaces) BeginState(
	context.Context, workspace.ID, string, workspace.NewState, workspace.Authority,
) (workspace.PendingState, error) {
	return workspace.PendingState{}, w.err
}

// TestRefusedUnlockedWriteIsAudited proves a write without the lock that the
// workspace refuses writes a denied entry with the unlocked-write action when
// the switch is off and when the caller lacks the permission. The refusal still
// answers as a lock conflict. A forced refusal keeps the forced-write action,
// and a plain lock conflict writes no entry.
func TestRefusedUnlockedWriteIsAudited(t *testing.T) {
	t.Parallel()

	const unlockedText = "workspace is not locked: a write without the lock needs the switch and `unit:write-unlocked`"

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantAction string
	}{
		{
			name: "permission missing",
			err: fmt.Errorf("new state: %w", workspace.NewOverrideRefusedError(
				workspace.OverrideUnlocked, unlockedText, workspace.ErrNotLocked,
			)),
			wantStatus: http.StatusConflict,
			wantAction: string(identity.ActionUnitWriteUnlocked),
		},
		{
			name: "switch off",
			err: fmt.Errorf("save: %w", workspace.NewOverrideRefusedError(
				workspace.OverrideUnlocked, "write refused: the unlocked-write switch is off",
			)),
			wantStatus: http.StatusForbidden,
			wantAction: string(identity.ActionUnitWriteUnlocked),
		},
		{
			name: "forced",
			err: workspace.NewOverrideRefusedError(
				workspace.OverrideForced, "write refused: the forced-write switch is off",
			),
			wantStatus: http.StatusForbidden,
			wantAction: string(identity.ActionUnitWriteForced),
		},
		{name: "plain lock conflict", err: workspace.ErrNotLocked, wantStatus: http.StatusConflict, wantAction: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, denied := declareRefused(t, tc.err)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}

			if tc.wantAction == "" {
				if len(denied) != 0 {
					t.Fatalf("denied entries = %+v, want none", denied)
				}

				return
			}

			if len(denied) != 1 || denied[0].Action != tc.wantAction || denied[0].Kind != audit.KindOverride {
				t.Fatalf("denied entries = %+v, want one %s override entry", denied, tc.wantAction)
			}
		})
	}
}

// declareRefused sends a state declare that the workspace refuses with err, and
// returns the response and the denied entries that it wrote.
func declareRefused(t *testing.T, err error) (*httptest.ResponseRecorder, []audit.Entry) {
	t.Helper()

	entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}

	handler, newErr := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     refusingWorkspaces{stubWorkspaces: stubWorkspaces{}, err: err},
		OutputChecker:  nil,
		BaseURL:        baseurl.Resolver{},
		Audit:          entries,
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	})
	if newErr != nil {
		t.Fatalf("handler: %v", newErr)
	}

	const body = `{"data":{"type":"state-versions","attributes":{"serial":4,` +
		`"lineage":"seed-lineage","md5":"d41d8cd98f00b204e9800998ecf8427e"}}}`

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, createStateRequest(t, "/api/v2/workspaces/ws-test123/state-versions", body))

	return rec, deniedEntries(entries)
}

// deniedEntries returns the denied entries that the store holds.
func deniedEntries(entries *memAuditStore) []audit.Entry {
	entries.mu.Lock()
	defer entries.mu.Unlock()

	var denied []audit.Entry

	for i := range entries.entries {
		if entries.entries[i].Outcome == audit.OutcomeDenied {
			denied = append(denied, entries.entries[i])
		}
	}

	return denied
}
