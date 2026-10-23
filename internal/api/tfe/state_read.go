package tfe

import (
	"fmt"
	"net/http"
	"strconv"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// Masked resource read (ADR 41 decisions 1 to 3, 8 and 9): a paged resource
// list and one resource of a state version, in JSON:API and with OpenTofu
// words. Each secret value answers as a marker with its value path, never as
// plain text, ciphertext or sealed bytes. A state the client encrypted
// answers 409 on both routes, while the outputs list still works.

const (
	// stateVersionResourceType is the JSON:API type of the masked resources.
	stateVersionResourceType = "state-version-resources"
	// defaultResourcePageSize answers the resource list when the client sends
	// no page size.
	defaultResourcePageSize = 100
	// maxResourcePageSize bounds one page of the resource list.
	maxResourcePageSize = 500
)

// tofuResourceAttributes are the attributes of one masked resource: the
// address as stored, the type, the provider, the module ("" for the root
// module) and whether it differs from the previous version or is new. The
// list answers the structure with no attribute value.
type tofuResourceAttributes struct {
	Address  string `json:"address"`
	Type     string `json:"type"`
	Provider string `json:"provider"`
	Module   string `json:"module"`
	Changed  bool   `json:"changed"`
}

// tofuResourceItem is one entry of the masked resource list.
type tofuResourceItem struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Attributes tofuResourceAttributes `json:"attributes"`
}

// tofuResourceListMeta is the meta of the masked resource list: the cursor
// of the next page, or "" on the last page, and whether the server cut the
// list short of paging.
type tofuResourceListMeta struct {
	NextCursor string `json:"next-cursor"` //nolint:tagliatelle // The wire name of this API; it must not change.
	Incomplete bool   `json:"incomplete"`
}

// tofuResourceListDocument is the response of the masked resource list route.
type tofuResourceListDocument struct {
	Data []tofuResourceItem   `json:"data"`
	Meta tofuResourceListMeta `json:"meta"`
}

// tofuInstanceItem is one instance of a masked resource with its attributes.
// Each secret value answers as a marker with its value path.
type tofuInstanceItem struct {
	Attributes any `json:"attributes"`
}

// tofuDetailAttributes are the attributes of one masked resource: its
// identity, its instances with masked attributes, and the value path of each
// marker. Masked is authoritative: a real value with the shape of a marker
// but no entry in Masked is a real value.
type tofuDetailAttributes struct {
	Address   string             `json:"address"`
	Type      string             `json:"type"`
	Provider  string             `json:"provider"`
	Module    string             `json:"module"`
	Instances []tofuInstanceItem `json:"instances"`
	Masked    []string           `json:"masked"`
}

// tofuDetailItem is one masked resource.
type tofuDetailItem struct {
	ID         string               `json:"id"`
	Type       string               `json:"type"`
	Attributes tofuDetailAttributes `json:"attributes"`
}

// tofuDetailDocument is the response of the one masked resource route.
type tofuDetailDocument struct {
	Data tofuDetailItem `json:"data"`
}

// stateResourcePaging reads the cursor and size of a masked resource list
// request: an absent size means the default page. It writes the error and
// reports false for a size outside its range.
func (h *Handler) stateResourcePaging(w http.ResponseWriter, r *http.Request) (string, int, bool) {
	cursor := r.URL.Query().Get("page[cursor]")
	size := defaultResourcePageSize

	if raw := r.URL.Query().Get("page[size]"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > maxResourcePageSize {
			h.writeError(r.Context(), w, fmt.Errorf(
				"%w: page[size] must be a number from 1 to %d", errInvalidQuery, maxResourcePageSize,
			))

			return "", 0, false
		}

		size = n
	}

	return cursor, size, true
}

// listStateResources serves GET
// /api/v2/state-versions/{state_version_id}/resources: one page of the
// masked resource list of a finalized state version. The filter[q] query
// value is a substring of the address or of the resource type, or absent for
// no filter: it never matches attribute values, sensitive values or outputs.
// Paging applies after the filter, so a page holds only matching resources.
// An unknown version answers 404; a state the client encrypted answers 409.
func (h *Handler) listStateResources(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	cursor, size, ok := h.stateResourcePaging(w, r)
	if !ok {
		return
	}

	filter := r.URL.Query().Get("filter[q]")

	id := workspace.StateVersionID(r.PathValue("state_version_id"))

	page, err := h.workspaces.ReadResources(ctx, id, cursor, size, filter)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	items := make([]tofuResourceItem, 0, len(page.Resources))

	for i := range page.Resources {
		res := page.Resources[i]
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

	h.writeJSON(ctx, w, http.StatusOK, tofuResourceListDocument{
		Data: items,
		Meta: tofuResourceListMeta{NextCursor: page.NextCursor, Incomplete: page.Incomplete},
	})
}

// getStateResource serves GET
// /api/v2/state-versions/{state_version_id}/resources/{address}: one resource
// of a finalized state version with its instances masked. The address in the
// path is path-escaped, so an instance index such as ["key"] stays one
// segment. An unknown version and an unknown address answer 404; a state the
// client encrypted answers 409.
func (h *Handler) getStateResource(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	detail, err := h.workspaces.ReadResource(
		ctx, workspace.StateVersionID(r.PathValue("state_version_id")), r.PathValue("address"),
	)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	instances := make([]tofuInstanceItem, 0, len(detail.Instances))

	for i := range detail.Instances {
		instances = append(instances, tofuInstanceItem{Attributes: detail.Instances[i].Attributes})
	}

	masked := detail.Masked
	if masked == nil {
		masked = []string{}
	}

	h.writeJSON(ctx, w, http.StatusOK, tofuDetailDocument{
		Data: tofuDetailItem{
			ID:   detail.Address,
			Type: stateVersionResourceType,
			Attributes: tofuDetailAttributes{
				Address:   detail.Address,
				Type:      detail.Type,
				Provider:  detail.Provider,
				Module:    detail.Module,
				Instances: instances,
				Masked:    masked,
			},
		},
	})
}
