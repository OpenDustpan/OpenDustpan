package accesstest

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// testCallerGrants reads the grants of alice (prn-a), a member of ops from
// minute 2 to 6 and again from minute 8:
//
//	grt-1  viewer   prn-a    acme  minute 1
//	grt-2  writer   grp-ops  acme  minute 3
//	grt-3  viewer   prn-b    acme  minute 4
//	grt-4  auditor  prn-a    beta  minute 5, revoked at minute 9
//	grt-5  auditor  grp-ops  beta  minute 10
func testCallerGrants(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)

	ps, ok := s.(principal.Store)
	if !ok {
		t.Skip("the store keeps no principals")
	}

	seedMemberships(t, ps)

	acme, beta := identity.NamespaceObject("acme"), identity.NamespaceObject("beta")
	mustCreateGrant(t, s, grantAt(1, "grt-1", principalSubject("prn-a"), access.RoleViewer, acme))
	mustCreateGrant(t, s, grantAt(3, "grt-2", opsGroup(), access.RoleWriter, acme))
	mustCreateGrant(t, s, grantAt(4, "grt-3", principalSubject("prn-b"), access.RoleViewer, acme))
	mustCreateGrant(t, s, grantAt(5, "grt-4", principalSubject("prn-a"), access.RoleAuditor, beta))
	mustCreateGrant(t, s, grantAt(10, "grt-5", opsGroup(), access.RoleAuditor, beta))

	if err := revokeGrant(t, s, "grt-4", "beta", entryAt(at(9))); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	q := callerQuery(9)
	checkCallerGrantRows(t, callerGrants(t, rs, "alice at minute 9", q, "grt-1", "grt-2"))

	// Out of ops at minute 7, so its grant is not there. The revoke of
	// grt-4 is current, so a snapshot before it leaves grt-4 out too.
	callerGrants(t, rs, "alice at minute 7", callerQuery(7), "grt-1")

	q.Groups = nil
	callerGrants(t, rs, "alice with no group", q, "grt-1")

	checkCallerGrantPages(t, rs)
}

// checkCallerGrantRows checks the own row and the group row of alice at
// minute 9.
func checkCallerGrantRows(t *testing.T, rows []access.ReviewRow) {
	t.Helper()

	if rows[0].Via != nil || rows[0].SubjectName != "alice" || rows[0].MemberCount != nil {
		t.Fatalf("own row = %+v, want alice with no via and no member count", rows[0])
	}

	if v := rows[1].Via; v == nil || !v.AddedAt.Equal(at(8)) || v.RemovedAt != nil || v.PrincipalID != "prn-a" ||
		v.PrincipalSubject != "alice" || rows[1].SubjectName != "ops" ||
		rows[1].MemberCount == nil || *rows[1].MemberCount != 1 {
		t.Fatalf("group row = %+v, want ops through the membership from minute 8, with 1 member", rows[1])
	}
}

// checkCallerGrantPages reads the grants of alice at minute 9 one row a
// page, and with no count.
func checkCallerGrantPages(t *testing.T, rs access.ReviewStore) {
	t.Helper()

	q := callerQuery(9)
	q.Limit = 1

	first, total, err := rs.CallerGrants(t.Context(), q)
	if err != nil || len(first) != 1 || first[0].Grant.ID != "grt-1" || total != 2 {
		t.Fatalf("first page = %+v, %d, %v; want grt-1 with a total of 2", first, total, err)
	}

	q.After, q.Count = first[0].Key(), false
	callerGrants(t, rs, "second page", q, "grt-2")

	q.After = access.ReviewKey{ID: "grt-2", MemberID: "prn-a", AtMs: at(3).UnixMilli(), MemberAtMs: at(8).UnixMilli()}
	callerGrants(t, rs, "after the last row", q)
}

// callerQuery returns the query of the grants of alice and ops in a
// snapshot at minute, with its count.
func callerQuery(minute int) access.CallerGrantsQuery {
	return access.CallerGrantsQuery{
		Groups: []string{"grp-ops"}, Principal: "prn-a",
		After:  access.ReviewKey{ID: "", MemberID: "", AtMs: 0, MemberAtMs: 0},
		AsOfMs: at(minute).UnixMilli(), Limit: access.MaxLimit, Count: true,
	}
}

// callerGrants reads q and checks the grant IDs of its rows and its total:
// the count of the rows when q asks for it, else 0.
func callerGrants(
	t *testing.T, rs access.ReviewStore, label string, q access.CallerGrantsQuery, want ...string,
) []access.ReviewRow {
	t.Helper()

	total := int64(0)
	if q.Count {
		total = int64(len(want))
	}

	rows, got, err := rs.CallerGrants(t.Context(), q)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}

	ids := make([]string, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].Grant.ID)
	}

	if !slices.Equal(ids, want) || got != total {
		t.Fatalf("%s = %v with total %d, want %v with total %d", label, ids, got, want, total)
	}

	return rows
}
