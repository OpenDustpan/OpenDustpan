package audit

import (
	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/route"
)

// operationLabels names the routes of this adapter that sit under
// apidesc.Prefix. No route of this adapter sits there yet, so the table is
// empty. A route that moves under the prefix gets its entry here, and the
// server refuses to start without it.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var operationLabels = map[string]apidesc.Label{}

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, rt := range h.routes {
		actions := make([]string, 0, len(rt.Actions)+len(rt.ServerActions))
		for _, action := range rt.Actions {
			actions = append(actions, string(action))
		}

		for _, action := range rt.ServerActions {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method:           rt.Method,
			Path:             rt.Path,
			Actions:          actions,
			NamespaceActions: nil,
			Public:           rt.Scope == route.ScopePublic,
			IdempotencyKey:   false,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}
