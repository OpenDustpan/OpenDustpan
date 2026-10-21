package registry

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// tagRegistry is the tag of every operation of this adapter.
const tagRegistry = "registry"

// operationLabels names the routes of this adapter. The key is the pattern of
// the route. The Idempotency-Key flag comes from Route.IdempotencyKey, so no
// label sets it. No route takes If-Match, and no answer holds an ETag.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var operationLabels = map[string]apidesc.Label{
	apidesc.Pattern(http.MethodPost, uploadsPath): apidesc.Name("stageRegistryUpload", tagRegistry),
	apidesc.Pattern(http.MethodPut, versionPath):  apidesc.Name("publishProviderRelease", tagRegistry),
	apidesc.Pattern(http.MethodGet, versionsPath): apidesc.Name("listProviderReleases", tagRegistry),
	apidesc.Pattern(http.MethodGet, versionPath):  apidesc.Name("getProviderRelease", tagRegistry),
	apidesc.Pattern(http.MethodPost, keysPath):    apidesc.Name("addRegistryKey", tagRegistry),
	apidesc.Pattern(http.MethodGet, keysPath):     apidesc.Name("listRegistryKeys", tagRegistry),
	apidesc.Pattern(http.MethodDelete, keyPath):   apidesc.Name("removeRegistryKey", tagRegistry),
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
