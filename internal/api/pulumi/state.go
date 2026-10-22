package pulumi

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
)

const (
	// defaultStateLimit answers the resource list when the client sends no
	// limit.
	defaultStateLimit = 100
	// maxStateLimit bounds one page of the resource list.
	maxStateLimit = 500
)

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

// stateDiffChangedItem is one resource present in both versions with a
// change: the URN, the resource-relative value paths that changed, and the
// paths of secrets whose ciphertext changed. A secret value is never in the
// diff: a changed secret is one path in changedSecrets. Secrets compare as
// ciphertext, and the CLI may encrypt the same value again with a new nonce,
// so changedSecrets may name a secret whose value did not change. The
// server never decrypts for a diff.
type stateDiffChangedItem struct {
	URN            string   `json:"urn"`
	ChangedPaths   []string `json:"changedPaths"`
	ChangedSecrets []string `json:"changedSecrets"`
}

// diffStateResponse is one page of the diff of two checkpoint versions:
// the sorted URNs added and removed, and the sorted changed resources with
// their paths. NextCursor names the cursor of the next page, or is empty on
// the last page. Incomplete with a Reason says the server cut the list short
// of paging.
type diffStateResponse struct {
	Added      []string               `json:"added"`
	Removed    []string               `json:"removed"`
	Changed    []stateDiffChangedItem `json:"changed"`
	NextCursor string                 `json:"nextCursor"`
	Incomplete bool                   `json:"incomplete"`
	Reason     string                 `json:"reason,omitempty"`
}

// stateRoutes returns the masked state read routes (ADR 41 decisions 1 to
// 3 and 7): a paged resource list, one resource and the stack outputs of
// one checkpoint version, and the diff of two versions. Each secret value
// answers as a marker with its value path, never as plain or ciphertext;
// the diff answers no value at all, only URNs and value paths. The reveal
// call (decisions 4 and 5) answers the plain value at one masked path with
// an audit entry of the kind reveal. The URN in the one-resource route is
// path-escaped, so that "/" becomes "%2F" and the URN stays one segment.
// The diff path holds the literal "diff", so it does not clash with the
// version routes, which always carry one more segment (resources, outputs
// or reveal) after the version. Every route reads state, so each needs
// unit:read; the reveal call also needs unit:read-secrets.
func (h *Handler) stateRoutes() []Route {
	versioned := []Route{
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

	// The diff path holds the literal "diff", so it does not clash with the
	// version routes, which always carry one more segment (resources or
	// outputs) after the version.
	diff := Route{
		Method:  http.MethodGet,
		Path:    "/api/stacks/{namespace}/{project}/{stack}/state/diff",
		Scope:   ScopeAuthorized,
		Actions: []identity.Action{identity.ActionUnitRead},
		Resolve: resolveGetStack,
		Serve:   h.diffState,
	}

	at := Route{
		Method:  http.MethodGet,
		Path:    "/api/stacks/{namespace}/{project}/{stack}/state-at/{at}/resources",
		Scope:   ScopeAuthorized,
		Actions: []identity.Action{identity.ActionUnitRead},
		Resolve: resolveGetStack,
		Serve:   h.listStateAtResources,
	}

	return []Route{versioned[0], versioned[1], versioned[2], diff, at, h.revealRoute()}
}

// stateTargetOf parses the stack reference and the version of a state
// request: "latest" or a non-negative integer. It writes the error and
// reports false when either fails.
func (h *Handler) stateTargetOf(w http.ResponseWriter, r *http.Request) (stack.Ref, stack.StateSelector, bool) {
	var (
		noRef stack.Ref
		noSel stack.StateSelector
	)

	ref, ok := h.updateRef(w, r)
	if !ok {
		return noRef, noSel, false
	}

	sel, err := stack.ParseStateSelector(r.PathValue("version"))
	if err != nil {
		h.writeError(r.Context(), w, err)

		return noRef, noSel, false
	}

	return ref, sel, true
}

// stateCurrent returns the state that sel names, with its version. It writes
// the error and reports false when the version has no checkpoint.
func (h *Handler) stateCurrent(
	w http.ResponseWriter, r *http.Request, ref stack.Ref, sel stack.StateSelector,
) (stack.SelectedState, bool) {
	st, err := h.updates.SelectState(r.Context(), ref, sel)
	if err != nil {
		h.writeError(r.Context(), w, err)

		var none stack.SelectedState

		return none, false
	}

	return st, true
}

// stateCheckpoint returns the checkpoint that sel names, for a read that
// needs no version: it does not read the update that saved the checkpoint.
// It writes the error and reports false when the version has no checkpoint.
func (h *Handler) stateCheckpoint(
	w http.ResponseWriter, r *http.Request, ref stack.Ref, sel stack.StateSelector,
) (stack.Checkpoint, bool) {
	cp, err := h.updates.SelectCheckpoint(r.Context(), ref, sel)
	if err != nil {
		h.writeError(r.Context(), w, err)

		var none stack.Checkpoint

		return none, false
	}

	return cp, true
}

// statePrevious returns the deployment of the version before current, or nil
// when there is none. It writes the error and reports false when the lookup
// itself fails.
func (h *Handler) statePrevious(
	w http.ResponseWriter, r *http.Request, ref stack.Ref, current stack.SelectedState,
) ([]byte, bool) {
	prev, err := h.updates.PreviousDeployment(r.Context(), ref, current)
	if err != nil {
		h.writeError(r.Context(), w, err)

		return nil, false
	}

	return prev, true
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
// checkpoint version. The filter[q] query value is a substring of the URN
// or of the resource type, or absent for no filter: it never matches
// attribute values. Paging applies after the filter, so a page holds only
// matching resources.
func (h *Handler) listStateResources(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()
	ref, sel, ok := h.stateTargetOf(w, r)
	if !ok {
		return
	}

	cursor, limit, ok := h.statePaging(w, r)
	if !ok {
		return
	}

	filter := r.URL.Query().Get("filter[q]")

	current, ok := h.stateCurrent(w, r, ref, sel)
	if !ok {
		return
	}

	previous, ok := h.statePrevious(w, r, ref, current)
	if !ok {
		return
	}

	page, err := stack.ListStateResources(current.Checkpoint.Deployment, previous, cursor, limit, filter)
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

// stateAtResourcesResponse is one page of the masked resource list of a stack
// at a time. Version is the update that held the state, and EndedAt is its end
// time in Unix milliseconds. Running is the version of an update that ran at
// the time, or 0: the state is then the one before that update. RequestedBy
// is the subject that asked for the update of Version, as in the update
// history. NameAtTime is the name of the stack at the time, as project/stack,
// and CurrentName is its name now. Each is omitted when the inventory change
// log does not know it. For a stack that was never renamed they are equal.
type stateAtResourcesResponse struct {
	Resources   []stateResourceItem `json:"resources"`
	NextCursor  string              `json:"nextCursor"`
	Reason      string              `json:"reason,omitempty"`
	Version     int                 `json:"version"`
	EndedAt     int64               `json:"endedAt"`
	Running     int                 `json:"running"`
	RequestedBy string              `json:"requestedBy"`
	Incomplete  bool                `json:"incomplete"`
	NameAtTime  string              `json:"nameAtTime,omitempty"`
	CurrentName string              `json:"currentName,omitempty"`
}

// parseStateAt reads the time of a state-at request: Unix milliseconds.
func parseStateAt(raw string) (time.Time, error) {
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms < 0 {
		return time.Time{}, fmt.Errorf("%w: time %q is not Unix milliseconds", stack.ErrInvalidUpdate, raw)
	}

	return time.UnixMilli(ms).UTC(), nil
}

// listStateAtResources answers one page of the masked resource list of the
// state that a stack held at a time. It takes the same paging and filter as
// the list of one version.
func (h *Handler) listStateAtResources(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	ref, ok := h.updateRef(w, r)
	if !ok {
		return
	}

	at, err := parseStateAt(r.PathValue("at"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	cursor, limit, ok := h.statePaging(w, r)
	if !ok {
		return
	}

	current, found, err := h.updates.ExportAt(ctx, ref, at)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	selected := stack.SelectedState{Checkpoint: current, Version: int64(found.Version)}

	previous, ok := h.statePrevious(w, r, ref, selected)
	if !ok {
		return
	}

	page, err := stack.ListStateResources(current.Deployment, previous, cursor, limit, r.URL.Query().Get("filter[q]"))
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

	h.writeJSON(ctx, w, http.StatusOK, stateAtResourcesResponse{
		Resources:   items,
		NextCursor:  page.NextCursor,
		Reason:      page.Reason,
		Version:     found.Version,
		EndedAt:     found.EndedAt.UnixMilli(),
		Running:     found.Running,
		RequestedBy: found.CreatedBy,
		Incomplete:  page.Incomplete,
		NameAtTime:  found.Names.AtTime,
		CurrentName: found.Names.Current,
	})
}

// getStateResource answers one resource of one checkpoint version with its
// inputs and outputs masked.
func (h *Handler) getStateResource(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()
	ref, sel, ok := h.stateTargetOf(w, r)
	if !ok {
		return
	}

	current, ok := h.stateCheckpoint(w, r, ref, sel)
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

// diffCheckpointOf resolves one side of a diff request: "latest" names the
// latest checkpoint, otherwise a non-negative integer version. It writes the
// error and reports false when the value is missing or invalid, or when the
// version has no checkpoint.
func (h *Handler) diffCheckpointOf(
	w http.ResponseWriter, r *http.Request, ref stack.Ref, name, raw string,
) (stack.Checkpoint, bool) {
	var none stack.Checkpoint

	if raw == "" {
		h.writeError(r.Context(), w, invalidDiffVersion(name, raw))

		return none, false
	}

	sel, err := stack.ParseStateSelector(raw)
	if err != nil {
		h.writeError(r.Context(), w, err)

		return none, false
	}

	return h.stateCheckpoint(w, r, ref, sel)
}

// invalidDiffVersion reports a missing or empty diff side.
func invalidDiffVersion(name, raw string) error {
	return fmt.Errorf("%w: diff %s version %q", stack.ErrInvalidUpdate, name, raw)
}

// diffState answers the diff of two checkpoint versions named by the from
// and to query values: each is a version or "latest". It answers the sorted
// URNs added and removed, and for each changed resource the
// resource-relative value paths that changed. A secret value is never in the
// diff: a changed secret is one path in changedSecrets, compared as
// ciphertext, so a re-encrypted but unchanged value may still be listed. The
// server never decrypts for a diff. The page holds at most limit differing
// URNs; NextCursor pages through them.
func (h *Handler) diffState(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	ref, ok := h.updateRef(w, r)
	if !ok {
		return
	}

	cursor, limit, ok := h.statePaging(w, r)
	if !ok {
		return
	}

	query := r.URL.Query()

	from, ok := h.diffCheckpointOf(w, r, ref, "from", query.Get("from"))
	if !ok {
		return
	}

	to, ok := h.diffCheckpointOf(w, r, ref, "to", query.Get("to"))
	if !ok {
		return
	}

	diff, err := stack.DiffStates(from.Deployment, to.Deployment, cursor, limit)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	changed := make([]stateDiffChangedItem, 0, len(diff.Changed))
	for i := range diff.Changed {
		paths := diff.Changed[i].ChangedPaths
		if paths == nil {
			paths = []string{}
		}

		secrets := diff.Changed[i].ChangedSecrets
		if secrets == nil {
			secrets = []string{}
		}

		changed = append(changed, stateDiffChangedItem{
			URN:            diff.Changed[i].URN,
			ChangedPaths:   paths,
			ChangedSecrets: secrets,
		})
	}

	added := diff.Added
	if added == nil {
		added = []string{}
	}

	removed := diff.Removed
	if removed == nil {
		removed = []string{}
	}

	h.writeJSON(ctx, w, http.StatusOK, diffStateResponse{
		Added:      added,
		Removed:    removed,
		Changed:    changed,
		NextCursor: diff.NextCursor,
		Incomplete: diff.Incomplete,
		Reason:     diff.Reason,
	})
}

// getStateOutputs answers the masked outputs of the stack resource of one
// checkpoint version.
func (h *Handler) getStateOutputs(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()
	ref, sel, ok := h.stateTargetOf(w, r)
	if !ok {
		return
	}

	current, ok := h.stateCheckpoint(w, r, ref, sel)
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
