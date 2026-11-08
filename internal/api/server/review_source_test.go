package server_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// plantUnknownMembershipSource writes a source that no port writes, as a row of
// another version or a hand edit would. It uses a second connection to the
// database file of the fixture.
func plantUnknownMembershipSource(t *testing.T, f accessFixture, group, principalID string) {
	t.Helper()

	conn, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close database: %v", closeErr)
		}
	})

	const query = "UPDATE group_members SET source = 'ldap-forged' WHERE group_id = ? AND principal_id = ?"
	if _, execErr := conn.ExecContext(t.Context(), query, group, principalID); execErr != nil {
		t.Fatalf("plant unknown source: %v", execErr)
	}
}

// TestReviewStoredUnknownSourceAnswersInternalError checks that an export that
// reads a stored membership with an unknown source answers 500 with the server
// code. The answer has no page and does not echo the stored value.
func TestReviewStoredUnknownSourceAnswersInternalError(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	seedOpsGrants(t, f)
	plantUnknownMembershipSource(t, f, "grp-ops", "prn-bob")

	for _, path := range []string{
		"/api/server/access-review?expand=members",
		"/api/server/access-review?subject=principal:prn-bob",
	} {
		var raw map[string]any
		f.as(t, "ann", apiCall{http.MethodGet, path, "", http.StatusInternalServerError}, &raw)

		if _, hasPage := raw["grants"]; hasPage {
			t.Fatalf("%s answers a page with the unknown source, want no page: %v", path, raw)
		}

		body := fmt.Sprint(raw)
		if !strings.Contains(body, errcode.ServerInternal) || strings.Contains(body, "ldap-forged") {
			t.Fatalf("%s answers %v, want the server code and no stored value", path, raw)
		}
	}
}

// seedBetaGroupGrant gives group grp-beta a member with a scim source and a
// grant on namespace beta. Namespace beta exists first.
func seedBetaGroupGrant(t *testing.T, f accessFixture, at time.Time) {
	t.Helper()

	nss, err := namespace.NewService(namespace.Config{Store: f.store, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	if _, createErr := nss.Create(t.Context(), "beta"); createErr != nil {
		t.Fatalf("seed namespace beta: %v", createErr)
	}

	group := principal.Group{
		CreatedAt: at, UpdatedAt: at, ID: "grp-beta", Name: "beta-ops", Description: "",
		Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
	}
	groupChange := storetest.ServerChange(at, "group:grp-beta")
	if groupErr := f.store.CreateGroup(t.Context(), group, groupChange); groupErr != nil {
		t.Fatalf("seed group: %v", groupErr)
	}

	member := principal.Membership{
		AddedAt: at, SuspendedAt: time.Time{}, GroupID: "grp-beta", PrincipalID: "prn-olga",
		Source: principal.MembershipSourceSCIM, SourceID: "scim-token-beta",
		State: principal.MembershipStateActive, Pinned: false,
	}
	joined := storetest.ServerChange(at, "group:grp-beta#member:prn-olga")
	if memberErr := f.store.AddMember(t.Context(), member, joined, nil); memberErr != nil {
		t.Fatalf("seed member: %v", memberErr)
	}

	beta := identity.NamespaceObject("beta")
	grant := access.Grant{
		CreatedAt: at, Condition: nil, Object: beta, ID: "grt-beta-ops", Role: access.RoleViewer,
		CreatedBy: "admin", Subject: access.Subject{Kind: access.SubjectGroup, ID: "grp-beta"},
	}

	fence, e := storetest.GrantWrite(t, f.store, beta, at, "grant:grt-beta-ops")
	if grantErr := f.store.CreateGrant(t.Context(), grant, fence, e, nil); grantErr != nil {
		t.Fatalf("seed grant: %v", grantErr)
	}
}

// TestReviewNamespaceExportHidesOtherNamespaceMemberships checks that the
// export of namespace acme shows neither a grant on namespace beta nor the
// membership source of that grant, while the installation export shows both.
func TestReviewNamespaceExportHidesOtherNamespaceMemberships(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	seedBetaGroupGrant(t, f, time.Date(2026, time.October, 13, 13, 0, 0, 0, time.UTC))

	// The installation export shows the grant with its source, so the seed is read.
	installation := reviewRowsAsMap(t, f, "ann", "/api/server/access-review?expand=members")

	found := false

	for _, row := range installation {
		if row["grantID"] == "grt-beta-ops" {
			via, ok := row["via"].(map[string]any)
			found = ok && via["memberSource"] == "scim" && via["memberSourceId"] == "scim-token-beta"
		}
	}

	if !found {
		t.Fatalf("installation export = %v, want the beta grant with its scim source", installation)
	}

	// The export of acme shows neither the grant nor the source of the beta membership.
	acme := reviewRowsAsMap(t, f, "alice", "/api/server/namespaces/acme/access-review?expand=members")
	for _, row := range acme {
		if body := fmt.Sprint(row); strings.Contains(body, "grt-beta-ops") ||
			strings.Contains(body, "scim-token-beta") {
			t.Fatalf("export of acme shows a beta membership: %v", row)
		}
	}
}
