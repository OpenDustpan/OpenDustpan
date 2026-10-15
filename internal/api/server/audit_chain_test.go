package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
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

// newAuditChainHandler builds a Handler with chain and an authorizer that
// denies deny.
func newAuditChainHandler(t *testing.T, chain apiserver.AuditChain, deny ...identity.Action) *apiserver.Handler {
	t.Helper()

	denied := map[identity.Action]bool{}
	for _, action := range deny {
		denied[action] = true
	}

	handler, err := apiserver.NewHandler(apiserver.Config{
		Authenticator:  tokenAuthenticator{},
		Authorizer:     selectiveAuthorizer{deny: denied},
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Backups:        nil,
		Restores:       nil,
		Maintenance:    newFakeMaintenance(maintenance.StateMaintenance),
		Metrics:        stubMetrics{},
		Version:        "",
		Schema:         stubSchema{migrations: nil},
		Principals:     nil,
		Access:         nil,
		Tokens:         nil,
		Audit:          nil,
		OnAuditFailure: nil,
		DeletedUnits:   nil,
		AuditChain:     chain,
		IPAllowlists:   nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

func TestGetAuditChainAnswersTheState(t *testing.T) {
	t.Parallel()

	handler := newAuditChainHandler(t, newFakeAuditChain(audit.ChainOff))

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
		{name: "on with the enable action", deny: "", setting: audit.ChainOn, want: http.StatusAccepted},
		{
			name: "on without the enable action", deny: identity.ActionServerEnableAuditChain,
			setting: audit.ChainOn, want: http.StatusForbidden,
		},
		{
			name: "on asks for no action of off", deny: identity.ActionServerApproveAuditChainOff,
			setting: audit.ChainOn, want: http.StatusAccepted,
		},
		{name: "off with both actions", deny: "", setting: audit.ChainOff, want: http.StatusAccepted},
		{
			name: "off without the disable action", deny: identity.ActionServerDisableAuditChain,
			setting: audit.ChainOff, want: http.StatusForbidden,
		},
		{
			name: "off without the administration action", deny: identity.ActionServerApproveAuditChainOff,
			setting: audit.ChainOff, want: http.StatusForbidden,
		},
		{
			name: "off asks for no action of on", deny: identity.ActionServerEnableAuditChain,
			setting: audit.ChainOff, want: http.StatusAccepted,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			chain := newFakeAuditChain(tc.setting)
			handler := newAuditChainHandler(t, chain, tc.deny)

			status, body := serveMaintenance(t, handler, http.MethodPost, "/api/server/audit/chain", "")
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
		{name: "active work", err: maintenance.ErrActiveWork, want: http.StatusConflict},
		{name: "a server fault", err: errAuditChainFault, want: http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			chain := newFakeAuditChain(audit.ChainOff)
			chain.approveErr = fmt.Errorf("approve: %w", tc.err)
			handler := newAuditChainHandler(t, chain)

			status, body := serveMaintenance(t, handler, http.MethodPost, "/api/server/audit/chain", "")
			if status != tc.want {
				t.Fatalf("status = %d, want %d (%s)", status, tc.want, body)
			}
		})
	}
}

func TestAuditChainRoutesWithoutAChain(t *testing.T) {
	t.Parallel()

	handler := newAuditChainHandler(t, nil)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		status, body := serveMaintenance(t, handler, method, "/api/server/audit/chain", "")
		if status != http.StatusNotImplemented {
			t.Fatalf("%s: status = %d, want 501 (%s)", method, status, body)
		}
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
