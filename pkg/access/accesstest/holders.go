package accesstest

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// testListGrantHolders lists the grants on acme of a person with a display
// name, a service with none, a group and a subject with no record. Each
// grant carries the name and the kind of its holder, and the holder and
// role filters keep their grants and count only them.
func testListGrantHolders(t *testing.T, s access.Store) {
	t.Helper()

	ps, ok := s.(principal.Store)
	if !ok {
		t.Skip("the store keeps no principals")
	}

	ctx := t.Context()

	people := []principal.Principal{
		{
			CreatedAt:   at(0),
			UpdatedAt:   at(0),
			ID:          "prn-a",
			Subject:     "alice",
			DisplayName: "Alice A.",
			Kind:        principal.KindPerson,
			Source:      principal.SourceLocal,
			Namespace:   "",
			GroupCount:  0, BreakGlass: false,
			DisabledAt: time.Time{},
		},
		{
			CreatedAt:   at(0),
			UpdatedAt:   at(0),
			ID:          "prn-ci",
			Subject:     "ci-deploy",
			DisplayName: "",
			Kind:        principal.KindService,
			Source:      principal.SourceLocal,
			Namespace:   "",
			GroupCount:  0, BreakGlass: false,
			DisabledAt: time.Time{},
		},
	}
	for i := range people {
		if err := ps.CreatePrincipal(ctx, people[i], entry()); err != nil {
			t.Fatalf("create principal %s: %v", people[i].ID, err)
		}
	}

	grp := principal.Group{
		CreatedAt: at(0), UpdatedAt: at(0), ID: "grp-ops", Name: "ops", Description: "",
		Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
	}
	if err := ps.CreateGroup(ctx, grp, entry()); err != nil {
		t.Fatalf("create group: %v", err)
	}

	acme := identity.NamespaceObject("acme")
	mustCreateGrant(t, s, newGrant("grt-1", principalSubject("prn-a"), access.RoleViewer, acme))
	mustCreateGrant(t, s, newGrant("grt-2", principalSubject("prn-a"), access.RoleWriter, acme))
	mustCreateGrant(t, s, newGrant("grt-3", principalSubject("prn-ci"), access.RoleWriter, acme))
	mustCreateGrant(t, s, newGrant("grt-4", opsGroup(), access.RoleViewer, acme))
	mustCreateGrant(t, s, newGrant("grt-5", principalSubject("prn-none"), access.RoleViewer, acme))

	list, total, err := s.ListGrantsOn(ctx, acme, everyGrant())
	if err != nil || total != 5 {
		t.Fatalf("list = %v, %d, %v; want 5 grants", list, total, err)
	}

	want := map[string]access.Holder{
		"grt-1": {Name: "Alice A.", Kind: string(principal.KindPerson)},
		"grt-2": {Name: "Alice A.", Kind: string(principal.KindPerson)},
		"grt-3": {Name: "ci-deploy", Kind: string(principal.KindService)},
		"grt-4": {Name: "ops", Kind: access.HolderGroup},
		"grt-5": {Name: "", Kind: ""},
	}

	for i := range list {
		if got := list[i].Holder; got != want[list[i].Grant.ID] {
			t.Errorf("holder of %s = %+v, want %+v", list[i].Grant.ID, got, want[list[i].Grant.ID])
		}
	}

	filtered := func(subject, role string) access.GrantQuery {
		q := everyGrant()
		q.Subject, q.Role = subject, role

		return q
	}

	for _, c := range []struct {
		q     access.GrantQuery
		label string
		want  []string
	}{
		{filtered(principalSubject("prn-a").String(), ""), "grants of alice", []string{"grt-1", "grt-2"}},
		{filtered("", access.RoleWriter), "writer grants", []string{"grt-2", "grt-3"}},
		{filtered(principalSubject("prn-a").String(), access.RoleViewer), "viewer grants of alice", []string{"grt-1"}},
		{filtered(opsGroup().String(), access.RoleWriter), "writer grants of ops", nil},
	} {
		got, count, listErr := s.ListGrantsOn(ctx, acme, c.q)
		if listErr != nil {
			t.Fatalf("%s: %v", c.label, listErr)
		}

		assertIDs(t, c.label, grantsOf(got), c.want...)

		if count != int64(len(c.want)) {
			t.Errorf("%s total = %d, want %d", c.label, count, len(c.want))
		}
	}
}
