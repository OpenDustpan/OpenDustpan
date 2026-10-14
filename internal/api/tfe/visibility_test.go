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

// TestUnknownAndHiddenIDsAnswerTheSame proves that an unknown workspace or
// state version ID, and an existing one that the caller cannot read, give
// the same 404 document: the answer does not tell what exists. No sentinel
// unit takes part, so a namespace or a workspace that someone names
// "unknown" changes nothing. A caller that can read the workspace but not
// lock it answers 403.
func TestUnknownAndHiddenIDsAnswerTheSame(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		method  string
		unknown string
		hidden  string
		body    string
	}{
		{
			name: "workspace by ID", method: http.MethodGet,
			unknown: "/api/v2/workspaces/ws-missing", hidden: "/api/v2/workspaces/ws-test123", body: "",
		},
		{
			name: "lock", method: http.MethodPost,
			unknown: "/api/v2/workspaces/ws-missing/actions/lock",
			hidden:  "/api/v2/workspaces/ws-test123/actions/lock", body: `{"data":{}}`,
		},
		{
			name: "state version", method: http.MethodGet,
			unknown: "/api/v2/state-versions/sv-missing", hidden: "/api/v2/state-versions/sv-test123", body: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := newEnforcingHandler(t, stubAuthorizer{})

			unknown := httptest.NewRecorder()
			handler.ServeHTTP(unknown, workspaceRequest(t, tc.method, tc.unknown, tc.body))

			hidden := httptest.NewRecorder()
			handler.ServeHTTP(hidden, workspaceRequest(t, tc.method, tc.hidden, tc.body))

			checkJSONAPIError(t, unknown, http.StatusNotFound)
			checkJSONAPIError(t, hidden, http.StatusNotFound)

			if unknown.Body.String() != hidden.Body.String() {
				t.Fatalf("unknown answers %q, hidden answers %q: want the same document",
					unknown.Body.String(), hidden.Body.String())
			}

			allowed := newEnforcingHandler(t, allowAll())

			rec := httptest.NewRecorder()
			allowed.ServeHTTP(rec, workspaceRequest(t, tc.method, tc.unknown, tc.body))

			checkJSONAPIError(t, rec, http.StatusNotFound)
		})
	}

	t.Run("a failed read check answers 500, not 404", func(t *testing.T) {
		t.Parallel()

		handler := newEnforcingHandler(t, failingReadAuthorizer{})

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, workspaceRequest(t, http.MethodPost, "/api/v2/workspaces/ws-test123/actions/lock",
			`{"data":{}}`))

		checkJSONAPIError(t, rec, http.StatusInternalServerError)
	})

	t.Run("read but not lock", func(t *testing.T) {
		t.Parallel()

		handler := newEnforcingHandler(t, readOnlyAuthorizer{})

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, workspaceRequest(t, http.MethodPost, "/api/v2/workspaces/ws-test123/actions/lock",
			`{"data":{}}`))

		checkJSONAPIError(t, rec, http.StatusForbidden)
	})
}
