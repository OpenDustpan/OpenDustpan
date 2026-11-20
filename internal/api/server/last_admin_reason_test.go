package server_test

import (
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestLastSiteAdministratorRefusalNamesItsReason proves that each principal
// route that could end the last site administrator writes one failure entry
// with the reason access.last-site-administrator.
func TestLastSiteAdministratorRefusalNamesItsReason(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		method string
		path   func(admin string) string
		action string
	}{
		{
			"delete", http.MethodDelete,
			func(admin string) string { return "/api/server/principals/" + admin },
			string(identity.ActionServerManagePrincipals),
		},
		{
			"disable", http.MethodPost,
			func(admin string) string { return "/api/server/principals/" + admin + "/disable" },
			string(identity.ActionServerManagePrincipals),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			principals, st := newTestPrincipalStore(t)
			h := newAuditedPrincipalHandler(t, principals, st)

			var admin principalDoc
			mustCall(t, h, apiCall{
				http.MethodPost, "/api/server/principals", `{"subject":"root","kind":"person"}`, http.StatusCreated,
			}, &admin)

			grant := access.Grant{
				CreatedAt: time.Unix(0, 0), Condition: nil, Object: identity.ServerObject(), ID: "grt-last",
				Role: access.RoleSiteAdministrator, CreatedBy: "test",
				Subject: access.Subject{Kind: access.SubjectPrincipal, ID: admin.ID},
			}

			entry := storetest.ServerChange(time.Unix(0, 0), "@server")
			fence := namespace.Fence{Namespace: "", FailoverVersion: 0}

			if err := st.CreateGrant(t.Context(), grant, fence, entry, nil); err != nil {
				t.Fatalf("create grant: %v", err)
			}

			mustCall(t, h, apiCall{tc.method, tc.path(admin.ID), "", http.StatusConflict}, nil)

			wantOneLastAdministratorEntry(t, st, tc.action, "principal:"+admin.ID)
		})
	}
}

// TestLastSiteAdministratorGrantRefusalNamesItsReason proves that the revoke
// of the last grant of the site administrator role by its ID writes one
// failure entry with the reason access.last-site-administrator.
func TestLastSiteAdministratorGrantRefusalNamesItsReason(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seed(t, "boss", access.RoleSiteAdministrator, identity.ServerObject())

	f.as(t, "admin", apiCall{http.MethodDelete, "/api/server/grants/grt-seed-boss", "", http.StatusConflict}, nil)

	wantOneLastAdministratorEntry(t, f.store, string(identity.ActionServerManageAccess), "grant:grt-seed-boss")
}

// wantOneLastAdministratorEntry fails the test unless the scope @server holds
// exactly one failure entry of action on object, and the entry has the reason
// access.last-site-administrator.
func wantOneLastAdministratorEntry(t *testing.T, st *sqlite.Store, action, object string) {
	t.Helper()

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list server entries: %v", err)
	}

	var failures []audit.Entry

	for i := range listing.Entries {
		e := &listing.Entries[i]
		if e.Outcome == audit.OutcomeFailure && e.Action == action && e.Object == object {
			failures = append(failures, *e)
		}
	}

	if len(failures) != 1 {
		t.Fatalf("failure entries of %s on %s = %+v, want one", action, object, failures)
	}

	if failures[0].Reason != errcode.AccessLastSiteAdministrator {
		t.Fatalf("reason = %q, want %q", failures[0].Reason, errcode.AccessLastSiteAdministrator)
	}
}
