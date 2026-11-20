package state

import (
	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/route"
)

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

	return apidesc.Collect(rows, apidesc.UnitsStateLabels())
}
