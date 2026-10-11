package tfe_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// TestListWorkspacesFiltersByUnitRead proves the list keeps only the
// workspaces the caller may unit:read: denying unit:read empties the list
// while allowing it keeps every workspace.
func TestListWorkspacesFiltersByUnitRead(t *testing.T) {
	t.Parallel()

	t.Run("denied", func(t *testing.T) {
		t.Parallel()

		handler := newPagingHandler(
			t,
			selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionUnitRead: true}},
		)

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, workspaceRequest(t, http.MethodGet, "/api/v2/organizations/acme/workspaces", ""))

		doc := decodeWorkspaceList(t, rec, http.StatusOK)

		if len(doc.Data) != 0 {
			t.Fatalf("list holds %v, want no workspaces", listNames(doc))
		}

		if doc.Meta.Pagination.TotalCount != 0 {
			t.Fatalf("total count = %d, want 0", doc.Meta.Pagination.TotalCount)
		}
	})

	t.Run("allowed", func(t *testing.T) {
		t.Parallel()

		handler := newPagingHandler(t, allowAll())

		doc := getListPage(t, handler, "/api/v2/organizations/acme/workspaces")

		if len(doc.Data) != 3 {
			t.Fatalf("list holds %v, want three workspaces", listNames(doc))
		}
	})
}

// TestListWorkspacesPageSizeCapped proves page[size] above 100 reads as 100.
func TestListWorkspacesPageSizeCapped(t *testing.T) {
	t.Parallel()

	handler := newPagingHandler(t, allowAll())

	for _, target := range []string{
		"/api/v2/organizations/acme/workspaces?page[size]=101",
		"/api/v2/organizations/acme/workspaces?page[size]=200",
	} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			doc := getListPage(t, handler, target)

			if doc.Meta.Pagination.PageSize != 100 {
				t.Fatalf("page size = %d, want 100", doc.Meta.Pagination.PageSize)
			}

			if len(doc.Data) != 3 {
				t.Fatalf("page holds %v, want every workspace", listNames(doc))
			}
		})
	}
}

// TestUnknownIDAnswersForbiddenWhenDenied proves an unknown workspace or
// state version ID answers 403 for a caller without rights and 404 for an
// allowed caller, so the absence does not leak.
func TestUnknownIDAnswersForbiddenWhenDenied(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{name: "workspace by ID", method: http.MethodGet, target: "/api/v2/workspaces/ws-missing", body: ""},
		{
			name:   "lock",
			method: http.MethodPost,
			target: "/api/v2/workspaces/ws-missing/actions/lock",
			body:   `{"data":{}}`,
		},
		{
			name:   "state version",
			method: http.MethodGet,
			target: "/api/v2/state-versions/sv-missing",
			body:   "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+" denied", func(t *testing.T) {
			t.Parallel()

			handler := newEnforcingHandler(t, stubAuthorizer{})

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, workspaceRequest(t, tc.method, tc.target, tc.body))

			checkJSONAPIError(t, rec, http.StatusForbidden)
		})

		t.Run(tc.name+" allowed", func(t *testing.T) {
			t.Parallel()

			handler := newEnforcingHandler(t, allowAll())

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, workspaceRequest(t, tc.method, tc.target, tc.body))

			checkJSONAPIError(t, rec, http.StatusNotFound)
		})
	}
}
