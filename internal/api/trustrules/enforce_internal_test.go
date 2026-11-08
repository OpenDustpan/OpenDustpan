package trustrules

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// enforceTokens signs in every token as one principal with no grants of its own.
type enforceTokens struct{}

func (enforceTokens) AuthenticateToken(_ context.Context, _ string) (identity.Principal, error) {
	var p identity.Principal

	return p, nil
}

// enforceAllowAll allows every action on every object, so only the route row
// decides whether the request serves.
type enforceAllowAll struct {
	identity.Authorizer
}

func (enforceAllowAll) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return nil
}

// enforceAudit accepts each audit entry and keeps none.
type enforceAudit struct {
	audit.Store
}

func (enforceAudit) Append(_ context.Context, _ namespace.Fence, e audit.Entry) (audit.Entry, error) {
	return e, nil
}

// TestEnforceActions sends a request to one route row that the test builds
// itself. A row with no action never serves, even when the authorizer allows
// every action: the wrapper answers an error instead. Defense in depth: the
// route test of the API refuses such a row too.
func TestEnforceActions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		actions    []identity.Action
		wantServed bool
	}{
		{
			name:       "row with no action is refused",
			actions:    nil,
			wantServed: false,
		},
		{
			name:       "row with an action serves",
			actions:    []identity.Action{identity.ActionNamespaceReadAccess},
			wantServed: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger := slog.New(slog.DiscardHandler)

			refusals, err := refusal.New(enforceAllowAll{Authorizer: nil}, enforceAudit{Store: nil}, nil, logger, nil)
			if err != nil {
				t.Fatalf("refusal recorder: %v", err)
			}

			served := false
			h := &Handler{
				namespaces:     nil,
				rules:          nil,
				exchanger:      nil,
				authn:          enforceTokens{},
				sessions:       nil,
				guard:          refusals,
				audit:          enforceAudit{Store: nil},
				onAuditFailure: nil,
				logger:         logger,
				now:            nil,
				mux:            nil,
				routes:         nil,
			}
			rt := Route{
				Method:  http.MethodGet,
				Path:    RulesPath,
				Scope:   route.ScopeAuthorized,
				Actions: tc.actions,
				Resolve: resolveNamespace,
				Serve: func(http.ResponseWriter, *http.Request, identity.Principal, identity.Object) {
					served = true
				},
			}

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)
			r.Header.Set("Authorization", "token good")
			r.SetPathValue("namespace", "acme")
			resp := httptest.NewRecorder()

			h.enforce(rt).ServeHTTP(resp, r)

			if served != tc.wantServed {
				t.Fatalf("served = %v, want %v (status %d)", served, tc.wantServed, resp.Code)
			}

			if !tc.wantServed && resp.Code < http.StatusBadRequest {
				t.Fatalf("status = %d, want a refusal", resp.Code)
			}
		})
	}
}
