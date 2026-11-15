package server

// Retention sweep route of the installation API. The server runs
// the sweep on its own schedule. This route runs it at once. It purges only
// the deleted units whose retention period has ended, which the schedule
// would purge anyway, so it needs server:maintenance on the object @server
// and no new action.

import (
	"context"
	"errors"
	"net/http"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
)

// DeletedUnitSweeper runs the retention sweep of the deleted units.
// inventory.Sweeper implements it. It is defined here, in the package that
// consumes it.
type DeletedUnitSweeper interface {
	// Sweep runs one sweep with the audit entry e for each purge, and
	// returns its outcome. It returns an error only when it cannot read the
	// deleted units or the blob purge records.
	Sweep(ctx context.Context, e audit.Entry) (inventory.SweepResult, error)
}

// deletedSweepPath is the route that runs one retention sweep now.
const deletedSweepPath = "/api/server/deleted-units/sweep"

// sweptUnitResponse is one unit that a sweep purged.
//
// Contract: never remove or rename these fields.
type sweptUnitResponse struct {
	Tool      string `json:"tool"`
	Namespace string `json:"namespace"`
	UnitID    string `json:"unitID"`
	Reference string `json:"reference"`
}

// deletedSweepResponse is the outcome of one retention sweep.
//
// Contract: never remove or rename these fields.
type deletedSweepResponse struct {
	Purged             []sweptUnitResponse `json:"purged"`
	Failed             int                 `json:"failed"`
	Skipped            int                 `json:"skipped"`
	FinishedBlobPurges int                 `json:"finishedBlobPurges"`
	PendingBlobPurges  int                 `json:"pendingBlobPurges"`
	WaitingBlobPurges  int                 `json:"waitingBlobPurges"`
	WaitingFor         string              `json:"waitingFor"`
}

// sweepRunningMessage is the answer of the sweep route while another sweep
// of this server runs. The route does not wait for it.
const sweepRunningMessage = "a retention sweep is running. Try again later."

// noDeletedUnitsMessage is the answer of the sweep route on a server whose
// store keeps no deleted units.
const noDeletedUnitsMessage = "the store of this server keeps no deleted units"

// deletedUnitRoutes returns the retention sweep row of the route table.
func (h *Handler) deletedUnitRoutes() []Route {
	return []Route{
		{
			Method:     http.MethodPost,
			Path:       deletedSweepPath,
			Scope:      ScopeAuthorized,
			Actions:    []identity.Action{identity.ActionServerMaintenance},
			Resolve:    resolveServer,
			ActionsFor: nil, OpaqueDenial: false,
			Serve: h.sweepDeletedUnits,
		},
	}
}

// sweepDeletedUnits serves POST /api/server/deleted-units/sweep: it runs the
// retention sweep once and answers 200 with its outcome, or 409 at once when
// another sweep of this server runs. Each purge writes
// its audit entry in its own transaction, with the caller as the subject
// and the action unit:purge. A failed purge does not fail the request: the
// answer counts it, and the next sweep tries again.
func (h *Handler) sweepDeletedUnits(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if h.deletedUnits == nil {
		h.writeJSON(ctx, w, http.StatusNotImplemented, errorResponse{
			Code: http.StatusNotImplemented, Message: noDeletedUnitsMessage,
		})

		return
	}

	e := callerEntry(r, p, identity.ActionUnitPurge)
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess

	res, err := h.deletedUnits.Sweep(ctx, e)
	if errors.Is(err, inventory.ErrSweepRunning) {
		h.writeJSON(ctx, w, http.StatusConflict, errorResponse{
			Code: http.StatusConflict, Message: sweepRunningMessage,
		})

		return
	}

	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	body := deletedSweepResponse{
		Purged:             make([]sweptUnitResponse, 0, len(res.Purged)),
		Failed:             res.Failed,
		Skipped:            res.Skipped,
		FinishedBlobPurges: res.FinishedBlobPurges,
		PendingBlobPurges:  res.PendingBlobPurges,
		WaitingBlobPurges:  res.WaitingBlobPurges,
		WaitingFor:         res.WaitingFor,
	}

	for _, d := range res.Purged {
		body.Purged = append(body.Purged, sweptUnitResponse{
			Tool: d.Tool, Namespace: d.Namespace.String(), UnitID: d.UnitID, Reference: d.Reference(),
		})
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusOK, body)
}
