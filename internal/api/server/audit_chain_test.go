package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// fakeAuditChain answers setting and status, fails each approval with
// approveErr, and records the caller of each approval.
type fakeAuditChain struct {
	approveErr error
	callers    []audit.Entry
	status     apiserver.AuditChainStatus
	setting    audit.ChainSetting
}

func (f *fakeAuditChain) Setting() audit.ChainSetting { return f.setting }

func (f *fakeAuditChain) Status(context.Context) (apiserver.AuditChainStatus, error) {
	return f.status, nil
}

func (f *fakeAuditChain) Approve(_ context.Context, caller audit.Entry) error {
	f.callers = append(f.callers, caller)

	return f.approveErr
}

// newFakeAuditChain answers an instance with setting while the stored state
// is the other state, so a change waits.
func newFakeAuditChain(setting audit.ChainSetting) *fakeAuditChain {
	state := audit.ChainStateOn
	if setting == audit.ChainOn {
		state = audit.ChainStateOff
	}

	return &fakeAuditChain{
		approveErr: nil,
		callers:    nil,
		status: apiserver.AuditChainStatus{
			ChangedAt: time.UnixMilli(1_700_000_000_000).UTC(), ChangedBy: "alice",
			State: state, Setting: setting, Pending: true, Migrating: false,
		},
		setting: setting,
	}
}

// newAuditChainHandler builds a Handler with chain, namespaces and an
// authorizer that denies deny.
func newAuditChainHandler(
	t *testing.T, namespaces *namespace.Service, chain apiserver.AuditChain, deny ...identity.Action,
) testHandler {
	t.Helper()

	denied := map[identity.Action]bool{}
	for _, action := range deny {
		denied[action] = true
	}

	sessions := newStoreSessions(t, openRouteTestStore(t))

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Cursors:          testCursors(t),
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		TempAdmins:       nil,
		Now:              nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer:       selectiveAuthorizer{deny: denied},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       namespaces,
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateMaintenance),
		Metrics:          stubMetrics{},
		Version:          "",
		Schema:           stubSchema{migrations: nil},
		Principals:       sessions.principals,
		Access:           nil,
		Tokens:           nil,
		Audit:            newTestAudit(),
		OnAuditFailure:   nil,
		DeletedUnits:     nil,
		SessionPolicy:    nil,
		AuditChain:       chain,
		AuditSinks:       nil,
		SignIn:           sessions.signIn(),
		Journal:          nil,
		IPAllowlists:     nil,
		Gates:            nil,
		CheckpointEpochs: nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	rememberSessions(handler, sessions)

	return handler
}

// approveAuditChain sends POST /api/server/audit/chain from a console session
// that signed in just now: the change needs a fresh authentication.
func approveAuditChain(t *testing.T, handler testHandler) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/server/audit/chain", http.NoBody)
	startSession(t, handler, req, 0)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

func TestGetAuditChainAnswersTheState(t *testing.T) {
	t.Parallel()

	namespaces := newTestNamespaces(t)
	handler := newAuditChainHandler(t, namespaces, newFakeAuditChain(audit.ChainOff))

	status, body := serveMaintenance(t, handler, http.MethodGet, "/api/server/audit/chain", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", status, body)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	if got["state"] != "on" || got["setting"] != "off" || got["pending"] != any(true) ||
		got["migrating"] != any(false) || got["changedBy"] != "alice" || got["changedAt"] == nil {
		t.Fatalf("body = %s, want state on, setting off, a waiting change by alice", body)
	}
}

func TestApproveAuditChainNeedsTheActionsOfItsDirection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		deny    identity.Action
		setting audit.ChainSetting
		want    int
	}{
		{name: "on with the set action", deny: "", setting: audit.ChainOn, want: http.StatusAccepted},
		{
			name: "on without the set action", deny: identity.ActionServerSetAuditChain,
			setting: audit.ChainOn, want: http.StatusForbidden,
		},
		{
			name: "on asks for no disable action", deny: identity.ActionServerDisableAuditChain,
			setting: audit.ChainOn, want: http.StatusAccepted,
		},
		{name: "off with both actions", deny: "", setting: audit.ChainOff, want: http.StatusAccepted},
		{
			name: "off without the set action", deny: identity.ActionServerSetAuditChain,
			setting: audit.ChainOff, want: http.StatusForbidden,
		},
		{
			name: "off without the disable action", deny: identity.ActionServerDisableAuditChain,
			setting: audit.ChainOff, want: http.StatusForbidden,
		},
	}

	namespaces := newTestNamespaces(t)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			chain := newFakeAuditChain(tc.setting)
			handler := newAuditChainHandler(t, namespaces, chain, tc.deny)

			status, body := approveAuditChain(t, handler)
			if status != tc.want {
				t.Fatalf("status = %d, want %d (%s)", status, tc.want, body)
			}

			if tc.want != http.StatusAccepted {
				if len(chain.callers) != 0 {
					t.Fatalf("approvals = %d, want none after a refusal", len(chain.callers))
				}

				return
			}

			if len(chain.callers) != 1 || chain.callers[0].Subject != "tester" ||
				chain.callers[0].Action != audit.ActionSetChain {
				t.Fatalf("callers = %+v, want one approval by tester", chain.callers)
			}
		})
	}
}

func TestApproveAuditChainMapsItsErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err  error
		name string
		want int
	}{
		{name: "no change waits", err: audit.ErrNoChainChange, want: http.StatusConflict},
		{name: "a migration runs", err: audit.ErrChainMigrating, want: http.StatusConflict},
		{name: "no fresh MFA step", err: audit.ErrChainStepRequired, want: http.StatusForbidden},
		{name: "a service principal approves turning off", err: audit.ErrChainNeedsPerson, want: http.StatusForbidden},
		{name: "active work", err: maintenance.ErrActiveWork, want: http.StatusConflict},
		{name: "no migration loop", err: audit.ErrChainMigrationsStopped, want: http.StatusServiceUnavailable},
		{name: "a server fault", err: errAuditChainFault, want: http.StatusInternalServerError},
	}

	namespaces := newTestNamespaces(t)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			chain := newFakeAuditChain(audit.ChainOff)
			chain.approveErr = fmt.Errorf("approve: %w", tc.err)
			handler := newAuditChainHandler(t, namespaces, chain)

			status, body := approveAuditChain(t, handler)
			if status != tc.want {
				t.Fatalf("status = %d, want %d (%s)", status, tc.want, body)
			}
		})
	}
}

func TestAuditChainRoutesWithoutAChain(t *testing.T) {
	t.Parallel()

	namespaces := newTestNamespaces(t)
	handler := newAuditChainHandler(t, namespaces, nil)

	status, body := serveMaintenance(t, handler, http.MethodGet, "/api/server/audit/chain", "")
	if status != http.StatusNotImplemented {
		t.Fatalf("GET: status = %d, want 501 (%s)", status, body)
	}

	status, body = approveAuditChain(t, handler)
	if status != http.StatusNotImplemented {
		t.Fatalf("POST: status = %d, want 501 (%s)", status, body)
	}
}

func TestLeaveMaintenanceWhileHeld(t *testing.T) {
	t.Parallel()

	fake := newFakeMaintenance(maintenance.StateMaintenance)
	fake.leaveErr = maintenance.ErrHeld
	handler := newMaintenanceHandler(t, fake)

	status, body := serveMaintenance(t, handler, http.MethodPost, "/api/server/maintenance/leave", "")
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", status, body)
	}
}

// errAuditChainFault is a failure that no rule of the API maps.
var errAuditChainFault = errors.New("store unavailable")
