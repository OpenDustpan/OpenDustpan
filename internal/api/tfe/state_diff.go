package tfe

import (
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// Masked state diff (ADR 41 decisions 1 to 3, 8 and 9): the diff of two state
// versions of one workspace, in JSON:API and with OpenTofu words. The path
// version is the "to" side; the "from" query value names the older version.
// Both versions must belong to the same workspace: a from version of another
// workspace answers 404 as if it did not exist. The diff answers the added,
// removed and changed resources by address, with the resource-relative value
// paths of each change. A secret value is never in the diff: a changed,
// added or removed sensitive value is one path in changed-secrets. A path is
// sensitive when the masked read would mask it on either side. A state either
// side encrypted answers 409.

const (
	// stateVersionDiffType is the JSON:API type of the state diff.
	stateVersionDiffType = "state-version-diffs"
)

// tofuDiffChangedAttributes are the attributes of one changed resource: the
// address as stored, the resource-relative value paths that changed, and the
// paths of secrets that changed. A secret value is never in the diff.
type tofuDiffChangedAttributes struct {
	Address string `json:"address"`
	// ChangedPaths holds the resource-relative value paths that changed.
	ChangedPaths []string `json:"changed-paths"` //nolint:tagliatelle // The wire name of this API; it must not change.
	// ChangedSecrets holds the resource-relative paths of secrets that changed.
	ChangedSecrets []string `json:"changed-secrets"` //nolint:tagliatelle // The wire name of this API; it must not change.
}

// tofuDiffAttributes are the attributes of one state diff: the sorted
// addresses added and removed, and the sorted changed resources with their
// paths.
type tofuDiffAttributes struct {
	Added   []string                    `json:"added"`
	Removed []string                    `json:"removed"`
	Changed []tofuDiffChangedAttributes `json:"changed"`
}

// tofuDiffData is one state diff as a JSON:API resource.
type tofuDiffData struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Attributes tofuDiffAttributes `json:"attributes"`
}

// tofuDiffDocument is the response of the state diff route.
type tofuDiffDocument struct {
	Data tofuDiffData         `json:"data"`
	Meta tofuResourceListMeta `json:"meta"`
}

// stateDiffRoutes returns the state diff route: the diff of two state
// versions of one workspace, in JSON:API and with OpenTofu words. It needs
// unit:read on the unit of the workspace.
func (h *Handler) stateDiffRoutes() []Route {
	return []Route{
		{
			Method: http.MethodGet,
			Path:   "/api/v2/state-versions/{state_version_id}/diff",
			// A masked diff answers the added, removed and changed resources
			// by address with the changed value paths, so it needs unit:read
			// on the unit of the workspace. The path version is the to side;
			// the from query value names the older version. A missing from
			// value answers 422. Unknown versions, and a from version of
			// another workspace, answer 404; a state either side encrypted
			// answers 409.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: h.resolveStateVersionID,
			Serve:   h.diffState,
		},
	}
}

// diffState serves GET
// /api/v2/state-versions/{state_version_id}/diff?from=<state_version_id>: the
// diff of two finalized state versions of one workspace. The path version is
// the to side. A missing from value answers 422. An unknown version on
// either side, and a from version of another workspace, answer 404. A state
// either side encrypted answers 409.
func (h *Handler) diffState(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	cursor, size, ok := h.stateResourcePaging(w, r)
	if !ok {
		return
	}

	from := r.URL.Query().Get("from")
	if from == "" {
		h.writeError(ctx, w, fmt.Errorf("%w: diff from version is missing", workspace.ErrInvalidState))

		return
	}

	to := workspace.StateVersionID(r.PathValue("state_version_id"))

	diff, err := h.workspaces.DiffStates(ctx, to, workspace.StateVersionID(from), cursor, size)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	changed := make([]tofuDiffChangedAttributes, 0, len(diff.Changed))

	for i := range diff.Changed {
		paths := diff.Changed[i].ChangedPaths
		if paths == nil {
			paths = []string{}
		}

		secrets := diff.Changed[i].ChangedSecrets
		if secrets == nil {
			secrets = []string{}
		}

		changed = append(changed, tofuDiffChangedAttributes{
			Address:        diff.Changed[i].Address,
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

	h.writeJSON(ctx, w, http.StatusOK, tofuDiffDocument{
		Data: tofuDiffData{
			ID:   to.String(),
			Type: stateVersionDiffType,
			Attributes: tofuDiffAttributes{
				Added:   added,
				Removed: removed,
				Changed: changed,
			},
		},
		Meta: tofuResourceListMeta{NextCursor: diff.NextCursor, Incomplete: diff.Incomplete},
	})
}
