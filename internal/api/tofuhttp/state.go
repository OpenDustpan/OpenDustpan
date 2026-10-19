package tofuhttp

import (
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
)

// stateRoutes returns the state read row of the address. A read answers the
// whole state with plain secret values, so it needs unit:read and
// unit:read-secrets on the unit of the path.
func (h *Handler) stateRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodGet,
			Path:    statePath,
			Actions: []identity.Action{identity.ActionUnitRead, identity.ActionUnitReadSecrets},
			Serve:   h.getState,
		},
	}
}

// getState serves GET: the current state of the workspace, with its Content-MD5
// header. It writes one audit entry before it sends a byte, of the kind reveal
// for a person and tool-operation for any other caller. A lost entry answers
// 500 with no content. A workspace without a state, or one that does not exist, answers 404,
// which the backend reads as no state (ADR 49, decision 5).
func (h *Handler) getState(w http.ResponseWriter, r *http.Request, c call) {
	ctx := r.Context()

	ws, err := h.ws.Get(ctx, c.ref)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	sv, err := h.ws.Current(ctx, ws.ID)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	content, err := h.ws.Open(ctx, sv)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	defer func() {
		if closeErr := content.Close(); closeErr != nil {
			h.logger.WarnContext(ctx, "close state content", slog.String("error", closeErr.Error()))
		}
	}()

	obj, err := unitObject(c.ref)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if err := h.appendDownloadAudit(c.request, c.principal, obj, sv.ID.String()); err != nil {
		h.logger.ErrorContext(ctx, "audit append failed", slog.String("error", err.Error()))

		if h.onAuditFailure != nil {
			h.onAuditFailure()
		}

		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-MD5", base64.StdEncoding.EncodeToString(sv.MD5[:]))
	w.WriteHeader(http.StatusOK)

	if _, copyErr := io.Copy(w, content); copyErr != nil {
		h.logger.WarnContext(ctx, "write state content", slog.String("error", copyErr.Error()))
	}
}
