package tfe_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// errAppendBoom is the error of a store whose audit append fails.
var errAppendBoom = errors.New("append audit entry: boom")

// recordingDeleter answers deletes as stubWorkspaces does and records the
// audit entry of each delete. The workspace "broken" fails as a store whose
// audit append fails.
type recordingDeleter struct {
	stubWorkspaces

	entries *[]audit.Entry
}

func (d recordingDeleter) Delete(ctx context.Context, ref workspace.Ref, e audit.Entry) error {
	*d.entries = append(*d.entries, e)

	if ref.Name.String() == "broken" {
		return errAppendBoom
	}

	return d.stubWorkspaces.Delete(ctx, ref, e)
}

// TestDeleteWorkspace proves the workspace delete route (ADR 47): a delete
// answers 204, a locked workspace or one with a pending state version 409, an
// unknown one 404, and a store whose
// audit append fails 500. Each delete hands one timed change entry to the
// store, which appends it in the delete transaction (ADR 60, fail closed), and
// the handler appends no entry of its own.
func TestDeleteWorkspace(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		workspace string
		wantCode  int
	}{
		{name: "delete", workspace: "web", wantCode: http.StatusNoContent},
		{name: "locked", workspace: "locked", wantCode: http.StatusConflict},
		{name: "pending state version", workspace: "pending", wantCode: http.StatusConflict},
		{name: "unknown", workspace: "ghost", wantCode: http.StatusNotFound},
		{name: "failed append", workspace: "broken", wantCode: http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			appended := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}

			var passed []audit.Entry

			handler, err := tfe.NewHandler(tfe.Config{
				Authenticator:  tokenAuthenticator{},
				Authorizer:     allowAll(),
				Logger:         slog.New(slog.DiscardHandler),
				Namespaces:     newTestNamespaces(t),
				Workspaces:     recordingDeleter{stubWorkspaces: stubWorkspaces{}, entries: &passed},
				BaseURL:        baseurl.Resolver{},
				Audit:          appended,
				OnAuditFailure: nil,
				Gate:           nil,
				MaxStateBytes:  0,
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

			if rec.Code != tc.wantCode {
				t.Fatalf("delete answers %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
			}

			if len(appended.entries) != 0 {
				t.Fatalf("entries appended by the handler = %d, want 0: the store appends", len(appended.entries))
			}

			checkPassedChangeEntry(t, passed, "opentofu:acme/"+tc.workspace)
		})
	}
}

// checkPassedChangeEntry checks that the store got exactly one entry: a
// change by unit:manage on object.
func checkPassedChangeEntry(t *testing.T, entries []audit.Entry, object string) {
	t.Helper()

	if len(entries) != 1 {
		t.Fatalf("entries passed to the store = %d, want 1", len(entries))
	}

	got := entries[0]
	if got.Kind != audit.KindChange || got.Action != string(identity.ActionUnitManage) ||
		got.Object != object || got.Namespace != "acme" || got.Outcome != audit.OutcomeSuccess {
		t.Fatalf("audit entry = %+v, want a change by unit:manage on %s", got, object)
	}
}
