//go:build e2e

package e2e

import (
	"log/slog"
	"net/http"
	"testing"

	sourcefilterapi "opendustpan.org/opendustpan/internal/api/sourcefilter"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/source"
)

// coverageSourceFilterRoutes returns the source filter adapter's route table.
func coverageSourceFilterRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	handler, err := sourcefilterapi.NewHandler(sourcefilterapi.Config{
		Namespaces:     namespaces,
		Filters:        struct{ source.Filters }{nil},
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Audit:          coverageAuditStore{},
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
		Now:            nil,
	})
	if err != nil {
		tb.Fatalf("source filter handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// sourceFilterAllowEntries covers the routes of the source filter of a
// namespace. No CLI calls them: the console and the clients of the API do. The
// steps of TestSourceFilterAPI call both with the token of an administrator.
func sourceFilterAllowEntries() []AllowEntry {
	const (
		path = "/api/dustpan/v1/namespaces/{namespace}/source-filter"
		test = "TestSourceFilterAPI"
		why  = "Console and client route: no CLI calls it. The steps of TestSourceFilterAPI call it through the API."
	)

	return []AllowEntry{
		{Method: http.MethodGet, Path: path, CoveringTest: test, Reason: why},
		{Method: http.MethodPut, Path: path, CoveringTest: test, Reason: why},
	}
}
