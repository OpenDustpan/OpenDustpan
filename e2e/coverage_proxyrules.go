//go:build e2e

package e2e

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/jobproxysvc"
	"opendustpan.org/opendustpan/pkg/jobproxy/rulestore"
)

// coverageProxyRules returns the proxy rule service of the route table. The
// stores are stubs: the coverage gate reads the route table only and never
// serves a request.
func coverageProxyRules(tb testing.TB) *jobproxysvc.Service {
	tb.Helper()

	svc, err := jobproxysvc.New(jobproxysvc.Config{
		Store:      struct{ rulestore.RuleStore }{nil},
		Namespaces: coverageNamespaces(tb),
		Units:      struct{ jobproxysvc.Units }{nil},
		Now:        nil,
	})
	if err != nil {
		tb.Fatalf("proxy rule service: %v", err)
	}

	return svc
}

// proxyRulesAllowEntries covers the routes of the proxy rule sets of a
// namespace and a unit. No CLI calls them: the console and the clients of the
// API do. The steps of TestProxyRules call each of them with the token of an
// administrator.
func proxyRulesAllowEntries() []AllowEntry {
	const (
		namespaceRules = "/api/dustpan/v1/namespaces/{namespace}/proxy-rules"
		unitRules      = "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/proxy-rules"
		test           = "TestProxyRules"
		why            = "Console and client route: no CLI calls it. The steps of TestProxyRules call it through the API."
	)

	return []AllowEntry{
		{Method: http.MethodGet, Path: namespaceRules, CoveringTest: test, Reason: why},
		{Method: http.MethodPut, Path: namespaceRules, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: unitRules, CoveringTest: test, Reason: why},
		{Method: http.MethodPut, Path: unitRules, CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: unitRules + "/rendered", CoveringTest: test, Reason: why},
	}
}
