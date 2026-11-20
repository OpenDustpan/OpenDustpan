//go:build e2e

package e2e

import (
	"bytes"
	"log/slog"
	"testing"

	stateapi "opendustpan.org/opendustpan/internal/api/state"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// coverageStateRoutes returns the route table of the masked state read of a
// unit. The handler gets no store: the table needs no read.
func coverageStateRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	handler, err := stateapi.NewHandler(stateapi.Config{
		Namespaces:     namespaces,
		Stacks:         struct{ stateapi.Stacks }{nil},
		StackStates:    struct{ stateapi.StackStates }{nil},
		Workspaces:     struct{ stateapi.Workspaces }{nil},
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Audit:          coverageAuditStore{},
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		CursorKey:      bytes.Repeat([]byte{1}, stateapi.MinCursorKeyBytes),
		Logger:         slog.New(slog.DiscardHandler),
		Timeout:        0,
	})
	if err != nil {
		tb.Fatalf("state handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}
