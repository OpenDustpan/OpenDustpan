package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// CheckpointEpochs starts a new epoch of the signed audit checkpoints.
// audit.Checkpoints implements it.
type CheckpointEpochs interface {
	// NewEpoch starts the epoch after the current one at now and answers it.
	NewEpoch(ctx context.Context, now time.Time) (int64, error)
	// Now answers the time of the checkpoints.
	Now() time.Time
}

// errCheckpointsOff reports a checkpoint route while signed checkpoints are
// off.
var errCheckpointsOff = errors.New("audit checkpoints are off: set --audit-checkpoints=true and restart the server")

// checkpointEpochResponse is the answer of a new checkpoint epoch.
type checkpointEpochResponse struct {
	Epoch int64 `json:"epoch"`
}

// checkpointRoutes returns the route of the signed audit checkpoints.
func (h *Handler) checkpointRoutes() []Route {
	return []Route{
		{
			Method:     http.MethodPost,
			Path:       "/api/server/audit/checkpoint-epochs",
			Scope:      ScopeAuthorized,
			Actions:    []identity.Action{identity.ActionServerManageAudit},
			Resolve:    resolveServer,
			ActionsFor: nil, OpaqueDenial: false,
			Serve: h.newCheckpointEpoch,
		},
	}
}

// newCheckpointEpoch starts a new epoch of the signed audit checkpoints, so
// the check reads only the checkpoints that the server signs from now on.
// An administrator calls it after checking why a checkpoint does not
// match. It writes one entry of the kind change in the scope @server, and
// answers 409 while signed checkpoints are off.
func (h *Handler) newCheckpointEpoch(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	if h.epochs == nil {
		h.writeError(ctx, w, errCheckpointsOff)

		return
	}

	now := h.epochs.Now()

	epoch, err := h.epochs.NewEpoch(ctx, now)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.appendEpochChange(r, p, epoch, now)
	h.writeJSON(ctx, w, http.StatusCreated, checkpointEpochResponse{Epoch: epoch})
}

// appendEpochChange writes the entry of the kind change for a new
// checkpoint epoch. A lost entry does not change the answer: the epoch
// already exists, so the server logs the loss and counts it on the failure
// metric.
func (h *Handler) appendEpochChange(r *http.Request, p identity.Principal, epoch int64, at time.Time) {
	entry := callerEntry(r, p, identity.ActionServerManageAudit)
	entry.At = at
	entry.Namespace = audit.ServerScope
	entry.Kind = audit.KindChange
	entry.Outcome = audit.OutcomeSuccess
	entry.Object = audit.ServerScope.String()
	entry.ValuePath = "/audit/checkpoints/epoch/" + strconv.FormatInt(epoch, 10)

	if _, err := h.audit.AppendServer(r.Context(), entry); err != nil {
		h.failAudit(r.Context())
	}
}
