package objectcontext

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// tagContext is the tag of every operation of this adapter.
const tagContext = "context"

const (
	namespaceContextPath = "/api/dustpan/v1/namespaces/{namespace}/context"
	unitContextPath      = "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/context"
)

// operationLabels names the routes of this adapter. The key is the pattern of
// the route.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var operationLabels = map[string]apidesc.Label{
	apidesc.Pattern(http.MethodGet, namespaceContextPath):    apidesc.Name("getNamespaceContext", tagContext),
	apidesc.Pattern(http.MethodPatch, namespaceContextPath):  apidesc.Name("updateNamespaceContext", tagContext),
	apidesc.Pattern(http.MethodDelete, namespaceContextPath): apidesc.Name("deleteNamespaceContext", tagContext),
	apidesc.Pattern(http.MethodGet, unitContextPath):         apidesc.Name("getUnitContext", tagContext),
	apidesc.Pattern(http.MethodPatch, unitContextPath):       apidesc.Name("updateUnitContext", tagContext),
	apidesc.Pattern(http.MethodDelete, unitContextPath):      apidesc.Name("deleteUnitContext", tagContext),
}

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, route := range h.routes {
		actions := make([]string, 0, len(route.Reads)+len(route.Actions))
		for _, action := range route.Reads {
			actions = append(actions, string(action))
		}

		for _, action := range route.Actions {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method: route.Method, Path: route.Path, Actions: actions, Public: route.Scope == ScopePublic,
			IdempotencyKey: false,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}
