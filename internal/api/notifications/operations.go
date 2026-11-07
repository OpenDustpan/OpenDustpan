package notifications

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// tagSinks is the tag of every operation of this adapter.
const tagSinks = "notification-sinks"

// operationLabels names the routes of this adapter. The key is the pattern of
// the route. The Idempotency-Key flag comes from Route.IdempotencyKey, so no
// label sets it. No route takes If-Match or gives an ETag: a write sends the version
// that it read in its body.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var operationLabels = map[string]apidesc.Label{
	apidesc.Pattern(http.MethodGet, sinksPath): apidesc.Name("listNotificationSinks", tagSinks).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[listResponse](
			"ListNotificationSinksResponse", apidesc.Named[sinkDocument]("NotificationSink"),
		)),
	apidesc.Pattern(http.MethodPost, sinksPath): apidesc.Name("createNotificationSink", tagSinks).
		WithStatus(http.StatusCreated).
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
	apidesc.Pattern(http.MethodGet, subscriptionsPath): apidesc.Name("listNotificationSubscriptions", tagSinks).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[subscriptionListResponse](
			"ListNotificationSubscriptionsResponse", subscriptionNames()...,
		)),
	apidesc.Pattern(http.MethodPost, subscriptionsPath): apidesc.Name("createNotificationSubscription", tagSinks).
		WithStatus(http.StatusCreated).
		On(apidesc.KindNamespace).
		WithBodies(
			apidesc.Typed[createSubscriptionBody](
				"CreateNotificationSubscriptionRequest",
				apidesc.Named[filterBody]("NotificationFilterRequest"),
			),
			subscriptionBody(),
		),
	apidesc.Pattern(http.MethodGet, subscriptionPath): apidesc.Name("getNotificationSubscription", tagSinks).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, subscriptionBody()),
	apidesc.Pattern(http.MethodPut, subscriptionPath): apidesc.Name("replaceNotificationSubscription", tagSinks).
		On(apidesc.KindNamespace).
		WithBodies(
			apidesc.Typed[replaceSubscriptionBody](
				"ReplaceNotificationSubscriptionRequest",
				apidesc.Named[filterBody]("NotificationFilterRequest"),
			),
			subscriptionBody(),
		),
	apidesc.Pattern(http.MethodDelete, subscriptionPath): apidesc.Name("deleteNotificationSubscription", tagSinks).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
	apidesc.Pattern(http.MethodGet, historyPath): apidesc.Name("listNotificationDeliveries", tagSinks).
		On(apidesc.KindNamespace).
		WithQuery(
			apidesc.IntegerParam("pageSize", "The largest number of deliveries on the page, from 1 to 200."),
			apidesc.StringParam("pageToken", "The nextPageToken of the page before, for the same subscription."),
		).
		WithBodies(apidesc.NoBody, apidesc.Typed[historyResponse](
			"ListNotificationDeliveriesResponse", apidesc.Named[deliveryDocument]("NotificationDelivery"),
		)),
	apidesc.Pattern(http.MethodPost, redeliverPath): apidesc.Name("redeliverNotification", tagSinks).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
}

// subscriptionBody returns the body that the subscription routes answer with.
func subscriptionBody() apidesc.Body {
	return apidesc.Typed[subscriptionDocument]("NotificationSubscription", subscriptionNames()...)
}

// subscriptionNames returns the types that a subscription document holds.
func subscriptionNames() []apidesc.Declaration {
	return []apidesc.Declaration{apidesc.Named[filterDocument]("NotificationFilter")}
}

// queryOf returns the declared query parameters of the request.
func queryOf(r *http.Request) apidesc.Query {
	return apidesc.QueryOf(r, operationLabels)
}

// sinkBody is the body that the get and the replace routes answer with.
//
//nolint:gochecknoglobals // A fixed value; Go has no constant structs.
var sinkBody = apidesc.Typed[sinkDocument]("NotificationSink")

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, rt := range h.routes {
		actions := make([]string, 0, len(rt.Actions))
		for _, action := range rt.Actions {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method:           rt.Method,
			Path:             rt.Path,
			Actions:          actions,
			NamespaceActions: nil,
			Public:           false,
			IdempotencyKey:   rt.IdempotencyKey,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}

// userAgent returns the User-Agent header of a request, for the audit record.
func userAgent(r *http.Request) string {
	return apidesc.Header(r, "User-Agent")
}
