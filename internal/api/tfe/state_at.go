package tfe

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// stateAtMeta is the meta of the state of a workspace at a time. FinalizedAt
// is the finalize time of the state version in Unix milliseconds. RequestedBy
// is the holder of the lock when the client declared the state version, as
// the holder attribute of the state version. Opaque is true when the client
// encrypted the state, so the document holds no resource. NextCursor is the
// cursor of the next page of resources, or "" on the last page. Incomplete
// is true when the server cut the list short of paging.
type stateAtMeta struct {
	FinalizedAt int64  `json:"finalized-at"` //nolint:tagliatelle // The wire name of this API.
	RequestedBy string `json:"requested-by"` //nolint:tagliatelle // The wire name of this API.
	NextCursor  string `json:"next-cursor"`  //nolint:tagliatelle // The wire name of this API.
	Opaque      bool   `json:"opaque"`
	Incomplete  bool   `json:"incomplete"`
}

// stateAtDocument is the response of the state-at route: the state version
// as data, the masked resources of the page as included, and the meta.
type stateAtDocument struct {
	Data     stateVersionResource `json:"data"`
	Included []tofuResourceItem   `json:"included"`
	Meta     stateAtMeta          `json:"meta"`
}

// parseStateAt reads the time of a state-at request: Unix milliseconds.
func parseStateAt(raw string) (time.Time, error) {
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms < 0 {
		return time.Time{}, fmt.Errorf("%w: time %q is not Unix milliseconds", workspace.ErrInvalidState, raw)
	}

	return time.UnixMilli(ms).UTC(), nil
}

// readStateAt serves GET /api/v2/workspaces/{workspace_id}/state-at/{at}: the
// masked state that the workspace held at a time. It takes the same paging
// and filter as the masked resource list of one state version.
func (h *Handler) readStateAt(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	at, err := parseStateAt(r.PathValue("at"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	cursor, size, ok := h.stateResourcePaging(w, r)
	if !ok {
		return
	}

	read, err := h.workspaces.ReadStateAt(ctx, workspace.ID(r.PathValue("workspace_id")), workspace.StateAtRequest{
		At: at, Cursor: cursor, Filter: r.URL.Query().Get("filter[q]"), Size: size,
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	doc, err := h.stateVersionDocument(r, read.Version)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	items := make([]tofuResourceItem, 0, len(read.Page.Resources))

	for i := range read.Page.Resources {
		res := read.Page.Resources[i]
		items = append(items, tofuResourceItem{
			ID:   res.Address,
			Type: stateVersionResourceType,
			Attributes: tofuResourceAttributes{
				Address:  res.Address,
				Type:     res.Type,
				Provider: res.Provider,
				Module:   res.Module,
				Changed:  res.Changed,
			},
		})
	}

	h.writeJSON(ctx, w, http.StatusOK, stateAtDocument{
		Data:     doc,
		Included: items,
		Meta: stateAtMeta{
			FinalizedAt: read.State.FinalizedAt.UnixMilli(),
			RequestedBy: read.State.By,
			NextCursor:  read.Page.NextCursor,
			Opaque:      read.Opaque,
			Incomplete:  read.Page.Incomplete,
		},
	})
}
