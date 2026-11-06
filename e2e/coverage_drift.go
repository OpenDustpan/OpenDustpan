//go:build e2e

package e2e

import (
	"log/slog"
	"net/http"
	"testing"

	driftapi "opendustpan.org/opendustpan/internal/api/drift"
	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// coverageDriftRoutes returns the drift adapter's route table.
func coverageDriftRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	handler, err := driftapi.NewHandler(driftapi.Config{
		Namespaces:     namespaces,
		Workers:        nil,
		Units:          coverageReader{},
		Results:        struct{ drift.Store }{nil},
		Checker:        struct{ drift.Checker }{nil},
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Audit:          coverageAuditStore{},
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
	})
	if err != nil {
		tb.Fatalf("drift handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// driftAllowEntries covers the routes that start a drift check and read the
// latest drift result of a unit. No CLI calls them: the console and the clients
// of the API do. The harness steps of TestDriftAPI call each of them through
// the API: a read before any check, a start, the same start again, the refusals
// of a start, and the reads of a person who may not read the unit.
func driftAllowEntries() []AllowEntry {
	const (
		test = "TestDriftAPI"
		why  = "Console and client route: no CLI calls it. The harness steps of TestDriftAPI call it through the API."
	)

	return []AllowEntry{
		{
			Method: http.MethodPost, Path: "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift-checks",
			CoveringTest: test, Reason: why,
		},
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift",
			CoveringTest: test, Reason: why,
		},
	}
}
