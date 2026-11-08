package tfe_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/api/tofuunit"
	"opendustpan.org/opendustpan/internal/baseurl"
)

// newPlacedHandler returns a production Handler over stubWorkspaces whose
// Placer is placer.
func newPlacedHandler(t *testing.T, placer *tofuunit.Placer) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     stubWorkspaces{},
		OutputChecker:  nil,
		BaseURL:        baseurl.Resolver{},
		Audit:          newTestAudit(),
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         placer,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// TestWriteToAWorkspaceInNoCollectionIsRefused proves that a lock and a state
// version create on an existing workspace that no collection holds answer 409
// with the code unit.no-collection and name the move command. A Placer that
// cannot check the workspace refuses the same writes with 501.
func TestWriteToAWorkspaceInNoCollectionIsRefused(t *testing.T) {
	t.Parallel()

	noCollection := &tofuunit.Placer{
		Rules: fixedRules{none: false}, Collections: fixedCollections{missing: true},
	}

	requests := []struct {
		name   string
		target string
		body   string
	}{
		{"lock", "/api/v2/workspaces/ws-test123/actions/lock", `{"data":{}}`},
		{
			"state version create", "/api/v2/workspaces/ws-test123/state-versions",
			stateCreateBody(4, stateCreateOutputs()),
		},
	}

	cases := []struct {
		name       string
		placer     *tofuunit.Placer
		wantStatus int
		wantText   string
	}{
		{"unit in no collection", noCollection, http.StatusConflict, "unit.no-collection"},
		{"no placer", nil, http.StatusNotImplemented, "placement.unavailable"},
	}

	for _, tc := range cases {
		for _, rq := range requests {
			t.Run(tc.name+"/"+rq.name, func(t *testing.T) {
				t.Parallel()

				req := httptest.NewRequestWithContext(
					t.Context(), http.MethodPost, rq.target, strings.NewReader(rq.body),
				)
				req.Header.Set("Authorization", "Bearer test-token")

				rec := httptest.NewRecorder()
				newPlacedHandler(t, tc.placer).ServeHTTP(rec, req)

				if rec.Code != tc.wantStatus || !strings.Contains(rec.Body.String(), tc.wantText) {
					t.Fatalf(
						"%s = %d %s, want %d with %q",
						rq.name,
						rec.Code,
						rec.Body.String(),
						tc.wantStatus,
						tc.wantText,
					)
				}

				if tc.placer != nil && !strings.Contains(rec.Body.String(), "unit-collection") {
					t.Fatalf("%s answer %s does not name the move command", rq.name, rec.Body.String())
				}
			})
		}
	}
}
