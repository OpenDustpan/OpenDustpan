package tfe_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestDeleteWorkspace proves the workspace delete route (ADR 47): a delete
// answers 204 and writes one audit entry of the kind change, a locked
// workspace answers 409 and an unknown one 404, and a refused delete writes
// no entry.
func TestDeleteWorkspace(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		workspace string
		wantCode  int
		wantCount int
	}{
		{name: "delete", workspace: "web", wantCode: http.StatusNoContent, wantCount: 1},
		{name: "locked", workspace: "locked", wantCode: http.StatusConflict, wantCount: 0},
		{name: "unknown", workspace: "ghost", wantCode: http.StatusNotFound, wantCount: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}

			handler, err := tfe.NewHandler(tfe.Config{
				Authenticator:  tokenAuthenticator{},
				Authorizer:     allowAll(),
				Logger:         slog.New(slog.DiscardHandler),
				Namespaces:     newTestNamespaces(t),
				Workspaces:     stubWorkspaces{},
				BaseURL:        baseurl.Resolver{},
				Audit:          entries,
				OnAuditFailure: nil,
			})
			if err != nil {
				t.Fatalf("handler: %v", err)
			}

			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodDelete,
				"/api/v2/organizations/acme/workspaces/"+tc.workspace,
				http.NoBody,
			)
			req.Header.Set("Authorization", "Bearer test-token")

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			checkDeleteAnswer(t, rec, entries, tc.wantCode, tc.wantCount)
		})
	}
}

// checkDeleteAnswer checks the status of one delete answer and the audit
// entries that it wrote: wantCount entries, each a change by unit:manage on
// opentofu:acme/web.
func checkDeleteAnswer(
	t *testing.T, rec *httptest.ResponseRecorder, entries *memAuditStore, wantCode, wantCount int,
) {
	t.Helper()

	if rec.Code != wantCode {
		t.Fatalf("delete answers %d, want %d (body %s)", rec.Code, wantCode, rec.Body.String())
	}

	if len(entries.entries) != wantCount {
		t.Fatalf("audit entries = %d, want %d", len(entries.entries), wantCount)
	}

	for i := range entries.entries {
		got := entries.entries[i]
		if got.Kind != audit.KindChange || got.Action != string(identity.ActionUnitManage) ||
			got.Object != "opentofu:acme/web" || got.Outcome != audit.OutcomeSuccess {
			t.Fatalf("audit entry = %+v, want a change by unit:manage on opentofu:acme/web", got)
		}
	}
}
