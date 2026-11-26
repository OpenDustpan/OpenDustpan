package server

import (
	"encoding/json"
	"errors"
	"net/http"

	denials "opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Paths of the model connection document and of the formats.
const (
	agentConnectionPath = "/api/agent/connection"
	agentFormatsPath    = "/api/agent/formats"
)

// agentConnectionResponse is the current model connection document on the
// wire: the document with references only, its version, and the pending
// documents with their probes. It never holds a secret value.
//
// Contract: never remove or rename these fields. The console reads only this
// API, and it updates on its own schedule.
type agentConnectionResponse struct {
	Document json.RawMessage        `json:"document"`
	Version  int64                  `json:"version"`
	Pending  []agentPendingResponse `json:"pending"`
}

// agentPendingResponse is one pending document of the answer: the document
// that a probe wrote, with the reference of the probe. It never holds a
// secret value.
type agentPendingResponse struct {
	Probe    string          `json:"probe"`
	Document json.RawMessage `json:"document"`
	Version  int64           `json:"version"`
}

// agentRefusalOf returns the status and the client text of a refusal of an
// agent route: a coded error keeps its own status and text, any other error
// goes through classify.
func agentRefusalOf(err error) (int, string) {
	if coded, ok := errors.AsType[*codedError](err); ok {
		return coded.status, coded.message
	}

	f := classify(err)

	return f.status, f.message
}

// writeAgentRefusal writes the error answer of err and records the denied
// audit entry of a refusal with a 4xx status. The server audits each call
// that reaches it, also a call that it refuses: a bad body, an invalid
// input, an unknown reference, an in-use item or a missing document. The
// entry holds the refusal text, never a request value. A 500 is a failure
// of the server and not a refusal: it logs instead and writes no entry.
func (h *Handler) writeAgentRefusal(
	w http.ResponseWriter, r *http.Request, p identity.Principal, action identity.Action, err error,
) {
	ctx := r.Context()
	status, message := agentRefusalOf(err)

	if status >= http.StatusBadRequest && status < http.StatusInternalServerError {
		h.guard.Record(ctx, r, p, denials.Denial{
			Action: action, Object: identity.ServerObject(), Reason: denials.Clean(message, maxCauseBytes),
			ReasonWithin: nil, Window: 0,
		})
	}

	h.writeError(ctx, w, err)
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
// the current document, its version and the pending documents, or 404 when
// no document exists.
func (h *Handler) readAgentConnection(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	conn, err := h.agent.Connection(ctx, p)
	if err != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageAgent, err)

		return
	}

	pending, err := h.agent.PendingAll(ctx, p)
	if err != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageAgent, err)

		return
	}

	body := agentConnectionResponse{
		Document: json.RawMessage(conn.Spec.Marshal()),
		Version:  conn.Version,
		Pending:  make([]agentPendingResponse, 0, len(pending)),
	}

	for i := range pending {
		body.Pending = append(body.Pending, agentPendingResponse{
			Probe:    string(pending[i].Probe),
			Document: json.RawMessage(pending[i].Spec.Marshal()),
			Version:  pending[i].Version,
		})
	}

	h.writeJSON(ctx, w, http.StatusOK, body)
}

// deleteAgentConnection serves DELETE /api/agent/connection. It answers 204,
// or 404 when no document exists.
func (h *Handler) deleteAgentConnection(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	if err := h.agent.Disconnect(ctx, p, callerEntry(r, p, identity.ActionServerManageAgent)); err != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageAgent, err)

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
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageAgent, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, agentFormatsResponse{Formats: formats})
}
