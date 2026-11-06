//go:build e2e

package e2e

import (
	"log/slog"
	"net/http"
	"testing"

	issuerapi "opendustpan.org/opendustpan/internal/api/issuer"
	"opendustpan.org/opendustpan/internal/issuersvc"
	"opendustpan.org/opendustpan/pkg/profile"
)

// coverageIssuerRoutes returns the routes of the issuer. The stores are empty
// shells: they exist only to build a Handler for its route table.
func coverageIssuerRoutes(tb testing.TB) []RouteRef {
	tb.Helper()

	svc, err := issuersvc.NewService(issuersvc.Config{
		Store:         struct{ profile.IssuerStore }{nil},
		Namespaces:    coverageNamespaceStore{},
		Sealer:        struct{ profile.Sealer }{nil},
		Authorizer:    coverageAuthorizer{},
		Notifier:      nil,
		Fetcher:       nil,
		IssuerBaseURL: "",
		PublicURL:     "",
		Now:           nil,
		Log:           nil,
	})
	if err != nil {
		tb.Fatalf("issuer service: %v", err)
	}

	handler, err := issuerapi.NewHandler(issuerapi.Config{
		Issuer:         svc,
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Audit:          coverageAuditStore{},
		Namespaces:     nil,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		Logger:         slog.New(slog.DiscardHandler),
	})
	if err != nil {
		tb.Fatalf("issuer handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// issuerAllowEntries covers the routes of the OIDC issuer. No CLI calls them:
// a relying party reads the two protocol routes, and the console and the
// client of the API call the management routes. The steps of TestIssuerAPI
// make the keys, read the documents with no credential, rotate, check the
// copy, change the settings and export, against the real server.
func issuerAllowEntries() []AllowEntry {
	const (
		base   = "/api/dustpan/v1/namespaces/{namespace}/issuer"
		reason = "Client route: no CLI calls the issuer. The steps of TestIssuerAPI call it against the real server."
	)

	routes := []struct{ method, path string }{
		{http.MethodGet, base},
		{http.MethodPatch, base},
		{http.MethodGet, base + "/export"},
		{http.MethodPost, base + "/keys"},
		{http.MethodPost, base + "/keys/rotate"},
		{http.MethodPost, base + "/keys/check-copy"},
		{http.MethodGet, "/oidc/{nsid}/.well-known/openid-configuration"},
		{http.MethodGet, "/oidc/{nsid}/jwks"},
	}

	entries := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		entries = append(entries, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: "TestIssuerAPI", Reason: reason,
		})
	}

	return entries
}
