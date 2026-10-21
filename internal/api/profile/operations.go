package profile

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// tagProfiles is the tag of every operation of this adapter.
const tagProfiles = "profiles"

// operationLabels names the routes of this adapter. The key is the pattern of
// the route. The Idempotency-Key flag comes from Route.IdempotencyKey, so no
// label sets it.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var operationLabels = map[string]apidesc.Label{
	apidesc.Pattern(http.MethodGet, profilesPath):   apidesc.Name("listProfiles", tagProfiles),
	apidesc.Pattern(http.MethodPost, profilesPath):  apidesc.Name("createProfile", tagProfiles).WithETag(),
	apidesc.Pattern(http.MethodGet, profilePath):    apidesc.Name("getProfile", tagProfiles).WithETag(),
	apidesc.Pattern(http.MethodDelete, profilePath): apidesc.Name("deleteProfile", tagProfiles).WithIfMatch(),
	apidesc.Pattern(http.MethodGet, revisionsPath):  apidesc.Name("listProfileRevisions", tagProfiles),
	apidesc.Pattern(http.MethodPost, revisionsPath): apidesc.Name("createProfileRevision", tagProfiles).
		WithIfMatchRequired().
		WithETag(),
	apidesc.Pattern(http.MethodGet, revisionsPath+"/{revision}"): apidesc.Name("getProfileRevision", tagProfiles).
		WithETag(),
}

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, route := range h.routes {
		actions := make([]string, 0, len(route.Actions))
		for _, action := range route.Actions {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method: route.Method, Path: route.Path, Actions: actions, Public: route.Scope == ScopePublic,
			IdempotencyKey: route.IdempotencyKey,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}
