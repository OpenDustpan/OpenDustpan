package accesstest

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// reviewMemberships returns one active membership from each source.
func reviewMemberships() []principal.Membership {
	out := make([]principal.Membership, 0, 3)

	for i, c := range []struct {
		source   principal.MembershipSource
		sourceID string
	}{
		{principal.MembershipSourceLocal, ""},
		{principal.MembershipSourceSCIM, "scim-token-1"},
		{principal.MembershipSourceClaim, "idp-1"},
	} {
		out = append(out, principal.Membership{
			AddedAt: at(i + 2), SuspendedAt: time.Time{}, GroupID: "grp-ops",
			PrincipalID: "prn-" + string(c.source), Source: c.source, SourceID: c.sourceID,
			State: principal.MembershipStateActive, Pinned: false,
		})
	}

	return out
}

// seedReviewMembers stores the members of ops and one grant of that group.
func seedReviewMembers(t *testing.T, s access.Store, members []principal.Membership) {
	t.Helper()

	ps, ok := s.(principal.Store)
	if !ok {
		t.Skip("the store keeps no principals")
	}

	ctx := t.Context()
	group := principal.Group{
		CreatedAt: at(0), UpdatedAt: at(0), ID: "grp-ops", Name: "ops", Description: "",
		Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
	}
	if err := ps.CreateGroup(ctx, group, entry()); err != nil {
		t.Fatalf("create group: %v", err)
	}

	for i := range members {
		m := members[i]
		p := principal.Principal{
			CreatedAt: at(0), UpdatedAt: at(0), DisabledAt: time.Time{}, ID: m.PrincipalID,
			Subject: m.PrincipalID, DisplayName: "", Kind: principal.KindPerson, Source: principal.SourceLocal,
			Namespace: "", GroupCount: 0, BreakGlass: false,
		}
		if err := ps.CreatePrincipal(ctx, p, entry()); err != nil {
			t.Fatalf("create principal: %v", err)
		}

		if err := ps.AddMember(ctx, m, entryAt(m.AddedAt), nil); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}

	mustCreateGrant(t, s, grantAt(1, "grt-ops", opsGroup(), access.RoleWriter, identity.NamespaceObject("acme")))
}

func testReviewMembershipSources(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)
	members := reviewMemberships()
	seedReviewMembers(t, s, members)

	for _, history := range []bool{false, true} {
		q := everyRow(allScope())
		q.Expand, q.History = true, history
		q.FromMs, q.ToMs = at(0).UnixMilli(), at(60).UnixMilli()

		rows := review(t, rs, "membership sources", q, "grt-ops", "grt-ops", "grt-ops")
		for i := range members {
			checkReviewMembership(t, rows[i].Via, wantVia{member: members[i], suspended: false})
		}
	}

	for i := range members {
		m := members[i]
		q := everyRow(allScope())
		q.Subject, q.Principal = principalSubject(m.PrincipalID).String(), m.PrincipalID
		rows := review(t, rs, "filtered membership source", q, "grt-ops")
		checkReviewMembership(t, rows[0].Via, wantVia{member: m, suspended: false})

		cq := callerQuery(snapshotMinute)
		cq.Principal = m.PrincipalID
		rows = callerGrants(t, rs, "caller membership source", cq, "grt-ops")
		checkReviewMembership(t, rows[0].Via, wantVia{member: m, suspended: false})
	}
}

// wantVia is the membership that a row must show: the stored membership and
// whether the row reports it as suspended.
type wantVia struct {
	member    principal.Membership
	suspended bool
}

// checkReviewMembership checks the source, the source ID and the suspension of one membership.
func checkReviewMembership(t *testing.T, got *access.Membership, want wantVia) {
	t.Helper()

	if got == nil {
		t.Fatal("membership is nil")
	}

	if got.PrincipalID != want.member.PrincipalID || got.Source != string(want.member.Source) ||
		got.SourceID != want.member.SourceID || got.Suspended != want.suspended {
		t.Fatalf("membership = %+v, want %s from %s/%s with suspended %t",
			got, want.member.PrincipalID, want.member.Source, want.member.SourceID, want.suspended)
	}
}

func testReviewMembershipSuspension(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)
	members := reviewMemberships()
	members[1].State, members[1].SuspendedAt = principal.MembershipStateSuspended, at(20)
	members[2].State, members[2].SuspendedAt = principal.MembershipStateSuspended, at(40)
	seedReviewMembers(t, s, members)

	for _, c := range []struct {
		minute int
		want   [3]bool
	}{
		{19, [3]bool{false, false, false}},
		{20, [3]bool{false, true, false}},
		{snapshotMinute, [3]bool{false, true, false}},
		{40, [3]bool{false, true, true}},
	} {
		q := everyRow(allScope())
		q.Expand, q.AsOfMs = true, at(c.minute).UnixMilli()
		rows := review(t, rs, "snapshot membership suspension", q, "grt-ops", "grt-ops", "grt-ops")

		for i := range members {
			checkReviewMembership(t, rows[i].Via, wantVia{member: members[i], suspended: c.want[i]})
		}
	}

	checkReviewSuspensionFilters(t, rs, members)

	for i := range members {
		q := callerQuery(snapshotMinute)
		q.Principal = members[i].PrincipalID
		want := []string{"grt-ops"}
		if i != 0 {
			want = nil
		}

		callerGrants(t, rs, "caller excludes suspended memberships", q, want...)
	}
}

// checkReviewSuspensionFilters checks expanded and filtered snapshots and histories.
func checkReviewSuspensionFilters(t *testing.T, rs access.ReviewStore, members []principal.Membership) {
	t.Helper()

	for _, history := range []bool{false, true} {
		q := everyRow(allScope())
		q.Expand, q.History = true, history
		q.FromMs, q.ToMs = at(0).UnixMilli(), at(60).UnixMilli()

		want := []string{"grt-ops", "grt-ops", "grt-ops"}
		if history {
			want = want[:1]
		}

		review(t, rs, "expanded membership suspension", q, want...)

		for i := range members {
			m := members[i]
			q.Subject, q.Principal = principalSubject(m.PrincipalID).String(), m.PrincipalID
			want := []string{"grt-ops"}
			if history && i != 0 {
				want = nil
			}

			rows := review(t, rs, "filtered membership suspension", q, want...)
			if len(rows) != 0 {
				checkReviewMembership(t, rows[0].Via, wantVia{member: m, suspended: !history && i == 1})
			}
		}
	}
}

func testReviewUnknownMembershipSource(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)
	sql, ok := s.(interface {
		ExecForTest(ctx context.Context, query string, args ...any) error
	})
	if !ok {
		t.Skip("the store has no SQL test helper")
	}

	members := reviewMemberships()
	seedReviewMembers(t, s, members)

	const query = "UPDATE group_members SET source = $1 WHERE group_id = $2 AND principal_id = $3"
	if err := sql.ExecForTest(t.Context(), query, "ldap-forged", "grp-ops", members[1].PrincipalID); err != nil {
		t.Fatalf("write unknown membership source: %v", err)
	}

	for _, history := range []bool{false, true} {
		q := everyRow(allScope())
		q.Expand, q.History = true, history
		q.FromMs, q.ToMs = at(0).UnixMilli(), at(60).UnixMilli()

		rows, total, err := rs.ReviewGrants(t.Context(), q)
		if !errors.Is(err, access.ErrStoredSource) || rows != nil || total != 0 {
			t.Fatalf("unknown source with history %t = %+v, total %d, %v; want no rows and ErrStoredSource",
				history, rows, total, err)
		}
	}

	q := callerQuery(snapshotMinute)
	q.Principal = members[1].PrincipalID
	rows, total, err := rs.CallerGrants(t.Context(), q)
	if !errors.Is(err, access.ErrStoredSource) || rows != nil || total != 0 {
		t.Fatalf("caller with unknown source = %+v, total %d, %v; want no rows and ErrStoredSource", rows, total, err)
	}
}

// testReviewAsOf reads snapshots at several times: a grant and a
// membership count only while they were current then.
func testReviewAsOf(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)

	ps, ok := s.(principal.Store)
	if !ok {
		t.Skip("the store keeps no principals")
	}

	seedMemberships(t, ps)

	acme := identity.NamespaceObject("acme")
	mustCreateGrant(t, s, grantAt(1, "grt-1", principalSubject("prn-b"), access.RoleViewer, acme))
	mustCreateGrant(t, s, grantAt(3, "grt-2", opsGroup(), access.RoleWriter, acme))

	if err := revokeGrant(t, s, "grt-1", "acme", entryAt(at(5))); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	asOf := func(minute int) access.ReviewQuery {
		q := everyRow(allScope())
		q.AsOfMs = at(minute).UnixMilli()

		return q
	}

	review(t, rs, "snapshot at minute 0", asOf(0))
	review(t, rs, "snapshot at minute 1", asOf(1), "grt-1")
	review(t, rs, "snapshot at minute 4", asOf(4), "grt-1", "grt-2")
	review(t, rs, "snapshot at minute 5", asOf(5), "grt-2")

	// alice is a member from minute 2 to 6 and from minute 8.
	q := asOf(4)
	q.Subject, q.Principal = principalSubject("prn-a").String(), "prn-a"

	rows := review(t, rs, "snapshot of prn-a at minute 4", q, "grt-2")
	if rows[0].Via == nil || !rows[0].Via.AddedAt.Equal(at(2)) || *rows[0].MemberCount != 1 {
		t.Fatalf("group row at minute 4 = %+v, want the membership from minute 2 and 1 member", rows[0])
	}

	q.AsOfMs = at(7).UnixMilli()
	review(t, rs, "snapshot of prn-a at minute 7", q)
}

// testReviewExpand expands the grants of groups into one row for each
// member, in the order of the membership start and the member ID.
func testReviewExpand(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)

	ps, ok := s.(principal.Store)
	if !ok {
		t.Skip("the store keeps no principals")
	}

	seedTeam(t, ps)

	acme := identity.NamespaceObject("acme")
	mustCreateGrant(t, s, grantAt(1, "grt-1", principalSubject("prn-x"), access.RoleViewer, acme))
	mustCreateGrant(t, s, grantAt(2, "grt-2", access.Subject{Kind: access.SubjectGroup, ID: "grp-team"},
		access.RoleWriter, acme))
	mustCreateGrant(t, s, grantAt(3, "grt-3", access.Subject{Kind: access.SubjectGroup, ID: "grp-empty"},
		access.RoleViewer, acme))

	q := everyRow(allScope())
	q.Expand = true

	rows := review(t, rs, "expanded export", q, "grt-1", "grt-2", "grt-2", "grt-3")

	members := []string{rows[1].Via.PrincipalSubject, rows[2].Via.PrincipalSubject}
	if members[0] != "xavier" || members[1] != "yvonne" || rows[1].Via.PrincipalID != "prn-x" {
		t.Fatalf("member rows = %v, want xavier then yvonne", members)
	}

	if rows[3].Via != nil {
		t.Fatalf("row of a group with no member = %+v, want no via", rows[3])
	}

	checkExpandedPages(t, rs, q)
}

// checkExpandedPages reads the expanded export of q two rows at a time. The
// two members joined in the same millisecond, so a page can end between
// them.
func checkExpandedPages(t *testing.T, rs access.ReviewStore, q access.ReviewQuery) {
	t.Helper()

	q.Limit, q.Count = 2, false

	first, _, err := rs.ReviewGrants(t.Context(), q)
	if err != nil || len(first) != 2 || first[1].Via == nil || first[1].Via.PrincipalID != "prn-x" {
		t.Fatalf("first expanded page = %+v, %v; want grt-1 and xavier", first, err)
	}

	q.After = first[1].Key()

	next, _, err := rs.ReviewGrants(t.Context(), q)
	if err != nil || len(next) != 2 || next[0].Via == nil || next[0].Via.PrincipalID != "prn-y" {
		t.Fatalf("second expanded page = %+v, %v; want yvonne first", next, err)
	}
}

// seedTeam makes xavier (prn-x) and yvonne (prn-y), both members of the
// group team from minute 1, and the group empty with no member.
func seedTeam(t *testing.T, ps principal.Store) {
	t.Helper()

	ctx := t.Context()

	for _, p := range [][2]string{{"prn-x", "xavier"}, {"prn-y", "yvonne"}} {
		prn := principal.Principal{
			CreatedAt: at(0), UpdatedAt: at(0), DisabledAt: time.Time{}, ID: p[0], Subject: p[1], DisplayName: "",
			Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
		}
		if err := ps.CreatePrincipal(ctx, prn, entry()); err != nil {
			t.Fatalf("create principal: %v", err)
		}
	}

	for _, g := range [][2]string{{"grp-team", "team"}, {"grp-empty", "empty"}} {
		grp := principal.Group{
			CreatedAt: at(0), UpdatedAt: at(0), ID: g[0], Name: g[1], Description: "",
			Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
		}
		if err := ps.CreateGroup(ctx, grp, entry()); err != nil {
			t.Fatalf("create group: %v", err)
		}
	}

	for _, id := range []string{"prn-y", "prn-x"} {
		m := principal.Membership{
			AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: "grp-team", PrincipalID: id,
			Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive,
			Pinned: false,
		}
		if err := ps.AddMember(ctx, m, entryAt(at(1)), nil); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}
}

// testReviewCustomRoles checks that a history keeps a grant on a
// container with a custom role that is not in ContainerRoles, a deleted
// role included, and that a snapshot does not.
func testReviewCustomRoles(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)

	mustCreateRole(t, s, newRole("rol-gone", "gone", identity.ActionNamespaceRead))
	mustCreateGrant(t, s, grantAt(1, "grt-1", principalSubject("prn-a"), "rol-gone", identity.ServerObject()))
	mustCreateGrant(t, s, grantAt(2, "grt-2", principalSubject("prn-a"), access.RoleInstallationOperator,
		identity.ServerObject()))

	if err := revokeGrant(t, s, "grt-1", "", entryAt(at(3))); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if err := s.DeleteRole(t.Context(), "rol-gone", entryAt(at(4))); err != nil {
		t.Fatalf("delete role: %v", err)
	}

	scope := access.ReviewScope{
		Namespace: "acme", Object: identity.Object{}, Inside: nil, Containers: []string{"@server"},
		ContainerRoles: []string{access.RoleViewer}, TokenNamespace: "", TokenNamespaceRoles: nil, All: false,
	}

	q := everyRow(scope)
	q.History, q.FromMs, q.ToMs = true, at(0).UnixMilli(), at(9).UnixMilli()

	review(t, rs, "history with a deleted custom role", q, "grt-1")

	q = everyRow(scope)
	q.AsOfMs = at(2).UnixMilli()

	review(t, rs, "snapshot with a custom role outside the container roles", q)
}

// testReviewCount checks that a query with Count false answers no count.
func testReviewCount(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)
	seedReview(t, s)

	q := everyRow(allScope())
	q.Count = false

	rows, total, err := rs.ReviewGrants(t.Context(), q)
	if err != nil || len(rows) != 6 || total != 0 {
		t.Fatalf("export with no count = %d rows, total %d, %v; want 6 rows and no count", len(rows), total, err)
	}
}

// testReviewUnits reads the units of grants: a live stack, a deleted
// workspace with its name at the delete, and no unit for an unknown ID.
func testReviewUnits(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)
	us := unitStore(t, s)
	ctx := t.Context()

	ns, err := us.GetNamespace(ctx, "acme")
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	name, err := workspace.ParseName("network")
	if err != nil {
		t.Fatalf("workspace name: %v", err)
	}

	gone := scoped(entry(), "acme")
	gone.Action, gone.Object = "unit:manage", "opentofu:acme/network"

	ref := workspace.Ref{Namespace: "acme", Name: name}
	deletion := inventory.Deletion{Entry: gone, PurgeAfter: gone.At.Add(time.Hour)}
	if delErr := us.DeleteWorkspace(ctx, ref, ns.Fence(), deletion); delErr != nil {
		t.Fatalf("delete workspace: %v", delErr)
	}

	// A lookup that returns nothing fails this case here, as the review reads
	// the same units.
	assertLiveStack(t, us)

	got, err := rs.ReviewUnits(ctx, []string{"stk-web", "ws-network", "stk-purged"})
	if err != nil {
		t.Fatalf("review units: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("review units = %v, want the stack and the workspace", got)
	}

	if web := got["stk-web"]; web.Deleted || web.Object.String() != "pulumi:acme/web/prod" {
		t.Errorf("stack = %+v, want pulumi:acme/web/prod, live", web)
	}

	if ws := got["ws-network"]; !ws.Deleted || ws.Object.String() != "opentofu:acme/network" ||
		ws.Object.UnitID() != "ws-network" {
		t.Errorf("workspace = %+v, want opentofu:acme/network, deleted, with its name at the delete", ws)
	}
}
