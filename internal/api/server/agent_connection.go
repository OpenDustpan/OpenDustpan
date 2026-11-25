package server

import (
	"encoding/json"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
)

// Paths of the model connection document and of the formats.
const (
	agentConnectionPath = "/api/agent/connection"
	agentFormatsPath    = "/api/agent/formats"
)

// agentConnectionResponse is the current model connection document on the
// wire: the document with references only and its version. It never holds a
// secret value.
//
// Contract: never remove or rename these fields. The console reads only this
// API, and it updates on its own schedule.
type agentConnectionResponse struct {
	Document json.RawMessage `json:"document"`
	Version  int64           `json:"version"`
}

// agentFormatsResponse is the answer of GET /api/agent/formats: the wire
// formats that a connection document can name. It is an object, not a bare
// array, because the API description names each response schema.
type agentFormatsResponse struct {
	Formats []string `json:"formats"`
}

// agentConnectionRoutes returns the connection rows of the route table:
// the read and the disconnect of the current document and the
// list of the supported wire formats. Nil service means no agent store is
// configured: no route is mounted. Every route needs server:manage-agent on
// @server.
func (h *Handler) agentConnectionRoutes() []Route {
	if h.agent == nil {
		return nil
	}

	return []Route{
		authorizedRoute(
			http.MethodGet, agentConnectionPath, []identity.Action{identity.ActionServerManageAgent}, resolveServer,
			h.readAgentConnection,
		),
		authorizedRoute(
			http.MethodDelete, agentConnectionPath, []identity.Action{identity.ActionServerManageAgent}, resolveServer,
			h.deleteAgentConnection,
		),
		authorizedRoute(
			http.MethodGet, agentFormatsPath, []identity.Action{identity.ActionServerManageAgent}, resolveServer,
			h.listAgentFormats,
		),
	}
}

// readAgentConnection serves GET /api/agent/connection. It answers 200 with
// the current document and its version, or 404 when no document exists.
func (h *Handler) readAgentConnection(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	conn, err := h.agent.Connection(ctx, p)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, agentConnectionResponse{
		Document: json.RawMessage(conn.Spec.Marshal()),
		Version:  conn.Version,
	})
}

// deleteAgentConnection serves DELETE /api/agent/connection. It answers 204,
// or 404 when no document exists.
func (h *Handler) deleteAgentConnection(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	if err := h.agent.Disconnect(ctx, p, callerEntry(r, p, identity.ActionServerManageAgent)); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// listAgentFormats serves GET /api/agent/formats. It answers the wire formats
// that a connection document can name now.
func (h *Handler) listAgentFormats(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	formats, err := h.agent.Formats(ctx, p)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, agentFormatsResponse{Formats: formats})
}
