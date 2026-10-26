package sourcefilter

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/route"
)

// tag is the tag of the routes of the source filter.
const tag = "source-filter"

// operationLabels names the routes of this adapter. The server refuses to
// start without a label for a route under apidesc.Prefix.
func operationLabels() map[string]apidesc.Label {
	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodGet, Path): apidesc.Name("getSourceFilter", tag).
			On(apidesc.KindNamespace).
			WithBodies(apidesc.NoBody, apidesc.Typed[document]("SourceFilter")),
		apidesc.Pattern(http.MethodPut, Path): apidesc.Name("setSourceFilter", tag).
			On(apidesc.KindNamespace).
			WithBodies(apidesc.Typed[request]("SourceFilterRequest"), apidesc.Typed[document]("SourceFilter")),
	}
}

// Operations returns the routes of this adapter for the description of the
// tool-neutral REST API. Each row lists namespace:read first, which the wrapper
// checks before the action of the route.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, rt := range h.routes {
		actions := make([]string, 0, 1+len(rt.Actions))
		actions = append(actions, "namespace:read")

		for _, action := range rt.Actions {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method: rt.Method, Path: rt.Path, Actions: actions, NamespaceActions: []string{"namespace:read"},
			Public: rt.Scope == route.ScopePublic, IdempotencyKey: false,
		})
	}

	return apidesc.Collect(rows, operationLabels())
}
