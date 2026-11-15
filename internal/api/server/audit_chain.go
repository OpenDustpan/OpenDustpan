package server

// Audit chain routes of the installation API: read the state of the audit
// chain, and approve a change to the setting of the instance.
//
// GET /api/server/audit/chain answers the stored state, the setting of the
// instance that answers, whether a change waits and whether its migration
// runs. It needs server:read-audit.
// POST /api/server/audit/chain approves the change to the setting of the
// instance that receives it, and starts the migration: it answers 202. It
// needs server:set-audit-chain. On an instance with the setting off it also
// needs server:disable-audit-chain, and a fresh MFA step.

import (
	"context"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// noAuditChainMessage answers the audit chain routes when the store keeps
// no audit chain.
const noAuditChainMessage = "the store does not keep an audit chain"

// AuditChain reads the state of the audit chain and approves a waiting
// change. The server wires it; tests stub it.
type AuditChain interface {
	// Setting answers the setting of this instance. It never changes while
	// the instance runs.
	Setting() audit.ChainSetting
	// Status answers the stored state of the chain and whether a change
	// waits or migrates.
	Status(ctx context.Context) (AuditChainStatus, error)
	// Approve approves the change to the setting of this instance and
	// starts its migration. caller names who acted and from where. It
	// reports audit.ErrNoChainChange when no change waits,
	// audit.ErrChainMigrating while a migration runs,
	// backup.ErrRestoreRunning while a restore runs,
	// maintenance.ErrActiveWork while an update or a run is active, and the
	// error of the MFA step for a change to off.
	Approve(ctx context.Context, caller audit.Entry) error
}

// AuditChainStatus is the state of the audit chain that the GET route
// answers.
type AuditChainStatus struct {
	ChangedAt time.Time
	ChangedBy string
	State     audit.ChainState
	Setting   audit.ChainSetting
	Pending   bool
	Migrating bool
}

// auditChainResponse is the state of the audit chain on the wire.
type auditChainResponse struct {
	State     string `json:"state"`
	Setting   string `json:"setting"`
	ChangedAt string `json:"changedAt,omitempty"`
	ChangedBy string `json:"changedBy,omitempty"`
	Pending   bool   `json:"pending"`
	Migrating bool   `json:"migrating"`
}

// auditChainRoutes returns the audit chain rows of the route table.
func (h *Handler) auditChainRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodGet,
			Path:    "/api/server/audit/chain",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerReadAudit},
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.getAuditChain,
		},
		{
			Method: http.MethodPost,
			Path:   "/api/server/audit/chain",
			Scope:  ScopeAuthorized,
			Actions: []identity.Action{
				identity.ActionServerSetAuditChain,
				identity.ActionServerDisableAuditChain,
			},
			Resolve:      resolveServer,
			ActionsFor:   h.auditChainApproveActions,
			OpaqueDenial: false,
			Serve:        h.fresh(identity.ActionServerSetAuditChain, h.approveAuditChain),
		},
	}
}

// auditChainApproveActions answers the actions of an approval on this
// instance: the setting of the instance decides the direction of the
// change, and it never changes while the instance runs.
func (h *Handler) auditChainApproveActions(identity.Object) []identity.Action {
	if h.auditChain != nil && h.auditChain.Setting() == audit.ChainOff {
		return []identity.Action{
			identity.ActionServerSetAuditChain,
			identity.ActionServerDisableAuditChain,
		}
	}

	return []identity.Action{identity.ActionServerSetAuditChain}
}

// getAuditChain answers the state of the audit chain.
func (h *Handler) getAuditChain(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	if h.auditChain == nil {
		h.writeJSON(ctx, w, http.StatusNotImplemented, errorResponse{
			Code: http.StatusNotImplemented, Message: noAuditChainMessage,
		})

		return
	}

	status, err := h.auditChain.Status(ctx)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, toAuditChainResponse(status))
}

// approveAuditChain approves the waiting change of the audit chain and
// answers 202 with the state: the migration runs in the background.
func (h *Handler) approveAuditChain(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	if h.auditChain == nil {
		h.writeJSON(ctx, w, http.StatusNotImplemented, errorResponse{
			Code: http.StatusNotImplemented, Message: noAuditChainMessage,
		})

		return
	}

	if !h.admitStart(ctx, w) {
		return
	}

	if err := h.auditChain.Approve(ctx, callerEntry(r, p, audit.ActionSetChain)); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	status, err := h.auditChain.Status(ctx)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusAccepted, toAuditChainResponse(status))
}

// toAuditChainResponse maps a status to the wire.
func toAuditChainResponse(s AuditChainStatus) auditChainResponse {
	resp := auditChainResponse{
		State:     string(s.State),
		Setting:   s.Setting.String(),
		ChangedAt: "",
		ChangedBy: s.ChangedBy,
		Pending:   s.Pending,
		Migrating: s.Migrating,
	}

	if !s.ChangedAt.IsZero() && s.ChangedAt.UnixMilli() != 0 {
		resp.ChangedAt = formatBackupTime(s.ChangedAt)
	}

	return resp
}
