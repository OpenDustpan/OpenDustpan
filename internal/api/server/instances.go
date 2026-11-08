package server

// Instance routes of the installation API: list the version rows of the server
// processes, and delete the row of a process that died.
//
// GET /api/server/instances answers every row, oldest start first. It needs the
// action server:read-instances on the installation (@server).
// DELETE /api/server/instances/{id} deletes one row and writes one entry of the
// kind change in the scope @server. It needs the action server:delete-instance
// on the installation. It does not stop a process, and a process that runs
// writes its row again only at its next start.
//
// The rows are not a liveness lease: nothing renews them, and nothing reads
// them to decide that an instance is live. A contract migration that raises
// the minimum binary version waits for every row below the new minimum.

import (
	"errors"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// instancesPath and instancePath are the paths of the instance routes.
const (
	instancesPath = "/api/server/instances"
	instancePath  = "/api/server/instances/{id}"
)

// instanceJSON is one version row: the ID of the process, the version of its
// binary and the time that it started, in UTC.
//
// Contract: never remove or rename these fields. The console reads only
// this API, and it updates on its own schedule.
type instanceJSON struct {
	ID            string `json:"id"`
	BinaryVersion string `json:"binaryVersion"`
	StartedAt     string `json:"startedAt"`
}

// listInstancesResponse lists the version rows.
type listInstancesResponse struct {
	Instances []instanceJSON `json:"instances"`
}

// instanceRoutes returns the instance rows of the route table. Without a store
// it returns none.
func (h *Handler) instanceRoutes() []Route {
	if h.instances == nil {
		return nil
	}

	return []Route{
		{
			Method:     http.MethodGet,
			Path:       instancesPath,
			Scope:      ScopeAuthorized,
			Actions:    []identity.Action{identity.ActionServerReadInstances},
			Resolve:    resolveServer,
			ActionsFor: nil, OpaqueDenial: false,
			Serve: h.listInstances,
		},
		{
			Method:     http.MethodDelete,
			Path:       instancePath,
			Scope:      ScopeAuthorized,
			Actions:    []identity.Action{identity.ActionServerDeleteInstance},
			Resolve:    resolveServer,
			ActionsFor: nil, OpaqueDenial: false,
			Serve: h.deleteInstance,
		},
	}
}

// listInstances serves GET /api/server/instances.
func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	rows, err := h.instances.ListInstances(ctx)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	body := listInstancesResponse{Instances: make([]instanceJSON, 0, len(rows))}
	for _, row := range rows {
		body.Instances = append(body.Instances, instanceJSON{
			ID:            row.ID,
			BinaryVersion: row.BinaryVersion,
			StartedAt:     row.StartedAt.UTC().Format(time.RFC3339Nano),
		})
	}

	h.writeJSON(ctx, w, http.StatusOK, body)
}

// deleteInstance serves DELETE /api/server/instances/{id}. It checks the ID
// before the store call. The store checks it again.
func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()
	id := r.PathValue("id")

	// Defense in depth: the store validates the ID too; this check stays so
	// that a hostile ID never reaches the store or the audit entry.
	if err := installation.ValidInstanceID(id); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if err := h.instances.DeleteInstance(ctx, id); err != nil {
		h.appendInstanceEntry(r, p, id, audit.OutcomeFailure)
		h.writeError(ctx, w, err)

		return
	}

	h.appendInstanceEntry(r, p, id, audit.OutcomeSuccess)
	w.WriteHeader(http.StatusNoContent)
}

// appendInstanceEntry writes the entry of the delete of the row id: who
// deleted it, from where, and the outcome. A lost entry does not change the
// answer: the server logs the loss and counts it on the failure metric.
func (h *Handler) appendInstanceEntry(r *http.Request, p identity.Principal, id string, outcome audit.Outcome) {
	entry := callerEntry(r, p, identity.ActionServerDeleteInstance)
	entry.At = h.now().UTC()
	entry.Namespace = audit.ServerScope
	entry.Kind = audit.KindChange
	entry.Outcome = outcome
	entry.Object = audit.ServerScope.String()
	entry.ValuePath = "/instances/" + id

	if _, err := h.audit.AppendServer(r.Context(), entry); err != nil {
		h.failAudit(r.Context())
	}
}

// classifyInstance maps the errors of the instance version rows: an ID that is
// not allowed answers 400, and an ID without a row answers 404. Both texts are
// fixed, so the ID of the request never reaches an answer. It also maps the
// refusal of a write by a binary below the minimum of the schema, which any
// route can meet.
func classifyInstance(err error) (failure, bool) {
	if errors.Is(err, schema.ErrBinaryBelowMinimum) {
		text, args := belowMinimumText(err)

		return failCoded(http.StatusServiceUnavailable, text, errcode.SchemaBinaryBelowMinimum, args...), true
	}

	if errors.Is(err, installation.ErrInvalidInstance) {
		return failCoded(http.StatusBadRequest, "invalid instance ID: use the ID that GET /api/server/instances lists",
			errcode.InstanceInvalidID), true
	}

	if errors.Is(err, installation.ErrInstanceNotFound) {
		return failCoded(http.StatusNotFound, "instance not found: GET /api/server/instances lists the IDs",
			errcode.InstanceNotFound), true
	}

	return fail(0, ""), false
}
