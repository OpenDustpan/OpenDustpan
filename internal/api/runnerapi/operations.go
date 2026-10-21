package runnerapi

import (
	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// operationLabels names the routes of this adapter that sit under
// apidesc.Prefix. The runner protocol is outside the prefix by design, and no
// route of the management API sits there yet, so the table is empty. A route
// that moves under the prefix gets its entry here, and the server refuses to
// start without it.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var operationLabels = map[string]apidesc.Label{}

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
			Method: route.Method, Path: route.Path, Actions: actions, Public: false, IdempotencyKey: false,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}
