package runnerapi

import (
	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix. The runner
// protocol is outside the prefix by design, so it has no entry, and the
// management API lives in internal/api/runnerpools. A route under the prefix
// with no entry stops the server at start.
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

	return apidesc.Collect(rows, nil)
}
