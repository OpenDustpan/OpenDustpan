// Package principaltest is the conformance suite for principal.Store. Every
// built-in store runs it, and a custom store passed to dustpan.NewServer
// should too:
//
//	func TestPrincipalConformance(t *testing.T) {
//		t.Parallel()
//		principaltest.Run(t, func(t *testing.T) principal.Store { return newMyStore(t) })
//	}
package principaltest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/principal"
)

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) principal.Store

// Run executes the conformance suite against stores produced by newStore.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s principal.Store)
		name string
	}{
		{name: "principal round trip", test: testPrincipalRoundTrip},
		{name: "principal by subject", test: testPrincipalBySubject},
		{name: "duplicate subject is ErrAlreadyExists", test: testDuplicateSubject},
		{name: "duplicate principal ID is ErrAlreadyExists", test: testDuplicatePrincipalID},
		{name: "subject compares exactly", test: testSubjectCase},
		{name: "missing principal is ErrNotFound", test: testMissingPrincipal},
		{name: "list principals orders by subject", test: testListPrincipals},
		{name: "update principal keeps subject and kind", test: testUpdatePrincipal},
		{name: "update of a missing principal is ErrNotFound", test: testUpdateMissingPrincipal},
		{name: "delete principal removes its memberships", test: testDeletePrincipal},
		{name: "delete of a missing principal is ErrNotFound", test: testDeleteMissingPrincipal},
		{name: "group round trip", test: testGroupRoundTrip},
		{name: "duplicate group name is ErrAlreadyExists without regard to case", test: testDuplicateGroupName},
		{name: "missing group is ErrNotFound", test: testMissingGroup},
		{name: "list groups orders by name without regard to case", test: testListGroups},
		{name: "update group renames it", test: testUpdateGroup},
		{name: "rename to a taken group name is ErrAlreadyExists", test: testRenameGroupTaken},
		{name: "rename of a group to another case of its own name", test: testRenameGroupCase},
		{name: "update of a missing group is ErrNotFound", test: testUpdateMissingGroup},
		{name: "delete group removes its memberships", test: testDeleteGroup},
		{name: "delete of a missing group is ErrNotFound", test: testDeleteMissingGroup},
		{name: "member round trip", test: testMemberRoundTrip},
		{name: "a principal can be in more than one group", test: testManyGroups},
		{name: "duplicate membership is ErrAlreadyExists", test: testDuplicateMember},
		{name: "membership needs the group and the principal", test: testMemberMissing},
		{name: "remove of a missing membership is ErrNotFound", test: testRemoveMissingMember},
		{name: "list of a missing group or principal is ErrNotFound", test: testListMissing},
		{name: "every kind of principal can be a member", test: testEveryKindIsMember},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// at is a fixed, millisecond-precision instant: stores keep milliseconds.
func at(minute int) time.Time {
	return time.Date(2026, time.October, 13, 21, minute, 0, 0, time.UTC)
}

func newPrincipal(id, subject string, kind principal.Kind) principal.Principal {
	return principal.Principal{
		CreatedAt:   at(0),
		UpdatedAt:   at(0),
		ID:          id,
		Subject:     subject,
		DisplayName: "Name of " + subject,
		Kind:        kind,
		Disabled:    false,
	}
}

func newGroup(id, name string) principal.Group {
	return principal.Group{
		CreatedAt:   at(0),
		UpdatedAt:   at(0),
		ID:          id,
		Name:        name,
		Description: "The " + name + " group",
	}
}

func mustCreatePrincipal(t *testing.T, s principal.Store, p principal.Principal) {
	t.Helper()

	if err := s.CreatePrincipal(t.Context(), p); err != nil {
		t.Fatalf("create principal %q: %v", p.Subject, err)
	}
}

func mustCreateGroup(t *testing.T, s principal.Store, g principal.Group) {
	t.Helper()

	if err := s.CreateGroup(t.Context(), g); err != nil {
		t.Fatalf("create group %q: %v", g.Name, err)
	}
}

func mustAddMember(t *testing.T, s principal.Store, groupID, principalID string) {
	t.Helper()

	m := principal.Membership{AddedAt: at(1), GroupID: groupID, PrincipalID: principalID}
	if err := s.AddMember(t.Context(), m); err != nil {
		t.Fatalf("add %q to %q: %v", principalID, groupID, err)
	}
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("%s: got %v, want %v", what, err, want)
	}
}

func equalPrincipal(t *testing.T, got, want principal.Principal) {
	t.Helper()

	if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("times = %v/%v, want %v/%v", got.CreatedAt, got.UpdatedAt, want.CreatedAt, want.UpdatedAt)
	}

	got.CreatedAt, got.UpdatedAt = want.CreatedAt, want.UpdatedAt
	if got != want {
		t.Fatalf("principal = %+v, want %+v", got, want)
	}
}

func equalGroup(t *testing.T, got, want principal.Group) {
	t.Helper()

	if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("times = %v/%v, want %v/%v", got.CreatedAt, got.UpdatedAt, want.CreatedAt, want.UpdatedAt)
	}

	got.CreatedAt, got.UpdatedAt = want.CreatedAt, want.UpdatedAt
	if got != want {
		t.Fatalf("group = %+v, want %+v", got, want)
	}
}

func subjects(ps []principal.Principal) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Subject)
	}

	return out
}

func memberSubjects(ms []principal.Member) []string {
	out := make([]string, 0, len(ms))
	for i := range ms {
		out = append(out, ms[i].Principal.Subject)
	}

	return out
}

func groupNames(gs []principal.Group) []string {
	out := make([]string, 0, len(gs))
	for _, g := range gs {
		out = append(out, g.Name)
	}

	return out
}

func equalStrings(t *testing.T, what string, got, want []string) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Fatalf("%s = %q, want %q", what, got, want)
	}
}

func testPrincipalRoundTrip(t *testing.T, s principal.Store) {
	t.Helper()

	want := newPrincipal("prn-1", "alice@example.com", principal.KindPerson)
	mustCreatePrincipal(t, s, want)

	got, err := s.GetPrincipal(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	equalPrincipal(t, got, want)
}

func testPrincipalBySubject(t *testing.T, s principal.Store) {
	t.Helper()

	want := newPrincipal("prn-1", "ci-deploy", principal.KindService)
	mustCreatePrincipal(t, s, want)

	got, err := s.GetPrincipalBySubject(t.Context(), "ci-deploy")
	if err != nil {
		t.Fatalf("get by subject: %v", err)
	}

	equalPrincipal(t, got, want)

	_, err = s.GetPrincipalBySubject(t.Context(), "nobody")
	wantErr(t, "get by a missing subject", err, principal.ErrNotFound)
}

func testDuplicateSubject(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))

	err := s.CreatePrincipal(t.Context(), newPrincipal("prn-2", "alice", principal.KindService))
	wantErr(t, "second principal with the subject", err, principal.ErrAlreadyExists)
}

func testDuplicatePrincipalID(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))

	err := s.CreatePrincipal(t.Context(), newPrincipal("prn-1", "bob", principal.KindPerson))
	wantErr(t, "second principal with the ID", err, principal.ErrAlreadyExists)
}

func testSubjectCase(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreatePrincipal(t, s, newPrincipal("prn-2", "Alice", principal.KindPerson))

	got, err := s.GetPrincipalBySubject(t.Context(), "Alice")
	if err != nil {
		t.Fatalf("get by subject: %v", err)
	}

	if got.ID != "prn-2" {
		t.Fatalf("subject Alice = %q, want prn-2", got.ID)
	}
}

func testMissingPrincipal(t *testing.T, s principal.Store) {
	t.Helper()

	_, err := s.GetPrincipal(t.Context(), "prn-missing")
	wantErr(t, "get a missing principal", err, principal.ErrNotFound)
}

func testListPrincipals(t *testing.T, s principal.Store) {
	t.Helper()

	got, err := s.ListPrincipals(t.Context())
	if err != nil {
		t.Fatalf("list an empty store: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("empty store lists %d principals", len(got))
	}

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "carol", principal.KindPerson))
	mustCreatePrincipal(t, s, newPrincipal("prn-2", "alice", principal.KindPerson))
	mustCreatePrincipal(t, s, newPrincipal("prn-3", "bob", principal.KindWorkload))

	got, err = s.ListPrincipals(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	equalStrings(t, "subjects", subjects(got), []string{"alice", "bob", "carol"})
}

func testUpdatePrincipal(t *testing.T, s principal.Store) {
	t.Helper()

	p := newPrincipal("prn-1", "alice", principal.KindPerson)
	mustCreatePrincipal(t, s, p)

	changed := p
	changed.DisplayName = "Alice A."
	changed.Disabled = true
	changed.UpdatedAt = at(5)
	changed.Subject = "mallory"
	changed.Kind = principal.KindService

	if err := s.UpdatePrincipal(t.Context(), changed); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.GetPrincipal(t.Context(), p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	want := p
	want.DisplayName = "Alice A."
	want.Disabled = true
	want.UpdatedAt = at(5)
	equalPrincipal(t, got, want)
}

func testUpdateMissingPrincipal(t *testing.T, s principal.Store) {
	t.Helper()

	err := s.UpdatePrincipal(t.Context(), newPrincipal("prn-missing", "alice", principal.KindPerson))
	wantErr(t, "update a missing principal", err, principal.ErrNotFound)
}

func testDeletePrincipal(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreatePrincipal(t, s, newPrincipal("prn-2", "bob", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))
	mustAddMember(t, s, "grp-1", "prn-1")
	mustAddMember(t, s, "grp-1", "prn-2")

	if err := s.DeletePrincipal(t.Context(), "prn-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := s.GetPrincipal(t.Context(), "prn-1")
	wantErr(t, "get a deleted principal", err, principal.ErrNotFound)

	members, err := s.ListMembers(t.Context(), "grp-1")
	if err != nil {
		t.Fatalf("list members: %v", err)
	}

	equalStrings(t, "members after the delete", memberSubjects(members), []string{"bob"})

	// The subject is free again after the delete.
	mustCreatePrincipal(t, s, newPrincipal("prn-3", "alice", principal.KindPerson))
}

func testDeleteMissingPrincipal(t *testing.T, s principal.Store) {
	t.Helper()

	err := s.DeletePrincipal(t.Context(), "prn-missing")
	wantErr(t, "delete a missing principal", err, principal.ErrNotFound)
}

func testGroupRoundTrip(t *testing.T, s principal.Store) {
	t.Helper()

	want := newGroup("grp-1", "Platform_Team")
	mustCreateGroup(t, s, want)

	got, err := s.GetGroup(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	equalGroup(t, got, want)
}

func testDuplicateGroupName(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreateGroup(t, s, newGroup("grp-1", "ops"))

	err := s.CreateGroup(t.Context(), newGroup("grp-2", "OPS"))
	wantErr(t, "second group with the name in another case", err, principal.ErrAlreadyExists)

	err = s.CreateGroup(t.Context(), newGroup("grp-1", "dev"))
	wantErr(t, "second group with the ID", err, principal.ErrAlreadyExists)
}

func testMissingGroup(t *testing.T, s principal.Store) {
	t.Helper()

	_, err := s.GetGroup(t.Context(), "grp-missing")
	wantErr(t, "get a missing group", err, principal.ErrNotFound)
}

func testListGroups(t *testing.T, s principal.Store) {
	t.Helper()

	got, err := s.ListGroups(t.Context())
	if err != nil {
		t.Fatalf("list an empty store: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("empty store lists %d groups", len(got))
	}

	mustCreateGroup(t, s, newGroup("grp-1", "ops"))
	mustCreateGroup(t, s, newGroup("grp-2", "Billing"))
	mustCreateGroup(t, s, newGroup("grp-3", "audit"))

	got, err = s.ListGroups(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	equalStrings(t, "names", groupNames(got), []string{"audit", "Billing", "ops"})
}

func testUpdateGroup(t *testing.T, s principal.Store) {
	t.Helper()

	g := newGroup("grp-1", "ops")
	mustCreateGroup(t, s, g)

	changed := g
	changed.Name = "operations"
	changed.Description = "Runs the platform"
	changed.UpdatedAt = at(7)

	if err := s.UpdateGroup(t.Context(), changed); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.GetGroup(t.Context(), g.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	equalGroup(t, got, changed)

	// The old name is free again after the rename.
	mustCreateGroup(t, s, newGroup("grp-2", "ops"))
}

func testRenameGroupTaken(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreateGroup(t, s, newGroup("grp-1", "ops"))

	g := newGroup("grp-2", "dev")
	mustCreateGroup(t, s, g)

	g.Name = "Ops"
	err := s.UpdateGroup(t.Context(), g)
	wantErr(t, "rename to a taken name", err, principal.ErrAlreadyExists)

	got, err := s.GetGroup(t.Context(), "grp-2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Name != "dev" {
		t.Fatalf("name after a refused rename = %q, want dev", got.Name)
	}
}

func testRenameGroupCase(t *testing.T, s principal.Store) {
	t.Helper()

	g := newGroup("grp-1", "ops")
	mustCreateGroup(t, s, g)

	g.Name = "Ops"
	if err := s.UpdateGroup(t.Context(), g); err != nil {
		t.Fatalf("rename to another case of the same name: %v", err)
	}

	got, err := s.GetGroup(t.Context(), "grp-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Name != "Ops" {
		t.Fatalf("name = %q, want Ops", got.Name)
	}
}

func testUpdateMissingGroup(t *testing.T, s principal.Store) {
	t.Helper()

	err := s.UpdateGroup(t.Context(), newGroup("grp-missing", "ops"))
	wantErr(t, "update a missing group", err, principal.ErrNotFound)
}

func testDeleteGroup(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))
	mustCreateGroup(t, s, newGroup("grp-2", "dev"))
	mustAddMember(t, s, "grp-1", "prn-1")
	mustAddMember(t, s, "grp-2", "prn-1")

	if err := s.DeleteGroup(t.Context(), "grp-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := s.GetGroup(t.Context(), "grp-1")
	wantErr(t, "get a deleted group", err, principal.ErrNotFound)

	groups, err := s.ListGroupsOf(t.Context(), "prn-1")
	if err != nil {
		t.Fatalf("list groups of: %v", err)
	}

	equalStrings(t, "groups after the delete", groupNames(groups), []string{"dev"})

	if _, err := s.GetPrincipal(t.Context(), "prn-1"); err != nil {
		t.Fatalf("a group delete must keep the principal: %v", err)
	}
}

func testDeleteMissingGroup(t *testing.T, s principal.Store) {
	t.Helper()

	err := s.DeleteGroup(t.Context(), "grp-missing")
	wantErr(t, "delete a missing group", err, principal.ErrNotFound)
}

func testMemberRoundTrip(t *testing.T, s principal.Store) {
	t.Helper()

	p := newPrincipal("prn-1", "bob", principal.KindPerson)
	mustCreatePrincipal(t, s, p)
	mustCreatePrincipal(t, s, newPrincipal("prn-2", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))
	mustAddMember(t, s, "grp-1", "prn-1")
	mustAddMember(t, s, "grp-1", "prn-2")

	members, err := s.ListMembers(t.Context(), "grp-1")
	if err != nil {
		t.Fatalf("list members: %v", err)
	}

	equalStrings(t, "members", memberSubjects(members), []string{"alice", "bob"})

	if !members[1].AddedAt.Equal(at(1)) {
		t.Fatalf("added at = %v, want %v", members[1].AddedAt, at(1))
	}

	equalPrincipal(t, members[1].Principal, p)

	if removeErr := s.RemoveMember(t.Context(), "grp-1", "prn-1"); removeErr != nil {
		t.Fatalf("remove: %v", removeErr)
	}

	members, err = s.ListMembers(t.Context(), "grp-1")
	if err != nil {
		t.Fatalf("list members after the remove: %v", err)
	}

	equalStrings(t, "members after the remove", memberSubjects(members), []string{"alice"})
}

func testManyGroups(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))
	mustCreateGroup(t, s, newGroup("grp-2", "Dev"))
	mustCreateGroup(t, s, newGroup("grp-3", "billing"))
	mustAddMember(t, s, "grp-1", "prn-1")
	mustAddMember(t, s, "grp-2", "prn-1")

	groups, err := s.ListGroupsOf(t.Context(), "prn-1")
	if err != nil {
		t.Fatalf("list groups of: %v", err)
	}

	equalStrings(t, "groups", groupNames(groups), []string{"Dev", "ops"})
}

func testDuplicateMember(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))
	mustAddMember(t, s, "grp-1", "prn-1")

	err := s.AddMember(t.Context(), principal.Membership{AddedAt: at(2), GroupID: "grp-1", PrincipalID: "prn-1"})
	wantErr(t, "add a member twice", err, principal.ErrAlreadyExists)
}

func testMemberMissing(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))

	err := s.AddMember(t.Context(), principal.Membership{AddedAt: at(1), GroupID: "grp-missing", PrincipalID: "prn-1"})
	wantErr(t, "add to a missing group", err, principal.ErrNotFound)

	err = s.AddMember(t.Context(), principal.Membership{AddedAt: at(1), GroupID: "grp-1", PrincipalID: "prn-missing"})
	wantErr(t, "add a missing principal", err, principal.ErrNotFound)
}

func testRemoveMissingMember(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))

	err := s.RemoveMember(t.Context(), "grp-1", "prn-1")
	wantErr(t, "remove a principal that is not a member", err, principal.ErrNotFound)
}

func testListMissing(t *testing.T, s principal.Store) {
	t.Helper()

	_, err := s.ListMembers(t.Context(), "grp-missing")
	wantErr(t, "list members of a missing group", err, principal.ErrNotFound)

	_, err = s.ListGroupsOf(t.Context(), "prn-missing")
	wantErr(t, "list groups of a missing principal", err, principal.ErrNotFound)

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))

	members, err := s.ListMembers(t.Context(), "grp-1")
	if err != nil || len(members) != 0 {
		t.Fatalf("list members of an empty group = %v, %v; want none", members, err)
	}

	groups, err := s.ListGroupsOf(t.Context(), "prn-1")
	if err != nil || len(groups) != 0 {
		t.Fatalf("list groups of a principal in no group = %v, %v; want none", groups, err)
	}
}

func testEveryKindIsMember(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreateGroup(t, s, newGroup("grp-1", "deployers"))

	for i, kind := range []principal.Kind{principal.KindPerson, principal.KindService, principal.KindWorkload} {
		id := "prn-" + string(kind)
		mustCreatePrincipal(t, s, newPrincipal(id, string(rune('a'+i))+"-"+string(kind), kind))
		mustAddMember(t, s, "grp-1", id)
	}

	members, err := s.ListMembers(t.Context(), "grp-1")
	if err != nil {
		t.Fatalf("list members: %v", err)
	}

	equalStrings(t, "members", memberSubjects(members), []string{"a-person", "b-service", "c-workload"})
}
