//go:build e2e

package e2e

import (
	"log/slog"
	"net/http"
	"testing"

	trustrulesapi "opendustpan.org/opendustpan/internal/api/trustrules"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// coverageTrustRuleRoutes returns the trust rule adapter's route table.
func coverageTrustRuleRoutes(tb testing.TB, namespaces *namespace.Service) []RouteRef {
	tb.Helper()

	rules, err := trustrulesvc.NewService(trustrulesvc.Config{
		Store: struct{ trustrule.Store }{nil}, Namespaces: namespaces,
		Principals: struct{ trustrulesvc.Principals }{nil}, Authorizer: coverageAuthorizer{},
		Reach:            nil,
		InstallationURLs: nil, OwnIssuer: nil, CheckURL: nil, Now: nil,
	})
	if err != nil {
		tb.Fatalf("trust rule service: %v", err)
	}

	exchanger, err := trustrulesvc.NewExchanger(trustrulesvc.ExchangeConfig{
		Rules: struct{ trustrule.Store }{nil}, Exchanges: struct{ trustrule.ExchangeStore }{nil},
		Namespaces: namespaces, Principals: struct{ trustrulesvc.Principals }{nil},
		Tokens: struct{ trustrulesvc.TokenIssuer }{nil}, Audit: coverageAuditStore{},
		ServerAudit: struct{ audit.ServerStore }{nil}, Refusals: nil, Keys: nil, InstallationURLs: nil,
		OwnIssuer: nil, Now: nil, Log: nil,
	})
	if err != nil {
		tb.Fatalf("token exchange: %v", err)
	}

	handler, err := trustrulesapi.NewHandler(trustrulesapi.Config{
		Exchanger:      exchanger,
		Namespaces:     namespaces,
		Rules:          rules,
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
		tb.Fatalf("trust rule handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// trustRuleAllowEntries covers the routes of the workload trust rules of a
// namespace. No CLI calls them: the console and the clients of the API do.
// The steps of TestTrustRuleAPI call each of them with the token of an
// administrator.
func trustRuleAllowEntries() []AllowEntry {
	const (
		rules = "/api/dustpan/v1/namespaces/{namespace}/trust-rules"
		rule  = rules + "/{rule_id}"
		test  = "TestTrustRuleAPI"
		why   = "Console and client route: no CLI calls it. The steps of TestTrustRuleAPI call it through the API."
	)

	return []AllowEntry{
		{Method: http.MethodGet, Path: rules, CoveringTest: test, Reason: why},
		{Method: http.MethodPost, Path: rules, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: rule, CoveringTest: test, Reason: why},
		{Method: http.MethodDelete, Path: rule, CoveringTest: test, Reason: why},
		{
			Method: http.MethodPost, Path: rules + "/exchange", CoveringTest: "TestWorkloadTokenExchange",
			Reason: "Workload route: a CI job calls it with the token of its platform. The steps of " +
				"TestWorkloadTokenExchange call it with tokens of a fake issuer.",
		},
	}
}
