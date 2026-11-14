package accesstest

import (
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// testSuspendedMembershipGivesNoRights proves that a suspended membership
// gives no group: the subjects of a suspended principal hold no group,
// while the subjects of an active member hold it.
func testSuspendedMembershipGivesNoRights(t *testing.T, s access.Store) {
	t.Helper()

	ps, ok := s.(principal.Store)
	if !ok {
		t.Skip("the store keeps no principals")
	}

	callers, isCallers := s.(access.Callers)
	if !isCallers {
		t.Skip("the store answers no subjects of a caller")
	}

	ctx := t.Context()

	for _, p := range [][2]string{{"prn-suspended", "suspended"}, {"prn-active", "active"}} {
		prn := principal.Principal{
			CreatedAt: at(0), UpdatedAt: at(0), DisabledAt: time.Time{}, ID: p[0], Subject: p[1], DisplayName: "",
			Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0,
			BreakGlass: false,
		}
		if err := ps.CreatePrincipal(ctx, prn, entry()); err != nil {
			t.Fatalf("create principal %q: %v", p[1], err)
		}
	}

	grp := principal.Group{
		CreatedAt: at(0), UpdatedAt: at(0), ID: "grp-ops", Name: "ops", Description: "",
		Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
	}
	if err := ps.CreateGroup(ctx, grp, entry()); err != nil {
		t.Fatalf("create group: %v", err)
	}

	suspended := principal.Membership{
		AddedAt: at(1), SuspendedAt: at(2), GroupID: "grp-ops", PrincipalID: "prn-suspended",
		Source: principal.MembershipSourceSCIM, SourceID: "scim-token-1",
		State: principal.MembershipStateSuspended, Pinned: false,
	}
	if err := ps.AddMember(ctx, suspended, entry(), nil); err != nil {
		t.Fatalf("add suspended member: %v", err)
	}

	active := principal.Membership{
		AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: "grp-ops", PrincipalID: "prn-active",
		Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive,
		Pinned: false,
	}
	if err := ps.AddMember(ctx, active, entry(), nil); err != nil {
		t.Fatalf("add active member: %v", err)
	}

	if got := subjectNames(t, callers, "suspended"); !slices.Equal(got, []string{"principal:prn-suspended"}) {
		t.Fatalf("subjects of suspended = %v, want only its principal", got)
	}

	want := []string{"group:grp-ops#member", "principal:prn-active"}
	if got := subjectNames(t, callers, "active"); !slices.Equal(got, want) {
		t.Fatalf("subjects of active = %v, want %v", got, want)
	}
}

// testReviewHistoryHidesSuspendedMember checks that a history lists only
// the active member of a group: one suspended and one active member give
// one expanded row, with a member count of one.
func testReviewHistoryHidesSuspendedMember(t *testing.T, s access.Store) {
	t.Helper()

	rs := reviewStore(t, s)

	ps, ok := s.(principal.Store)
	if !ok {
		t.Skip("the store keeps no principals")
	}

	ctx := t.Context()

	for _, p := range [][2]string{{"prn-suspended", "suspended"}, {"prn-active", "active"}} {
		prn := principal.Principal{
			CreatedAt: at(0), UpdatedAt: at(0), DisabledAt: time.Time{}, ID: p[0], Subject: p[1], DisplayName: "",
			Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0,
			BreakGlass: false,
		}
		if err := ps.CreatePrincipal(ctx, prn, entry()); err != nil {
			t.Fatalf("create principal %q: %v", p[1], err)
		}
	}

	grp := principal.Group{
		CreatedAt: at(0), UpdatedAt: at(0), ID: "grp-ops", Name: "ops", Description: "",
		Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
	}
	if err := ps.CreateGroup(ctx, grp, entry()); err != nil {
		t.Fatalf("create group: %v", err)
	}

	suspended := principal.Membership{
		AddedAt: at(1), SuspendedAt: at(2), GroupID: "grp-ops", PrincipalID: "prn-suspended",
		Source: principal.MembershipSourceSCIM, SourceID: "scim-token-1",
		State: principal.MembershipStateSuspended, Pinned: false,
	}
	if err := ps.AddMember(ctx, suspended, entry(), nil); err != nil {
		t.Fatalf("add suspended member: %v", err)
	}

	active := principal.Membership{
		AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: "grp-ops", PrincipalID: "prn-active",
		Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive,
		Pinned: false,
	}
	if err := ps.AddMember(ctx, active, entry(), nil); err != nil {
		t.Fatalf("add active member: %v", err)
	}

	mustCreateGrant(t, s, grantAt(3, "grt-1", opsGroup(), access.RoleWriter, identity.NamespaceObject("acme")))

	q := everyRow(allScope())
	q.Expand = true
	q.History, q.FromMs, q.ToMs = true, at(0).UnixMilli(), at(snapshotMinute).UnixMilli()

	rows := review(t, rs, "expanded export without the suspended member", q, "grt-1")
	if len(rows) != 1 || rows[0].Via == nil || rows[0].Via.PrincipalID != "prn-active" {
		t.Fatalf("expanded row = %+v, want the active member", rows)
	}

	if rows[0].MemberCount == nil || *rows[0].MemberCount != 1 {
		t.Fatalf("member count = %+v, want 1 active member", rows[0].MemberCount)
	}
}
