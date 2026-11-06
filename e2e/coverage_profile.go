//go:build e2e

package e2e

import (
	"log/slog"
	"testing"

	profileapi "opendustpan.org/opendustpan/internal/api/profile"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/profile"
)

// coverageProfileRoutes returns the routes of the profile API. The stores
// are empty shells: they exist only to build a Handler for its route table.
func coverageProfileRoutes(tb testing.TB) []RouteRef {
	tb.Helper()

	svc, err := profile.NewService(profile.Config{
		Store:      struct{ profile.Store }{nil},
		Namespaces: coverageNamespaceStore{},
		Sealer:     struct{ profile.Sealer }{nil},
		Audit:      struct{ audit.Store }{nil},
		Authorizer: coverageAuthorizer{},
		Now:        nil,
	})
	if err != nil {
		tb.Fatalf("profile service: %v", err)
	}

	handler, err := profileapi.NewHandler(profileapi.Config{
		Profiles:      svc,
		Authenticator: coverageAuthenticator{},
		Authorizer:    coverageAuthorizer{},
		Sessions:      nil,
		SessionPolicy: nil,
		Idempotency:   nil,
		Logger:        slog.New(slog.DiscardHandler),
		Now:           nil,
		Audit:         coverageAuditStore{}, Namespaces: nil, OnAuditFailure: nil, DenialLimiter: nil,
	})
	if err != nil {
		tb.Fatalf("profile handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}
