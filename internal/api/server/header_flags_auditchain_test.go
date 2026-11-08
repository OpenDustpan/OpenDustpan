package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc/headertest"
	apiserver "opendustpan.org/opendustpan/internal/api/server"
)

// auditChainScenarios returns the scenarios of the audit chain, of the
// retention sweep and of the namespace override switch writes for the check
// of the header flags. The approval needs a console session with a fresh
// sign-in; the other two writes take an access token.
func auditChainScenarios() map[string]headertest.Scenario {
	const (
		auditChainURL = "/api/dustpan/v1/audit/chain"
		sweepURL      = "/api/dustpan/v1/deleted-units/sweep"
		switchesURL   = "/api/dustpan/v1/namespaces/" + testNamespace + "/override-switches"
	)

	// tokenScenario sends one request with the access token of the tests.
	tokenScenario := func(method, target, body string) headertest.Scenario {
		return func(t *testing.T) headertest.Probe {
			t.Helper()

			_, g := newGateHandler(t, allowAll{})

			return headertest.Probe{
				ETag: "", Replayed: nil,
				Send: func(map[string]string) headertest.Answer {
					rec := serveGate(t, g.env.handler, gateCall{method, target, body, ""})

					return headertest.Answer{Status: rec.Code, Header: rec.Header(), Body: rec.Body.String()}
				},
			}
		}
	}

	return map[string]headertest.Scenario{
		"approveAuditChain": func(t *testing.T) headertest.Probe {
			t.Helper()

			_, g := newGateHandler(t, allowAll{})

			return headertest.Probe{
				ETag: "", Replayed: nil,
				Send: func(map[string]string) headertest.Answer {
					req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, auditChainURL, http.NoBody)
					req.AddCookie(g.session)
					req.Header.Set(apiserver.CSRFHeader, g.csrf)
					req.Header.Set("Sec-Fetch-Site", "same-origin")

					rec := httptest.NewRecorder()
					g.env.handler.ServeHTTP(rec, req)

					return headertest.Answer{Status: rec.Code, Header: rec.Header(), Body: rec.Body.String()}
				},
			}
		},
		"sweepDeletedUnits": tokenScenario(http.MethodPost, sweepURL, ""),
		"setNamespaceOverrideSwitches": tokenScenario(
			http.MethodPut, switchesURL, `{"allowForcedWrites":true,"allowUnlockedWrites":true}`,
		),
	}
}
