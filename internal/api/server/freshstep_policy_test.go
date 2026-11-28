package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sendToken sends a request with an access token and no console session.
func sendToken(t *testing.T, h testHandler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// wantNoStep fails when rec is the refusal of a missing fresh step. The other
// checks of the route (the reach of the caller, the object) may still refuse
// the request of a token whose person is not the person of the console
// session. Those refusals are not the subject of this table.
func wantNoStep(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), freshErrorCode) {
		t.Errorf("with the setting off the request asked for a step: %d %s", rec.Code, rec.Body.String())
	}

	if rec.Code >= http.StatusInternalServerError {
		t.Errorf("with the setting off the request = %d %s, want no server error", rec.Code, rec.Body.String())
	}
}

// TestFreshStepPolicyGatesEachHandlerSite proves for each handler site of the
// installation API that asks for a fresh step (the token routes, the token
// policy, the session policy, the providers, the gate configuration, the
// access routes, the IP allowlists, the audit chain, and the Git host writes):
// with the setting freshAuthEnabled off, an access token with no console
// session passes with no step, and with the setting on, the same request gets
// 403 mfa.fresh-step-required. The off rows fail when a site stops asking the
// policy, because that site then answers 403. The on rows fail when a site
// stops asking for the step.
func TestFreshStepPolicyGatesEachHandlerSite(t *testing.T) {
	t.Parallel()

	for _, route := range freshRoutes() {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()

			e := newProviderEnv(t)

			body, path := route.body, route.path
			if route.name == "create provider" {
				body = e.betaBody()
			}

			if route.setup != nil {
				if setPath := route.setup(t, e); setPath != "" {
					path = setPath
				}
			}

			e.freshOn.Store(true)
			checkTokenRefused(t, e.handler, route.method, path, body)

			e.freshOn.Store(false)
			wantNoStep(t, sendToken(t, e.handler, route.method, path, body))
		})
	}
}

// TestFreshStepPolicyGatesGitHostSites is the table of the Git host writes of
// TestFreshStepPolicyGatesEachHandlerSite.
func TestFreshStepPolicyGatesGitHostSites(t *testing.T) {
	t.Parallel()

	for _, route := range hostFreshRoutes() {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()

			e := newHostEnv(t, hostAllowAll())

			if route.setup != nil {
				route.setup(t, e)
			}

			e.freshOn.Store(true)
			checkTokenRefused(t, e.handler, route.method, route.target, route.body)

			e.freshOn.Store(false)
			wantNoStep(t, sendToken(t, e.handler, route.method, route.target, route.body))
		})
	}
}
