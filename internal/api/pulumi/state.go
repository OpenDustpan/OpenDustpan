package pulumi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
)

const (
	// defaultStateLimit answers the resource list when the client sends no
	// limit.
	defaultStateLimit = 100
	// maxStateLimit bounds one page of the resource list.
	maxStateLimit = 500
	// latestVersionName addresses the latest checkpoint of a stack in the
	// state routes.
	latestVersionName = "latest"
)

// stateTarget is the stack and the checkpoint version a state route reads:
// an explicit version, or the latest checkpoint.
type stateTarget struct {
	ref     stack.Ref
	version int64
	latest  bool
}

// stateResourceItem is one entry of the masked resource list.
type stateResourceItem struct {
	URN      string `json:"urn"`
	Type     string `json:"type"`
	Provider string `json:"provider"`
	Parent   string `json:"parent"`
	Changed  bool   `json:"changed"`
}

// listStateResourcesResponse is one page of the masked resource list.
// NextCursor names the cursor of the next page, or is empty on the last
// page. Incomplete with a Reason says the server cut the list short of
// paging.
type listStateResourcesResponse struct {
	Resources  []stateResourceItem `json:"resources"`
	NextCursor string              `json:"nextCursor"`
	Incomplete bool                `json:"incomplete"`
	Reason     string              `json:"reason,omitempty"`
}

// stateResourceResponse is one resource with masked inputs and outputs.
// Masked holds the value path of each marker and is authoritative.
type stateResourceResponse struct {
	URN      string   `json:"urn"`
	Type     string   `json:"type"`
	Provider string   `json:"provider"`
	Parent   string   `json:"parent"`
	Inputs   any      `json:"inputs"`
	Outputs  any      `json:"outputs"`
	Masked   []string `json:"masked"`
}

// stateOutputsResponse are the masked outputs of the stack resource of one
// version.
type stateOutputsResponse struct {
	Outputs any      `json:"outputs"`
	Masked  []string `json:"masked"`
}

// stateRoutes returns the masked state read routes (ADR 41 decisions 1 to
// 3 and 7): a paged resource list, one resource and the stack outputs of
// one checkpoint version. Each secret value answers as a marker with its
// value path, never as plain or ciphertext. The URN in the one-resource
// route is path-escaped, so that "/" becomes "%2F" and the URN stays one
// segment. Every route reads state, so
// each needs unit:read.
func (h *Handler) stateRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodGet,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/state/{version}/resources",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: resolveGetStack,
			Serve:   h.listStateResources,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/state/{version}/resources/{urn}",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: resolveGetStack,
			Serve:   h.getStateResource,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/state/{version}/outputs",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: resolveGetStack,
			Serve:   h.getStateOutputs,
		},
	}
}

// stateTargetOf parses the stack reference and the version of a state
// request: "latest" or a non-negative integer. It writes the error and
// reports false when either fails.
func (h *Handler) stateTargetOf(w http.ResponseWriter, r *http.Request) (stateTarget, bool) {
	var zero stateTarget

	ref, ok := h.updateRef(w, r)
	if !ok {
		return zero, false
	}

	raw := r.PathValue("version")

	if raw == latestVersionName {
		return stateTarget{ref: ref, version: 0, latest: true}, true
	}

	version, err := stack.ParseStackVersion(raw)
	if err != nil {
		h.writeError(r.Context(), w, err)

		return zero, false
	}

	return stateTarget{ref: ref, version: version, latest: false}, true
}

// stateCurrent returns the checkpoint the target names: the latest one, or
// the one of the explicit version. It writes the error and reports false
// when the version has no checkpoint.
func (h *Handler) stateCurrent(
	w http.ResponseWriter, r *http.Request, target stateTarget,
) (stack.Checkpoint, bool) {
	var zero stack.Checkpoint

	var (
		cp  stack.Checkpoint
		err error
	)

	if target.latest {
		cp, err = h.updates.Export(r.Context(), target.ref)
	} else {
		cp, err = h.updates.ExportVersion(r.Context(), target.ref, target.version)
	}

	if err != nil {
		h.writeError(r.Context(), w, err)

		return zero, false
	}

	return cp, true
}

// statePrevious returns the deployment of the version before the target, or
// nil when there is none: the first version, or a previous version without
// a checkpoint. It writes the error and reports false when the lookup
// itself fails.
func (h *Handler) statePrevious(
	w http.ResponseWriter, r *http.Request, target stateTarget, current stack.Checkpoint,
) ([]byte, bool) {
	version := target.version

	if target.latest {
		u, err := h.updates.Get(r.Context(), target.ref, current.Update)
		if err != nil {
			h.writeError(r.Context(), w, err)

			return nil, false
		}

		version = u.Version
	}

	if version <= 1 {
		return nil, true
	}

	prev, err := h.updates.ExportVersion(r.Context(), target.ref, version-1)
	if err != nil {
		if errors.Is(err, stack.ErrNoState) {
			return nil, true
		}

		h.writeError(r.Context(), w, err)

		return nil, false
	}

	return prev.Deployment, true
}

// statePaging reads the cursor and limit query values of a resource list
// request: an absent limit means the default page. It writes the error and
// reports false for a value outside its range.
func (h *Handler) statePaging(w http.ResponseWriter, r *http.Request) (string, int, bool) {
	cursor := r.URL.Query().Get("cursor")
	limit := defaultStateLimit

	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > maxStateLimit {
			h.writeError(r.Context(), w, invalidStateLimit(raw))

			return "", 0, false
		}

		limit = n
	}

	if cursor != "" {
		if n, err := strconv.Atoi(cursor); err != nil || n < 0 {
			h.writeError(r.Context(), w, invalidStateCursor(cursor))

			return "", 0, false
		}
	}

	return cursor, limit, true
}

// invalidStateLimit reports a resource list limit outside 1 to 500.
func invalidStateLimit(raw string) error {
	return fmt.Errorf("%w: resource limit %q", stack.ErrInvalidUpdate, raw)
}

// invalidStateCursor reports a resource list cursor that is not a
// non-negative offset.
func invalidStateCursor(raw string) error {
	return fmt.Errorf("%w: resource cursor %q", stack.ErrInvalidUpdate, raw)
}

// listStateResources answers one page of the masked resource list of one
// checkpoint version.
func (h *Handler) listStateResources(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()
	target, ok := h.stateTargetOf(w, r)

	if !ok {
		return
	}

	cursor, limit, ok := h.statePaging(w, r)
	if !ok {
		return
	}

	current, ok := h.stateCurrent(w, r, target)
	if !ok {
		return
	}

	previous, ok := h.statePrevious(w, r, target, current)
	if !ok {
		return
	}

	page, err := stack.ListStateResources(current.Deployment, previous, cursor, limit)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	items := make([]stateResourceItem, 0, len(page.Resources))
	for i := range page.Resources {
		items = append(items, stateResourceItem{
			URN:      page.Resources[i].URN,
			Type:     page.Resources[i].Type,
			Provider: page.Resources[i].Provider,
			Parent:   page.Resources[i].Parent,
			Changed:  page.Resources[i].Changed,
		})
	}

	h.writeJSON(ctx, w, http.StatusOK, listStateResourcesResponse{
		Resources:  items,
		NextCursor: page.NextCursor,
		Incomplete: page.Incomplete,
		Reason:     page.Reason,
	})
}

// getStateResource answers one resource of one checkpoint version with its
// inputs and outputs masked.
func (h *Handler) getStateResource(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()
	target, ok := h.stateTargetOf(w, r)

	if !ok {
		return
	}

	current, ok := h.stateCurrent(w, r, target)
	if !ok {
		return
	}

	detail, err := stack.ReadStateResource(current.Deployment, r.PathValue("urn"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, stateResourceResponse{
		URN:      detail.URN,
		Type:     detail.Type,
		Provider: detail.Provider,
		Parent:   detail.Parent,
		Inputs:   detail.Inputs,
		Outputs:  detail.Outputs,
		Masked:   detail.Masked,
	})
}

// getStateOutputs answers the masked outputs of the stack resource of one
// checkpoint version.
func (h *Handler) getStateOutputs(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()
	target, ok := h.stateTargetOf(w, r)

	if !ok {
		return
	}

	current, ok := h.stateCurrent(w, r, target)
	if !ok {
		return
	}

	outputs, err := stack.ReadStateOutputs(current.Deployment)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, stateOutputsResponse{Outputs: outputs.Outputs, Masked: outputs.Masked})
}
