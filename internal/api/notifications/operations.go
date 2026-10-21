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
	apidesc.Pattern(http.MethodGet, sinksPath): apidesc.Name("listNotificationSinks", tagSinks).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[listResponse](
			"ListNotificationSinksResponse", apidesc.Named[sinkDocument]("NotificationSink"),
		)),
	apidesc.Pattern(http.MethodPost, sinksPath): apidesc.Name("createNotificationSink", tagSinks).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.Typed[createBody]("CreateNotificationSinkRequest"), apidesc.Typed[createdDocument](
			"CreatedNotificationSink",
		)),
	apidesc.Pattern(http.MethodGet, sinkPath): apidesc.Name("getNotificationSink", tagSinks).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, sinkBody),
	apidesc.Pattern(http.MethodPut, sinkPath): apidesc.Name("replaceNotificationSink", tagSinks).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.Typed[replaceBody]("ReplaceNotificationSinkRequest"), sinkBody),
	apidesc.Pattern(http.MethodDelete, sinkPath): apidesc.Name("deleteNotificationSink", tagSinks).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
	apidesc.Pattern(http.MethodPost, testPath): apidesc.Name("testNotificationSink", tagSinks).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[testDocument]("NotificationSinkTest")),
}

// sinkBody is the body that the get and the replace routes answer with.
//
//nolint:gochecknoglobals // A fixed value; Go has no constant structs.
var sinkBody = apidesc.Typed[sinkDocument]("NotificationSink")

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
			Method:           route.Method,
			Path:             route.Path,
			Actions:          actions,
			NamespaceActions: nil,
			Public:           route.Scope == ScopePublic,
			IdempotencyKey:   route.IdempotencyKey,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}
