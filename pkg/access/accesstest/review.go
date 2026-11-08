package accesstest

import (
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// reviewCases are the cases of access.ReviewStore. A store that does not
// implement it skips them.
func reviewCases() []struct {
	test func(t *testing.T, s access.Store)
	name string
} {
	return []struct {
		test func(t *testing.T, s access.Store)
		name string
	}{
		{name: "review reads each scope", test: testReviewScopes},
		{name: "review filters by subject", test: testReviewSubject},
		{name: "review pages in the order of grant time and ID", test: testReviewPaging},
		{name: "review history reads the grants that overlap the window", test: testReviewHistory},
		{name: "review of a principal reads its groups through each membership", test: testReviewThroughGroups},
		{name: "review of a unit matches its stable ID, not its name", test: testReviewUnit},
		{name: "review snapshot reads the grants and memberships at its time", test: testReviewAsOf},
		{name: "review expand puts each member of a group in its own row", test: testReviewExpand},
		{name: "review reads each membership source", test: testReviewMembershipSources},
		{name: "review snapshots show suspension at the snapshot time", test: testReviewMembershipSuspension},
		{name: "review rejects an unknown stored membership source", test: testReviewUnknownMembershipSource},
		{name: "review history keeps a container grant with any custom role", test: testReviewCustomRoles},
		{name: "review of a principal reads the grants on its token namespace", test: testReviewTokenNamespace},
		{name: "review counts only when asked", test: testReviewCount},
		{name: "review units reads live and deleted units", test: testReviewUnits},
		{name: "review last access reads the latest use of each principal", test: testLastAccess},
		{name: "review last access counts the use of a console session", test: testLastAccessSessions},
		{name: "caller grants read the current grants of a principal and its groups", test: testCallerGrants},
		{name: "review history hides a suspended member", test: testReviewHistoryHidesSuspendedMember},
	}
}

// reviewStore returns s as an access.ReviewStore, or skips t.
//
//nolint:ireturn // The suite tests the port: a store has no other shared type.
func reviewStore(t *testing.T, s access.Store) access.ReviewStore {
	t.Helper()

	rs, ok := s.(access.ReviewStore)
	if !ok {
		t.Skip("the store reads no access review")
	}

	return rs
}

// entryAt returns entry() at the time when.
func entryAt(when time.Time) audit.Entry {
	e := entry()
	e.At = when

	return e
}

// grantAt returns newGrant made at minute.
func grantAt(minute int, id string, subject access.Subject, role string, obj identity.Object) access.Grant {
	g := newGrant(id, subject, role, obj)
	g.CreatedAt = at(minute)

	return g
}

// seedReview stores the grants of the scope cases:
//
//	grt-1  installation-operator  prn-a    @server
//	grt-2  account-administrator  grp-ops  @server
//	grt-3  writer                 grp-ops  acme
//	grt-4  auditor                prn-a    acme/payments
//	grt-5  maintainer             prn-a    pulumi:acme/web
//	grt-6  viewer                 prn-b    beta
func seedReview(t *testing.T, s access.Store) {
	t.Helper()

	collection := identity.CollectionObject("acme", "payments").WithCollectionID("col-payments")

	grants := []access.Grant{
		grantAt(1, "grt-1", principalSubject("prn-a"), access.RoleInstallationOperator, identity.ServerObject()),
		grantAt(2, "grt-2", opsGroup(), access.RoleAccountAdministrator, identity.ServerObject()),
		grantAt(3, "grt-3", opsGroup(), access.RoleWriter, identity.NamespaceObject("acme")),
		grantAt(4, "grt-4", principalSubject("prn-a"), access.RoleAuditor, collection),
		grantAt(5, "grt-5", principalSubject("prn-a"), access.RoleMaintainer, unit(t, "acme", "web")),
		grantAt(6, "grt-6", principalSubject("prn-b"), access.RoleViewer, identity.NamespaceObject("beta")),
	}
	for i := range grants {
		mustCreateGrant(t, s, grants[i])
	}
}

// snapshotMinute is the time of the snapshots of the suite: after every
// seed.
const snapshotMinute = 30

// everyRow returns the query of every row of scope in a snapshot at
// snapshotMinute, with its count.
func everyRow(scope access.ReviewScope) access.ReviewQuery {
	return access.ReviewQuery{
		Scope: scope, Subject: "", Principal: "", After: access.ReviewKey{ID: "", MemberID: "", AtMs: 0, MemberAtMs: 0},
		AsOfMs: at(snapshotMinute).UnixMilli(), FromMs: 0, ToMs: 0, Limit: access.MaxLimit, History: false,
		Expand: false, Count: true,
	}
}

// allScope is the scope of every grant.
func allScope() access.ReviewScope {
	return access.ReviewScope{
		Object: identity.Object{}, Namespace: "", Inside: nil, Containers: nil, ContainerRoles: nil, TokenNamespace: "",
		TokenNamespaceRoles: nil, All: true,
	}
}

// review reads q and checks the grant IDs of its rows and its total.
func review(
	t *testing.T, rs access.ReviewStore, label string, q access.ReviewQuery, want ...string,
) []access.ReviewRow {
	t.Helper()

	rows, total, err := rs.ReviewGrants(t.Context(), q)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}

	got := make([]string, 0, len(rows))
	for i := range rows {
		got = append(got, rows[i].Grant.ID)
	}

	if !slices.Equal(got, want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}

	if total != int64(len(want)) {
		t.Fatalf("%s total = %d, want %d", label, total, len(want))
	}

	return rows
}

func testReviewScopes(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)
	seedReview(t, s)

	review(t, rs, "every grant", everyRow(allScope()), "grt-1", "grt-2", "grt-3", "grt-4", "grt-5", "grt-6")

	review(t, rs, "namespace acme", everyRow(access.ReviewScope{
		Namespace: "acme", Object: identity.Object{}, Inside: nil, Containers: []string{"@server"},
		ContainerRoles: []string{access.RoleAccountAdministrator}, TokenNamespace: "",
		TokenNamespaceRoles: nil, All: false,
	}), "grt-2", "grt-3", "grt-4", "grt-5")

	review(t, rs, "collection acme/payments", everyRow(access.ReviewScope{
		Namespace:      "",
		Object:         identity.CollectionObject("acme", "payments").WithCollectionID("col-payments"),
		Inside:         nil,
		Containers:     []string{"@server", "acme"},
		ContainerRoles: []string{access.RoleAccountAdministrator, access.RoleWriter},
		TokenNamespace: "", TokenNamespaceRoles: nil, All: false,
	}), "grt-2", "grt-3", "grt-4")

	// The grants on the units of Inside are in the scope; a unit that is not
	// listed, also one of another namespace, never is.
	review(t, rs, "collection acme/payments with its units", everyRow(access.ReviewScope{
		Namespace:      "",
		Object:         identity.CollectionObject("acme", "payments").WithCollectionID("col-payments"),
		Inside:         []string{"unit:stk-acme-web", "unit:stk-beta-other"},
		Containers:     []string{"@server", "acme"},
		ContainerRoles: []string{access.RoleAccountAdministrator, access.RoleWriter},
		TokenNamespace: "", TokenNamespaceRoles: nil, All: false,
	}), "grt-2", "grt-3", "grt-4", "grt-5")

	review(t, rs, "collection acme/payments with a unit that has no grant", everyRow(access.ReviewScope{
		Namespace:      "",
		Object:         identity.CollectionObject("acme", "payments").WithCollectionID("col-payments"),
		Inside:         []string{"unit:stk-acme-api"},
		Containers:     []string{"@server", "acme"},
		ContainerRoles: []string{access.RoleAccountAdministrator, access.RoleWriter},
		TokenNamespace: "", TokenNamespaceRoles: nil, All: false,
	}), "grt-2", "grt-3", "grt-4")

	// A container grant with a role outside ContainerRoles stays out.
	review(t, rs, "namespace beta", everyRow(access.ReviewScope{
		Namespace: "beta", Object: identity.Object{}, Inside: nil, Containers: []string{"@server"},
		ContainerRoles: []string{access.RoleViewer}, TokenNamespace: "", TokenNamespaceRoles: nil, All: false,
	}), "grt-6")
}

// testReviewTokenNamespace checks the scope of the export of a principal
// of a namespace: a grant of a token-namespace role on the token namespace
// is in it, and a grant of another role on it, a grant of a token-namespace
// role on another namespace, or a grant of a token-namespace role on the
// installation is not, unless the installation role is in ContainerRoles.
func testReviewTokenNamespace(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)
	bot := identity.PrincipalObject("prn-bot")

	mustCreateGrant(t, s, grantAt(1, "grt-1", principalSubject("prn-a"), access.RoleAdmin,
		identity.NamespaceObject("acme")))
	mustCreateGrant(t, s, grantAt(2, "grt-2", principalSubject("prn-b"), access.RoleViewer,
		identity.NamespaceObject("acme")))
	mustCreateGrant(t, s, grantAt(3, "grt-3", principalSubject("prn-c"), access.RoleAdmin,
		identity.NamespaceObject("beta")))
	mustCreateGrant(t, s, grantAt(4, "grt-4", principalSubject("prn-d"), access.RoleAdmin,
		identity.ServerObject()))

	scope := access.ReviewScope{
		Object: bot, Namespace: "", Inside: nil, Containers: []string{"@server"},
		ContainerRoles: []string{access.RoleViewer}, TokenNamespace: "acme",
		TokenNamespaceRoles: []string{access.RoleAdmin}, All: false,
	}

	review(t, rs, "export of a principal with a token namespace", everyRow(scope), "grt-1")

	scope.ContainerRoles = []string{access.RoleAdmin}

	review(t, rs, "export of a principal with the installation role", everyRow(scope), "grt-1", "grt-4")
}

func testReviewSubject(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)
	seedReview(t, s)

	q := everyRow(allScope())
	q.Subject = opsGroup().String()

	rows := review(t, rs, "grants of the group", q, "grt-2", "grt-3")
	for i := range rows {
		if rows[i].Via != nil || rows[i].MemberCount == nil || *rows[i].MemberCount != 0 {
			t.Fatalf("group row = %+v, want no via and 0 members", rows[i])
		}
	}

	q.Subject = principalSubject("prn-a").String()

	rows = review(t, rs, "grants of prn-a without its groups", q, "grt-1", "grt-4", "grt-5")
	for i := range rows {
		if rows[i].MemberCount != nil || rows[i].RevokedAt != nil {
			t.Fatalf("principal row = %+v, want no member count and no revoke", rows[i])
		}
	}
}

func testReviewPaging(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)

	// Two grants in the same millisecond come in the order of their IDs.
	grants := []access.Grant{
		grantAt(1, "grt-b", principalSubject("prn-a"), access.RoleViewer, identity.NamespaceObject("acme")),
		grantAt(1, "grt-a", principalSubject("prn-b"), access.RoleViewer, identity.NamespaceObject("acme")),
		grantAt(2, "grt-c", principalSubject("prn-c"), access.RoleViewer, identity.NamespaceObject("acme")),
	}
	for i := range grants {
		mustCreateGrant(t, s, grants[i])
	}

	q := everyRow(allScope())
	q.Limit = 2

	rows, total, err := rs.ReviewGrants(t.Context(), q)
	if err != nil || len(rows) != 2 || total != 3 {
		t.Fatalf("first page = %d rows, total %d, %v; want 2 of 3", len(rows), total, err)
	}

	if rows[0].Grant.ID != "grt-a" || rows[1].Grant.ID != "grt-b" {
		t.Fatalf("first page = %s %s, want grt-a grt-b", rows[0].Grant.ID, rows[1].Grant.ID)
	}

	q.After = rows[1].Key()

	rows, total, err = rs.ReviewGrants(t.Context(), q)
	if err != nil || len(rows) != 1 || rows[0].Grant.ID != "grt-c" || total != 3 {
		t.Fatalf("second page = %d rows, total %d, %v; want grt-c of 3", len(rows), total, err)
	}
}

func testReviewHistory(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)
	acme := identity.NamespaceObject("acme")

	mustCreateGrant(t, s, grantAt(1, "grt-1", principalSubject("prn-a"), access.RoleViewer, acme))
	mustCreateGrant(t, s, grantAt(3, "grt-2", principalSubject("prn-b"), access.RoleViewer, acme))

	if err := revokeGrant(t, s, "grt-1", "acme", entryAt(at(5))); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	review(t, rs, "snapshot", everyRow(allScope()), "grt-2")

	window := func(from, to int) access.ReviewQuery {
		q := everyRow(allScope())
		q.History, q.FromMs, q.ToMs = true, at(from).UnixMilli(), at(to).UnixMilli()

		return q
	}

	rows := review(t, rs, "history of 0 to 9", window(0, 9), "grt-1", "grt-2")
	if rows[0].RevokedAt == nil || !rows[0].RevokedAt.Equal(at(5)) || rows[0].RevokedBy != "admin" {
		t.Fatalf("revoked row = %+v, want the revoke at minute 5 by admin", rows[0])
	}

	review(t, rs, "history of 0 to 1", window(0, 1))
	review(t, rs, "history of 0 to 2", window(0, 2), "grt-1")
	review(t, rs, "history of 4 to 9", window(4, 9), "grt-1", "grt-2")
	review(t, rs, "history of 5 to 9", window(5, 9), "grt-2")
}

func testReviewThroughGroups(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)

	ps, ok := s.(principal.Store)
	if !ok {
		t.Skip("the store keeps no principals")
	}

	seedMemberships(t, ps)

	acme := identity.NamespaceObject("acme")
	mustCreateGrant(t, s, grantAt(0, "grt-1", principalSubject("prn-a"), access.RoleViewer, acme))
	mustCreateGrant(t, s, grantAt(1, "grt-2", opsGroup(), access.RoleWriter, acme))
	mustCreateGrant(t, s, grantAt(3, "grt-3", opsGroup(), access.RoleAuditor, acme))

	if err := revokeGrant(t, s, "grt-3", "acme", entryAt(at(4))); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	q := everyRow(allScope())
	q.Subject, q.Principal = principalSubject("prn-a").String(), "prn-a"

	checkGroupSnapshot(t, rs, q)
	checkGroupHistory(t, rs, q)
}

// checkGroupSnapshot reads the current rows of prn-a: its own grant, and
// the grant of its group through the current membership.
func checkGroupSnapshot(t *testing.T, rs access.ReviewStore, q access.ReviewQuery) {
	t.Helper()

	rows := review(t, rs, "snapshot of prn-a", q, "grt-1", "grt-2")
	if rows[0].Via != nil || rows[0].SubjectName != "alice" {
		t.Fatalf("own row = %+v, want alice with no via", rows[0])
	}

	if v := rows[1].Via; v == nil || !v.AddedAt.Equal(at(8)) || v.RemovedAt != nil ||
		rows[1].SubjectName != "ops" || rows[1].MemberCount == nil || *rows[1].MemberCount != 1 {
		t.Fatalf("group row = %+v, want ops through the membership from minute 8", rows[1])
	}
}

// checkGroupHistory reads the history of prn-a: one row for each
// membership that overlaps a grant of the group and the window.
func checkGroupHistory(t *testing.T, rs access.ReviewStore, q access.ReviewQuery) {
	t.Helper()

	q.History, q.FromMs, q.ToMs = true, at(0).UnixMilli(), at(10).UnixMilli()

	rows := review(t, rs, "history of prn-a", q, "grt-1", "grt-2", "grt-2", "grt-3")
	if v := rows[1].Via; v == nil || !v.AddedAt.Equal(at(2)) || v.RemovedAt == nil || !v.RemovedAt.Equal(at(6)) {
		t.Fatalf("first membership row = %+v, want minute 2 to 6", rows[1])
	}

	q.FromMs, q.ToMs = at(6).UnixMilli(), at(8).UnixMilli()
	review(t, rs, "history of prn-a out of the group", q, "grt-1")
}

// seedMemberships makes alice (prn-a) and the group ops, with alice a
// member from minute 2 to 6 and again from minute 8.
func seedMemberships(t *testing.T, ps principal.Store) {
	t.Helper()

	ctx := t.Context()

	prn := principal.Principal{
		CreatedAt: at(0), UpdatedAt: at(0), DisabledAt: time.Time{}, ID: "prn-a", Subject: "alice", DisplayName: "",
		Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
	}
	if err := ps.CreatePrincipal(ctx, prn, entry()); err != nil {
		t.Fatalf("create principal: %v", err)
	}

	grp := principal.Group{
		CreatedAt: at(0), UpdatedAt: at(0), ID: "grp-ops", Name: "ops", Description: "",
		Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
	}
	if err := ps.CreateGroup(ctx, grp, entry()); err != nil {
		t.Fatalf("create group: %v", err)
	}

	for _, step := range []struct {
		add    bool
		minute int
	}{{true, 2}, {false, 6}, {true, 8}} {
		var err error
		if step.add {
			err = ps.AddMember(ctx, principal.Membership{
				AddedAt: at(step.minute), SuspendedAt: time.Time{}, GroupID: "grp-ops", PrincipalID: "prn-a",
				Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive,
				Pinned: false,
			}, entryAt(at(step.minute)), nil)
		} else {
			err = ps.RemoveMember(ctx, "grp-ops", "prn-a", entryAt(at(step.minute)))
		}

		if err != nil {
			t.Fatalf("membership at minute %d: %v", step.minute, err)
		}
	}
}

func testReviewUnit(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)
	web := unit(t, "beta", "web")

	mustCreateGrant(t, s, grantAt(1, "grt-1", principalSubject("prn-a"), access.RoleViewer, web))

	scope := func(obj identity.Object) access.ReviewScope {
		return access.ReviewScope{
			Object: obj, Namespace: "", Inside: nil, Containers: nil, ContainerRoles: nil, TokenNamespace: "",
			TokenNamespaceRoles: nil, All: false,
		}
	}

	rows := review(t, rs, "export of the unit", everyRow(scope(web)), "grt-1")
	if rows[0].Grant.Object.UnitID() != web.UnitID() {
		t.Fatalf("row object = %+v, want the stable ID %q", rows[0].Grant.Object, web.UnitID())
	}

	review(t, rs, "export of a new unit with the old name", everyRow(scope(web.WithUnitID("stk-new"))))
	review(t, rs, "export of a unit with no stable ID", everyRow(scope(web.WithUnitID(""))))
}
