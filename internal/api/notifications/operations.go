package notifications

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// tagSinks is the tag of every operation of this adapter.
const tagSinks = "notification-sinks"

// operationLabels names the routes of this adapter. The key is the pattern of
// the route. The Idempotency-Key flag comes from Route.IdempotencyKey, so no
// label sets it. No route takes If-Match or gives an ETag.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var operationLabels = map[string]apidesc.Label{
	apidesc.Pattern(http.MethodGet, sinksPath):   apidesc.Name("listNotificationSinks", tagSinks),
	apidesc.Pattern(http.MethodPost, sinksPath):  apidesc.Name("createNotificationSink", tagSinks),
	apidesc.Pattern(http.MethodGet, sinkPath):    apidesc.Name("getNotificationSink", tagSinks),
	apidesc.Pattern(http.MethodPut, sinkPath):    apidesc.Name("replaceNotificationSink", tagSinks),
	apidesc.Pattern(http.MethodDelete, sinkPath): apidesc.Name("deleteNotificationSink", tagSinks),
	apidesc.Pattern(http.MethodPost, testPath):   apidesc.Name("testNotificationSink", tagSinks),
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
