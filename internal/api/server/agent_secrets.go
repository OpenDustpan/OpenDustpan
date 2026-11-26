package server

import (
	"errors"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/internal/agentsvc"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Paths of the secret items of the model connection.
const (
	agentItemsPath = "/api/agent/secrets"
	agentItemPath  = "/api/agent/secrets/{ref}"
)

// maxAgentSecretBodyBytes bounds the body of a secret item change. A secret
// value is at most 16 KiB, so this is ample for its JSON form.
const maxAgentSecretBodyBytes = 32 * 1024

// The errors of the secret item routes that no domain sentinel carries.
var (
	errAgentSecretCreateBody = &codedError{
		status: http.StatusBadRequest, code: errcode.AgentConnectionSecretInvalid, args: nil,
		message: `invalid secret item body: provide {"name":"...","value":"..."}`,
	}
	errAgentSecretReplaceBody = &codedError{
		status: http.StatusBadRequest, code: errcode.AgentConnectionSecretInvalid, args: nil,
		message: `invalid secret item body: provide {"value":"..."}`,
	}
)

// createAgentSecretRequest is the body of a secret item create.
type createAgentSecretRequest struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// replaceAgentSecretRequest is the body of a secret item replace.
type replaceAgentSecretRequest struct {
	Value string `json:"value"`
}

// agentSecretResponse is the metadata of one secret item on the wire. It
// never holds the value.
//
// Contract: never remove or rename these fields. The console reads only this
// API, and it updates on its own schedule.
type agentSecretResponse struct {
	Ref       agent.SecretRef `json:"ref"`
	Name      string          `json:"name"`
	CreatedAt string          `json:"createdAt"`
}

// agentSecretListResponse is the list of the secret items of the
// installation.
type agentSecretListResponse struct {
	Secrets []agentSecretResponse `json:"secrets"`
}

// agentSecretOf returns the wire form of item. The time is UTC in RFC 3339.
func agentSecretOf(item agent.SecretItem) agentSecretResponse {
	return agentSecretResponse{
		Ref:       item.Ref,
		Name:      item.Name,
		CreatedAt: time.UnixMilli(item.CreatedAt).UTC().Format(time.RFC3339),
	}
}

// agentSecretInvalid returns the coded error of an invalid name, value or
// reference.
func agentSecretInvalid(err error) error {
	return &codedError{
		status: http.StatusBadRequest, code: errcode.AgentConnectionSecretInvalid, args: nil,
		message: err.Error(),
	}
}

// agentSecretRef reads and checks the reference of the path. It returns the
// coded error of a reference that is not valid.
func agentSecretRef(r *http.Request) (agent.SecretRef, error) {
	ref := agent.SecretRef(r.PathValue("ref"))
	if err := agentsvc.CheckSecretRef(ref); err != nil {
		return agent.SecretRef(""), agentSecretInvalid(err)
	}

	return ref, nil
}

// agentSecretRoutes returns the secret items rows of the route table
// (each route declares its action and object): create, list, replace and
// delete. Nil service means no agent
// store is configured: no route is mounted. Every route needs
// server:manage-agent on @server. No route answers a value.
func (h *Handler) agentSecretRoutes() []Route {
	if h.agent == nil {
		return nil
	}

	return []Route{
		authorizedRoute(
			http.MethodPost, agentItemsPath, []identity.Action{identity.ActionServerManageAgent}, resolveServer,
			h.createAgentSecret,
		),
		authorizedRoute(
			http.MethodGet, agentItemsPath, []identity.Action{identity.ActionServerManageAgent}, resolveServer,
			h.listAgentSecrets,
		),
		authorizedRoute(
			http.MethodPut, agentItemPath, []identity.Action{identity.ActionServerManageAgent}, resolveServer,
			h.replaceAgentSecret,
		),
		authorizedRoute(
			http.MethodDelete, agentItemPath, []identity.Action{identity.ActionServerManageAgent}, resolveServer,
			h.deleteAgentSecret,
		),
	}
}

// createAgentSecret serves POST /api/agent/secrets. It answers 201 with the
// metadata of the stored item. It answers 400 for a body without a valid
// name or value.
func (h *Handler) createAgentSecret(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	body, readErr := readBodyUpTo(w, r, maxAgentSecretBodyBytes, errAgentSecretCreateBody)
	if readErr != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageAgent, readErr)

		return
	}

	var in createAgentSecretRequest
	if err := decodeBody(body, &in, errAgentSecretCreateBody); err != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageAgent, err)

		return
	}

	value := []byte(in.Value)
	// Defense in depth: the service and the store check the name and the
	// value too; this check stays so that a new caller cannot skip it.
	if err := agent.CheckSecretItem(in.Name, value); err != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageAgent, agentSecretInvalid(err))

		return
	}

	item, err := h.agent.CreateSecret(
		ctx, p, callerEntry(r, p, identity.ActionServerManageAgent), in.Name, value,
	)
	if err != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageAgent, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusCreated, agentSecretOf(item))
}

// listAgentSecrets serves GET /api/agent/secrets. It answers 200 with the
// metadata of every item, newest first, and never a value.
func (h *Handler) listAgentSecrets(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	items, err := h.agent.ListSecrets(ctx, p)
	if err != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageAgent, err)

		return
	}

	body := agentSecretListResponse{Secrets: make([]agentSecretResponse, 0, len(items))}
	for _, item := range items {
		body.Secrets = append(body.Secrets, agentSecretOf(item))
	}

	h.writeJSON(ctx, w, http.StatusOK, body)
}

// replaceAgentSecret serves PUT /api/agent/secrets/{ref}. It answers 200
// with the metadata of the item. It answers 404 for an unknown reference.
func (h *Handler) replaceAgentSecret(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	ref, refErr := agentSecretRef(r)
	if refErr != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageAgent, refErr)

		return
	}

	body, readErr := readBodyUpTo(w, r, maxAgentSecretBodyBytes, errAgentSecretReplaceBody)
	if readErr != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageAgent, readErr)

		return
	}

	var in replaceAgentSecretRequest
	if err := decodeBody(body, &in, errAgentSecretReplaceBody); err != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageAgent, err)

		return
	}

	value := []byte(in.Value)
	// Defense in depth: the service and the store check the value too;
	// this check stays so that a new caller cannot skip it. The item keeps
	// its name, so only the value rules apply here.
	if err := agent.CheckSecretValue(value); err != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageAgent, agentSecretInvalid(err))

		return
	}

	item, err := h.agent.ReplaceSecret(
		ctx, p, callerEntry(r, p, identity.ActionServerManageAgent), ref, value,
	)
	if err != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageAgent, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, agentSecretOf(item))
}

// deleteAgentSecret serves DELETE /api/agent/secrets/{ref}. It answers 204.
// It answers 409 while a stored document refers to the item.
func (h *Handler) deleteAgentSecret(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	ref, refErr := agentSecretRef(r)
	if refErr != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageAgent, refErr)

		return
	}

	if err := h.agent.DeleteSecret(ctx, p, callerEntry(r, p, identity.ActionServerManageAgent), ref); err != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageAgent, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// classifyAgent maps the model connection errors to their status and coded
// message. The message of a refusal names the command that helps and never a
// value.
func classifyAgent(err error) (failure, bool) {
	switch {
	case errors.Is(err, agentsvc.ErrInvalidRef):
		return failCoded(http.StatusBadRequest,
			"The secret reference is not valid: list the secret items with GET /api/agent/secrets.",
			errcode.AgentConnectionSecretInvalid), true
	case errors.Is(err, agent.ErrSecretNotFound):
		return failCoded(http.StatusNotFound,
			"The secret item does not exist: list the secret items with GET /api/agent/secrets.",
			errcode.AgentConnectionSecretNotFound), true
	case errors.Is(err, agent.ErrSecretInUse):
		return failCoded(http.StatusConflict,
			"The secret item is in use: disconnect with DELETE /api/agent/connection, then delete it.",
			errcode.AgentConnectionSecretInUse), true
	case errors.Is(err, agent.ErrDisabled):
		return failCoded(http.StatusNotFound,
			"No model is connected: the route that writes a connection document is not available in this version.",
			errcode.AgentConnectionNotConnected), true
	}

	return fail(0, ""), false
}
