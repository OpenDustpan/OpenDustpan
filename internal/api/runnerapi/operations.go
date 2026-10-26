package runnerapi

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// tagSourceFilter is the tag of the routes of the source filter.
const tagSourceFilter = "source-filter"

// operationLabels names the routes of this adapter that sit under
// apidesc.Prefix. The runner protocol is outside the prefix by design, and
// the routes of the runner pools stay under /api/runners. The routes of the
// source filter sit under the prefix, so each has its entry here, and the
// server refuses to start without it.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var operationLabels = map[string]apidesc.Label{
	apidesc.Pattern(http.MethodGet, sourceFilterPath): apidesc.Name("getSourceFilter", tagSourceFilter).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[sourceFilterDocument]("SourceFilter")),
	apidesc.Pattern(http.MethodPut, sourceFilterPath): apidesc.Name("setSourceFilter", tagSourceFilter).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.Typed[sourceFilterRequest]("SourceFilterRequest"), apidesc.Typed[sourceFilterDocument]("SourceFilter")),
}

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix. The runner
// protocol is not one of them. No scope of this adapter is public: each scope
// needs a credential, so Public is false for every row.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, route := range h.routes {
		actions := make([]string, 0, len(route.Actions))
		for _, action := range route.Actions {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method:           route.Method,
			Path:             route.Path,
			Actions:          actions,
			NamespaceActions: nil,
			Public:           false,
			IdempotencyKey:   false,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}
