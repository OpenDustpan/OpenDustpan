//go:build e2e

package e2e

import (
	"log/slog"
	"testing"

	contextapi "opendustpan.org/opendustpan/internal/api/objectcontext"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// coverageContextRoutes returns the context adapter's route table.
func coverageContextRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	writer, err := objectcontext.NewWriter(objectcontext.WriterConfig{
		Store: coverageContextStore{}, Namespaces: coverageNamespaceStore{}, Now: nil,
	})
	if err != nil {
		tb.Fatalf("context writer: %v", err)
	}

	handler, err := contextapi.NewHandler(contextapi.Config{
		Namespaces:    namespaces,
		Units:         coverageReader{},
		Deleted:       coverageReader{},
		Collections:   nil,
		Drift:         nil,
		Entries:       coverageContextStore{},
		Writer:        writer,
		Authenticator: coverageAuthenticator{},
		Authorizer:    coverageAuthorizer{},
		Sessions:      nil,
		Logger:        slog.New(slog.DiscardHandler),
		Audit:         coverageAuditStore{}, OnAuditFailure: nil, DenialLimiter: nil,
	})
	if err != nil {
		tb.Fatalf("context handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}
