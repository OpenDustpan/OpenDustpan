//go:build e2e

package e2e

import (
	"log/slog"
	"net/http"
	"testing"

	notificationsapi "opendustpan.org/opendustpan/internal/api/notifications"
	"opendustpan.org/opendustpan/internal/authgate/authgatetest"
	"opendustpan.org/opendustpan/internal/subscriptionsvc"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
)

// subscriptionPort names notify.Subscriptions for an embedded field: the port has a method of the same name as
// its type, which an embedded field cannot have.
type subscriptionPort = notify.Subscriptions

// coverageSinkRoutes returns the routes of the notification sink API. The
// stores are empty shells: they exist only to build a Handler for its route
// table.
func coverageSinkRoutes(tb testing.TB) []RouteRef {
	tb.Helper()

	svc, err := sinks.NewService(sinks.Config{
		Store:      struct{ notify.SinkStore }{nil},
		Sealer:     notify.SinkSealer{Namespaces: struct{ notify.ScopeSealer }{nil}, Installation: nil},
		Namespaces: coverageNamespaces(tb),
		Senders:    nil,
		Ports:      egress.Ports{},
		Counter:    authgatetest.NewMemory(nil),
		Now:        nil,
	})
	if err != nil {
		tb.Fatalf("sink service: %v", err)
	}

	subscriptions, err := subscriptionsvc.NewService(subscriptionsvc.Config{
		Subscriptions: struct{ subscriptionPort }{nil},
		Deliveries:    struct{ notify.Deliveries }{nil},
		Namespaces:    coverageNamespaces(tb),
		Now:           nil,
	})
	if err != nil {
		tb.Fatalf("subscription service: %v", err)
	}

	handler, err := notificationsapi.NewHandler(notificationsapi.Config{
		Sinks:          svc,
		Subscriptions:  subscriptions,
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Sessions:       nil,
		Idempotency:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Audit:          coverageAuditStore{},
		Namespaces:     nil,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Now:            nil,
	})
	if err != nil {
		tb.Fatalf("sink handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// sinkAllowEntries covers the routes of the notification sinks, subscriptions and deliveries. No CLI calls
// them: the console and the clients of the API do. The steps of
// TestNotificationSinksAPI call each of them with the token of an administrator.
func sinkAllowEntries() []AllowEntry {
	const (
		base = "/api/dustpan/v1/namespaces/{namespace}/sinks"
		subs = "/api/dustpan/v1/namespaces/{namespace}/subscriptions"
		test = "TestNotificationSinksAPI"
		why  = "Console and client route: no CLI calls it. The steps of TestNotificationSinksAPI call it through the API."
	)

	return []AllowEntry{
		{Method: http.MethodPost, Path: base, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: base, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: base + "/{sink_id}", CoveringTest: test, Reason: why},
		{Method: http.MethodPut, Path: base + "/{sink_id}", CoveringTest: test, Reason: why},
		{Method: http.MethodDelete, Path: base + "/{sink_id}", CoveringTest: test, Reason: why},
		{Method: http.MethodPost, Path: base + "/{sink_id}/test", CoveringTest: test, Reason: why},
		{Method: http.MethodPost, Path: subs, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: subs, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: subs + "/{subscription_id}", CoveringTest: test, Reason: why},
		{Method: http.MethodPut, Path: subs + "/{subscription_id}", CoveringTest: test, Reason: why},
		{Method: http.MethodDelete, Path: subs + "/{subscription_id}", CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: subs + "/{subscription_id}/deliveries", CoveringTest: test, Reason: why},
		{
			Method: http.MethodPost, Path: "/api/dustpan/v1/namespaces/{namespace}/deliveries/{delivery_id}/redeliver",
			CoveringTest: test, Reason: why,
		},
	}
}
