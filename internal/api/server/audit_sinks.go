package server

import (
	"context"
	"net/http"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// AuditSinkSource lists the configured audit sinks with their delivery
// state. It is defined here, in the package that consumes it, so the domain
// stays free of transport code.
type AuditSinkSource interface {
	// ListAuditSinks answers the delivery state of every configured sink in
	// order.
	ListAuditSinks(ctx context.Context) ([]audit.SinkStatus, error)
}

// auditSinkJSON is one configured audit sink: its name and kind, the last
// delivered sequence of each scope, the delivery lag in seconds and the last
// error class. LastError is empty when the last round succeeded.
//
// Contract: never remove or rename these fields. The console reads only
// this API, and it updates on its own schedule.
type auditSinkJSON struct {
	Name       string           `json:"name"`
	Kind       string           `json:"kind"`
	Cursor     map[string]int64 `json:"cursor"`
	LagSeconds float64          `json:"lagSeconds"`
	LastError  string           `json:"lastError"`
}

// listAuditSinksResponse lists the configured audit sinks.
type listAuditSinksResponse struct {
	Sinks []auditSinkJSON `json:"sinks"`
}

// listAuditSinks serves GET /api/server/audit/sinks: the configured audit
// sinks with their delivery state. With no sink configured it answers an
// empty list. It needs the action server:observe on the installation.
func (h *Handler) listAuditSinks(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	var statuses []audit.SinkStatus

	if h.auditSinks != nil {
		var err error

		statuses, err = h.auditSinks.ListAuditSinks(ctx)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}
	}

	sinks := make([]auditSinkJSON, 0, len(statuses))
	for _, status := range statuses {
		cursor := status.Cursors
		if cursor == nil {
			cursor = map[string]int64{}
		}

		sinks = append(sinks, auditSinkJSON{
			Name:       status.Name,
			Kind:       status.Kind,
			Cursor:     cursor,
			LagSeconds: status.LagSeconds,
			LastError:  status.LastError,
		})
	}

	h.writeJSON(ctx, w, http.StatusOK, listAuditSinksResponse{Sinks: sinks})
}
