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
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
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
		{name: "list principals of one kind pages with a prefix", test: testListPrincipalsOfKind},
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
		{name: "a group records the identity-provider switch", test: testGroupIdentityProviderSwitch},
		{name: "delete group removes its memberships", test: testDeleteGroup},
		{name: "delete of a missing group is ErrNotFound", test: testDeleteMissingGroup},
		{name: "member round trip", test: testMemberRoundTrip},
		{name: "a principal can be in more than one group", test: testManyGroups},
		{name: "list active group names of a principal", test: testActiveGroupNames},
		{name: "duplicate membership is ErrAlreadyExists", test: testDuplicateMember},
		{name: "membership needs the group and the principal", test: testMemberMissing},
		{name: "remove of a missing membership is ErrNotFound", test: testRemoveMissingMember},
		{name: "list of a missing group or principal is ErrNotFound", test: testListMissing},
		{name: "every kind of principal can be a member", test: testEveryKindIsMember},
		{name: "a deleted principal has no groups and frees its subject", test: testDeletedPrincipalHistory},
		{name: "a principal can join a group again after a removal", test: testRejoin},
		{name: "list principals pages by subject with a prefix", test: testPagePrincipals},
		{name: "list groups pages by name without regard to case", test: testPageGroups},
		{name: "list members and groups of a principal page", test: testPageMembers},
		{name: "a change appends its audit entry in @server", test: testChangeAppendsEntry},
		{name: "an entry that the audit log refuses changes nothing", test: testRefusedEntryChangesNothing},
		{name: "concurrent creates of one subject or group name: one wins", test: testConcurrentCreates},
		{name: "lists answer totals and records answer their counts", test: testTotalsAndCounts},
		{name: "a group of a directory refuses membership changes", test: testDirectoryGroup},
		{name: "membership source and state round trip", test: testMembershipSourceState},
		{name: "a local add defaults to local and active", test: testLocalMembershipDefaults},
		{name: "a membership with a bad source or state is ErrInvalid", test: testInvalidMembership},
		{name: "a break-glass person round trips", test: testBreakGlassRoundTrip},
		{name: "a break-glass account must be a person", test: testBreakGlassKind},
		{name: "the last enabled break-glass account stays", test: testLastBreakGlassRefused},
		{name: "a disabled or ended break-glass account does not count", test: testBreakGlassDisabledOrEndedDoNotCount},
		{name: "the break-glass rule leaves other persons alone", test: testBreakGlassRuleIsForBreakGlassOnly},
		{name: "two disables of the last two break-glass accounts leave one", test: testBreakGlassParallel},
		{
			name: "a disable and a delete of the last two break-glass accounts leave one",
			test: testBreakGlassParallelDelete,
		},
		{
			name: "a break-glass flag on a person with a local password fails with ErrBreakGlassPassword",
			test: testBreakGlassPasswordRefused,
		},
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

// entry returns a valid audit entry of the kind change in the scope
// @server, as a Service builds it.
func entry() audit.Entry {
	return audit.Entry{
		At:            at(9),
		Namespace:     audit.ServerScope,
		Subject:       "admin",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "server:manage-principals",
		Object:        "principal:prn-test",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "principaltest",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// refused returns an entry that fails validation: it names no subject.
func refused() audit.Entry {
	e := entry()
	e.Subject = ""

	return e
}

// all is a query for every row.
func all() principal.Query {
	return principal.Query{After: "", Low: "", High: "", Limit: principal.MaxLimit + 1}
}

func newPrincipal(id, subject string, kind principal.Kind) principal.Principal {
	return principal.Principal{
		CreatedAt:   at(0),
		UpdatedAt:   at(0),
		DisabledAt:  time.Time{},
		ID:          id,
		Subject:     subject,
		DisplayName: "Name of " + subject,
		Kind:        kind,
		Source:      principal.SourceLocal,
		Namespace:   "", GroupCount: 0, BreakGlass: false,
	}
}

func newGroup(id, name string) principal.Group {
	return principal.Group{
		CreatedAt:                at(0),
		UpdatedAt:                at(0),
		ID:                       id,
		Name:                     name,
		Description:              "The " + name + " group",
		Source:                   principal.SourceLocal,
		MemberCount:              0,
		ClosedToIdentityProvider: false,
	}
}

func mustCreatePrincipal(t *testing.T, s principal.Store, p principal.Principal) {
	t.Helper()

	if err := s.CreatePrincipal(t.Context(), p, entry()); err != nil {
		t.Fatalf("create principal %q: %v", p.Subject, err)
	}
}

func mustCreateGroup(t *testing.T, s principal.Store, g principal.Group) {
	t.Helper()

	if err := s.CreateGroup(t.Context(), g, entry()); err != nil {
		t.Fatalf("create group %q: %v", g.Name, err)
	}
}

func mustGroup(t *testing.T, s principal.Store, id string) principal.Group {
	t.Helper()

	g, err := s.GetGroup(t.Context(), id)
	if err != nil {
		t.Fatalf("get the group %q: %v", id, err)
	}

	return g
}

func mustUpdateGroup(t *testing.T, s principal.Store, g principal.Group) {
	t.Helper()

	if err := s.UpdateGroup(t.Context(), g, entry()); err != nil {
		t.Fatalf("update the group %q: %v", g.Name, err)
	}
}

func mustDeleteGroup(t *testing.T, s principal.Store, id string) {
	t.Helper()

	if err := s.DeleteGroup(t.Context(), id, entry()); err != nil {
		t.Fatalf("delete the group %q: %v", id, err)
	}
}

func mustAddMember(t *testing.T, s principal.Store, groupID, principalID string) {
	t.Helper()

	m := principal.Membership{
		AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: groupID, PrincipalID: principalID,
		Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive, Pinned: false,
	}
	if err := s.AddMember(t.Context(), m, entry(), nil); err != nil {
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

	// The count is read-only: testTotalsAndCounts checks it.
	got.CreatedAt, got.UpdatedAt, got.GroupCount = want.CreatedAt, want.UpdatedAt, want.GroupCount
	if got != want {
		t.Fatalf("principal = %+v, want %+v", got, want)
	}
}

func equalGroup(t *testing.T, got, want principal.Group) {
	t.Helper()

	if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("times = %v/%v, want %v/%v", got.CreatedAt, got.UpdatedAt, want.CreatedAt, want.UpdatedAt)
	}

	got.CreatedAt, got.UpdatedAt, got.MemberCount = want.CreatedAt, want.UpdatedAt, want.MemberCount
	if got != want {
		t.Fatalf("group = %+v, want %+v", got, want)
	}
}

func subjects(ps []principal.Principal) []string {
	out := make([]string, 0, len(ps))
	for i := range ps {
		out = append(out, ps[i].Subject)
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
	for i := range gs {
		out = append(out, gs[i].Name)
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

	err := s.CreatePrincipal(t.Context(), newPrincipal("prn-2", "alice", principal.KindService), entry())
	wantErr(t, "second principal with the subject", err, principal.ErrAlreadyExists)
}

func testDuplicatePrincipalID(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))

	err := s.CreatePrincipal(t.Context(), newPrincipal("prn-1", "bob", principal.KindPerson), entry())
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

	got, _, err := s.ListPrincipals(t.Context(), all(), "")
	if err != nil {
		t.Fatalf("list an empty store: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("empty store lists %d principals", len(got))
	}

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "carol", principal.KindPerson))
	mustCreatePrincipal(t, s, newPrincipal("prn-2", "alice", principal.KindPerson))
	mustCreatePrincipal(t, s, newPrincipal("prn-3", "bob", principal.KindWorkload))

	got, _, err = s.ListPrincipals(t.Context(), all(), "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	equalStrings(t, "subjects", subjects(got), []string{"alice", "bob", "carol"})
}

func testListPrincipalsOfKind(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "ci-build", principal.KindService))
	mustCreatePrincipal(t, s, newPrincipal("prn-2", "alice", principal.KindPerson))
	mustCreatePrincipal(t, s, newPrincipal("prn-3", "ci-deploy", principal.KindService))
	mustCreatePrincipal(t, s, newPrincipal("prn-4", "ci-runner", principal.KindWorkload))
	mustCreatePrincipal(t, s, newPrincipal("prn-5", "bob", principal.KindService))

	if err := s.DeletePrincipal(t.Context(), "prn-5", entry()); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	for _, tc := range []struct {
		name  string
		kind  principal.Kind
		q     principal.Query
		want  []string
		total int64
	}{
		{"services", principal.KindService, all(), []string{"ci-build", "ci-deploy"}, 2},
		{
			"services after a key", principal.KindService,
			principal.Query{After: "ci-build", Low: "", High: "", Limit: 10},
			[]string{"ci-deploy"},
			2,
		},
		{
			"workloads with a prefix", principal.KindWorkload,
			principal.Query{After: "", Low: "ci-", High: "ci.", Limit: 10},
			[]string{"ci-runner"},
			1,
		},
		{
			"persons with a prefix", principal.KindPerson,
			principal.Query{After: "", Low: "ci-", High: "ci.", Limit: 10},
			nil, 0,
		},
		{
			"every kind with a prefix", "",
			principal.Query{After: "", Low: "ci-", High: "ci.", Limit: 2},
			[]string{"ci-build", "ci-deploy"},
			3,
		},
	} {
		got, total, err := s.ListPrincipals(t.Context(), tc.q, tc.kind)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		equalStrings(t, tc.name, subjects(got), tc.want)

		if total != tc.total {
			t.Fatalf("%s total = %d, want %d", tc.name, total, tc.total)
		}
	}
}

func testUpdatePrincipal(t *testing.T, s principal.Store) {
	t.Helper()

	p := newPrincipal("prn-1", "alice", principal.KindPerson)
	mustCreatePrincipal(t, s, p)

	changed := p
	changed.DisplayName = "Alice A."
	changed.UpdatedAt = at(5)
	changed.Subject = "mallory"
	changed.Kind = principal.KindService

	if err := s.UpdatePrincipal(t.Context(), changed, entry()); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.GetPrincipal(t.Context(), p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	want := p
	want.DisplayName = "Alice A."
	want.UpdatedAt = at(5)
	equalPrincipal(t, got, want)
}

func testUpdateMissingPrincipal(t *testing.T, s principal.Store) {
	t.Helper()

	err := s.UpdatePrincipal(t.Context(), newPrincipal("prn-missing", "alice", principal.KindPerson), entry())
	wantErr(t, "update a missing principal", err, principal.ErrNotFound)
}

func testDeletePrincipal(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreatePrincipal(t, s, newPrincipal("prn-2", "bob", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))
	mustAddMember(t, s, "grp-1", "prn-1")
	mustAddMember(t, s, "grp-1", "prn-2")

	if err := s.DeletePrincipal(t.Context(), "prn-1", entry()); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := s.GetPrincipal(t.Context(), "prn-1")
	wantErr(t, "get a deleted principal", err, principal.ErrNotFound)

	members, _, err := s.ListMembers(t.Context(), "grp-1", all())
	if err != nil {
		t.Fatalf("list members: %v", err)
	}

	equalStrings(t, "members after the delete", memberSubjects(members), []string{"bob"})

	// The subject is free again after the delete.
	mustCreatePrincipal(t, s, newPrincipal("prn-3", "alice", principal.KindPerson))
}

func testDeleteMissingPrincipal(t *testing.T, s principal.Store) {
	t.Helper()

	err := s.DeletePrincipal(t.Context(), "prn-missing", entry())
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

	err := s.CreateGroup(t.Context(), newGroup("grp-2", "OPS"), entry())
	wantErr(t, "second group with the name in another case", err, principal.ErrAlreadyExists)

	err = s.CreateGroup(t.Context(), newGroup("grp-1", "dev"), entry())
	wantErr(t, "second group with the ID", err, principal.ErrAlreadyExists)
}

func testMissingGroup(t *testing.T, s principal.Store) {
	t.Helper()

	_, err := s.GetGroup(t.Context(), "grp-missing")
	wantErr(t, "get a missing group", err, principal.ErrNotFound)
}

func testListGroups(t *testing.T, s principal.Store) {
	t.Helper()

	got, _, err := s.ListGroups(t.Context(), all())
	if err != nil {
		t.Fatalf("list an empty store: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("empty store lists %d groups", len(got))
	}

	mustCreateGroup(t, s, newGroup("grp-1", "ops"))
	mustCreateGroup(t, s, newGroup("grp-2", "Billing"))
	mustCreateGroup(t, s, newGroup("grp-3", "audit"))

	got, _, err = s.ListGroups(t.Context(), all())
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

	if err := s.UpdateGroup(t.Context(), changed, entry()); err != nil {
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
	err := s.UpdateGroup(t.Context(), g, entry())
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
	if err := s.UpdateGroup(t.Context(), g, entry()); err != nil {
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

	err := s.UpdateGroup(t.Context(), newGroup("grp-missing", "ops"), entry())
	wantErr(t, "update a missing group", err, principal.ErrNotFound)
}

// testGroupIdentityProviderSwitch proves that the switch that closes a group
// to the identity provider round trips: a new group is open, a create stores
// the switch, a change closes it, a rename keeps it and a change opens it
// again. A change of a deleted or an unknown group is ErrNotFound.
func testGroupIdentityProviderSwitch(t *testing.T, s principal.Store) {
	t.Helper()

	g := newGroup("grp-1", "ops")
	mustCreateGroup(t, s, g)

	if mustGroup(t, s, g.ID).ClosedToIdentityProvider {
		t.Fatal("a new group is closed to the identity provider")
	}

	closedAtCreate := newGroup("grp-2", "engineering")
	closedAtCreate.ClosedToIdentityProvider = true
	mustCreateGroup(t, s, closedAtCreate)
	equalGroup(t, mustGroup(t, s, closedAtCreate.ID), closedAtCreate)

	closed := g
	closed.ClosedToIdentityProvider = true
	closed.UpdatedAt = at(7)

	renamed := closed
	renamed.Name = "operations"
	renamed.UpdatedAt = at(8)

	open := renamed
	open.ClosedToIdentityProvider = false
	open.UpdatedAt = at(9)

	again := open
	again.UpdatedAt = at(10)

	// Each change is stored, and the rename between them keeps the switch.
	steps := []principal.Group{closed, renamed, open, again}
	for i := range steps {
		mustUpdateGroup(t, s, steps[i])
		equalGroup(t, mustGroup(t, s, steps[i].ID), steps[i])
	}

	missing := newGroup("grp-missing", "dev")
	missing.ClosedToIdentityProvider = true

	err := s.UpdateGroup(t.Context(), missing, entry())
	wantErr(t, "update an unknown group", err, principal.ErrNotFound)

	mustDeleteGroup(t, s, g.ID)

	deleted := open
	deleted.UpdatedAt = at(11)

	err = s.UpdateGroup(t.Context(), deleted, entry())
	wantErr(t, "update a deleted group", err, principal.ErrNotFound)
}

func testDeleteGroup(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))
	mustCreateGroup(t, s, newGroup("grp-2", "dev"))
	mustAddMember(t, s, "grp-1", "prn-1")
	mustAddMember(t, s, "grp-2", "prn-1")

	if err := s.DeleteGroup(t.Context(), "grp-1", entry()); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := s.GetGroup(t.Context(), "grp-1")
	wantErr(t, "get a deleted group", err, principal.ErrNotFound)

	groups, _, err := s.ListGroupsOf(t.Context(), "prn-1", all())
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

	err := s.DeleteGroup(t.Context(), "grp-missing", entry())
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

	members, _, err := s.ListMembers(t.Context(), "grp-1", all())
	if err != nil {
		t.Fatalf("list members: %v", err)
	}

	equalStrings(t, "members", memberSubjects(members), []string{"alice", "bob"})

	if len(members) != 2 {
		t.Fatalf("members = %d, want 2", len(members))
	}

	if !members[1].AddedAt.Equal(at(1)) {
		t.Fatalf("added at = %v, want %v", members[1].AddedAt, at(1))
	}

	equalPrincipal(t, members[1].Principal, p)

	if removeErr := s.RemoveMember(t.Context(), "grp-1", "prn-1", entry()); removeErr != nil {
		t.Fatalf("remove: %v", removeErr)
	}

	members, _, err = s.ListMembers(t.Context(), "grp-1", all())
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

	groups, _, err := s.ListGroupsOf(t.Context(), "prn-1", all())
	if err != nil {
		t.Fatalf("list groups of: %v", err)
	}

	equalStrings(t, "groups", groupNames(groups), []string{"Dev", "ops"})
}

func testActiveGroupNames(t *testing.T, s principal.Store) {
	t.Helper()

	ctx := t.Context()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-ops", "Ops"))
	mustCreateGroup(t, s, newGroup("grp-alpha", "alpha"))
	mustCreateGroup(t, s, newGroup("grp-suspended", "Suspended"))
	mustCreateGroup(t, s, newGroup("grp-removed", "Removed"))
	mustCreateGroup(t, s, newGroup("grp-deleted", "Deleted"))

	mustAddMember(t, s, "grp-ops", "prn-1")
	mustAddMember(t, s, "grp-alpha", "prn-1")

	suspended := principal.Membership{
		AddedAt: at(2), SuspendedAt: at(3), GroupID: "grp-suspended", PrincipalID: "prn-1",
		Source: principal.MembershipSourceLocal, SourceID: "",
		State: principal.MembershipStateSuspended, Pinned: false,
	}
	if err := s.AddMember(ctx, suspended, entry(), nil); err != nil {
		t.Fatalf("add a suspended member: %v", err)
	}

	mustAddMember(t, s, "grp-removed", "prn-1")

	if err := s.RemoveMember(ctx, "grp-removed", "prn-1", entry()); err != nil {
		t.Fatalf("remove the member: %v", err)
	}

	mustAddMember(t, s, "grp-deleted", "prn-1")

	if err := s.DeleteGroup(ctx, "grp-deleted", entry()); err != nil {
		t.Fatalf("delete the group: %v", err)
	}

	cases := []struct {
		name        string
		principalID string
		want        []string
		wantErr     error
	}{
		{
			"active memberships in name order without regard to case", "prn-1",
			[]string{"alpha", "Ops"},
			nil,
		},
		{"a missing principal", "prn-missing", nil, principal.ErrNotFound},
	}

	for _, tc := range cases {
		got, err := s.ListActiveGroupNamesOf(ctx, tc.principalID)
		if tc.wantErr != nil {
			wantErr(t, tc.name, err, tc.wantErr)

			continue
		}

		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		equalStrings(t, tc.name, got, tc.want)
	}

	if err := s.DeletePrincipal(ctx, "prn-1", entry()); err != nil {
		t.Fatalf("delete the principal: %v", err)
	}

	_, err := s.ListActiveGroupNamesOf(ctx, "prn-1")
	wantErr(t, "a deleted principal", err, principal.ErrNotFound)
}

func testDuplicateMember(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))
	mustAddMember(t, s, "grp-1", "prn-1")

	err := s.AddMember(
		t.Context(),
		principal.Membership{
			AddedAt: at(2), SuspendedAt: time.Time{}, GroupID: "grp-1", PrincipalID: "prn-1",
			Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive,
			Pinned: false,
		},
		entry(),
		nil,
	)
	wantErr(t, "add a member twice", err, principal.ErrAlreadyExists)
}

func testMemberMissing(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))

	err := s.AddMember(
		t.Context(),
		principal.Membership{
			AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: "grp-missing", PrincipalID: "prn-1",
			Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive,
			Pinned: false,
		},
		entry(),
		nil,
	)
	wantErr(t, "add to a missing group", err, principal.ErrNotFound)

	err = s.AddMember(
		t.Context(),
		principal.Membership{
			AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: "grp-1", PrincipalID: "prn-missing",
			Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive,
			Pinned: false,
		},
		entry(),
		nil,
	)
	wantErr(t, "add a missing principal", err, principal.ErrNotFound)
}

func testRemoveMissingMember(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))

	err := s.RemoveMember(t.Context(), "grp-1", "prn-1", entry())
	wantErr(t, "remove a principal that is not a member", err, principal.ErrNotFound)
}

func testListMissing(t *testing.T, s principal.Store) {
	t.Helper()

	_, _, err := s.ListMembers(t.Context(), "grp-missing", all())
	wantErr(t, "list members of a missing group", err, principal.ErrNotFound)

	_, _, err = s.ListGroupsOf(t.Context(), "prn-missing", all())
	wantErr(t, "list groups of a missing principal", err, principal.ErrNotFound)

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))

	members, _, err := s.ListMembers(t.Context(), "grp-1", all())
	if err != nil || len(members) != 0 {
		t.Fatalf("list members of an empty group = %v, %v; want none", members, err)
	}

	groups, _, err := s.ListGroupsOf(t.Context(), "prn-1", all())
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

	members, _, err := s.ListMembers(t.Context(), "grp-1", all())
	if err != nil {
		t.Fatalf("list members: %v", err)
	}

	equalStrings(t, "members", memberSubjects(members), []string{"a-person", "b-service", "c-workload"})
}

func testDeletedPrincipalHistory(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))
	mustAddMember(t, s, "grp-1", "prn-1")

	if err := s.DeletePrincipal(t.Context(), "prn-1", entry()); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, _, err := s.ListGroupsOf(t.Context(), "prn-1", all())
	wantErr(t, "list groups of a deleted principal", err, principal.ErrNotFound)

	_, err = s.GetPrincipalBySubject(t.Context(), "alice")
	wantErr(t, "get a deleted principal by subject", err, principal.ErrNotFound)

	err = s.DeletePrincipal(t.Context(), "prn-1", entry())
	wantErr(t, "delete a deleted principal", err, principal.ErrNotFound)

	err = s.AddMember(
		t.Context(),
		principal.Membership{
			AddedAt: at(3), SuspendedAt: time.Time{}, GroupID: "grp-1", PrincipalID: "prn-1",
			Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive,
			Pinned: false,
		},
		entry(),
		nil,
	)
	wantErr(t, "add a deleted principal", err, principal.ErrNotFound)

	mustCreateGroup(t, s, newGroup("grp-2", "dev"))

	if err := s.DeleteGroup(t.Context(), "grp-2", entry()); err != nil {
		t.Fatalf("delete group: %v", err)
	}

	// The name of a deleted group is free again.
	mustCreateGroup(t, s, newGroup("grp-3", "DEV"))
}

func testRejoin(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))
	mustAddMember(t, s, "grp-1", "prn-1")

	if err := s.RemoveMember(t.Context(), "grp-1", "prn-1", entry()); err != nil {
		t.Fatalf("remove: %v", err)
	}

	err := s.RemoveMember(t.Context(), "grp-1", "prn-1", entry())
	wantErr(t, "remove an ended membership", err, principal.ErrNotFound)

	m := principal.Membership{
		AddedAt: at(4), SuspendedAt: time.Time{}, GroupID: "grp-1", PrincipalID: "prn-1",
		Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive, Pinned: false,
	}
	if err = s.AddMember(t.Context(), m, entry(), nil); err != nil {
		t.Fatalf("join again: %v", err)
	}

	members, _, err := s.ListMembers(t.Context(), "grp-1", all())
	if err != nil {
		t.Fatalf("list members: %v", err)
	}

	if len(members) != 1 || !members[0].AddedAt.Equal(at(4)) {
		t.Fatalf("members = %+v, want alice once, added at %v", members, at(4))
	}
}

func testPagePrincipals(t *testing.T, s principal.Store) {
	t.Helper()

	for i, subject := range []string{"b1", "a3", "a1", "ab", "a2", "A1"} {
		mustCreatePrincipal(t, s, newPrincipal(fmt.Sprintf("prn-%d", i), subject, principal.KindPerson))
	}

	ctx := t.Context()

	cases := []struct {
		name string
		q    principal.Query
		want []string
	}{
		{"first page", principal.Query{After: "", Low: "", High: "", Limit: 2}, []string{"A1", "a1"}},
		{"after a key", principal.Query{After: "a1", Low: "", High: "", Limit: 3}, []string{"a2", "a3", "ab"}},
		{"a prefix", principal.Query{After: "", Low: "a", High: "b", Limit: 10}, []string{"a1", "a2", "a3", "ab"}},
		{"a prefix after a key", principal.Query{After: "a2", Low: "a", High: "b", Limit: 10}, []string{"a3", "ab"}},
		{"an exact prefix", principal.Query{After: "", Low: "A", High: "B", Limit: 10}, []string{"A1"}},
	}

	for _, tc := range cases {
		got, _, err := s.ListPrincipals(ctx, tc.q, "")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		equalStrings(t, tc.name, subjects(got), tc.want)
	}
}

func testPageGroups(t *testing.T, s principal.Store) {
	t.Helper()

	for i, name := range []string{"Ops", "ops-2", "dev", "OPERATIONS"} {
		mustCreateGroup(t, s, newGroup(fmt.Sprintf("grp-%d", i), name))
	}

	got, _, err := s.ListGroups(t.Context(), principal.Query{After: "", Low: "op", High: "oq", Limit: 2})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	equalStrings(t, "first page of op", groupNames(got), []string{"OPERATIONS", "Ops"})

	got, _, err = s.ListGroups(t.Context(), principal.Query{After: "ops", Low: "op", High: "oq", Limit: 2})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	equalStrings(t, "page after ops", groupNames(got), []string{"ops-2"})
}

func testPageMembers(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreateGroup(t, s, newGroup("grp-a", "alpha"))
	mustCreateGroup(t, s, newGroup("grp-b", "Beta"))
	mustCreateGroup(t, s, newGroup("grp-c", "beacon"))

	for i, subject := range []string{"carol", "alice", "bob"} {
		id := fmt.Sprintf("prn-%d", i)
		mustCreatePrincipal(t, s, newPrincipal(id, subject, principal.KindPerson))
		mustAddMember(t, s, "grp-a", id)
	}

	for _, g := range []string{"grp-b", "grp-c"} {
		mustAddMember(t, s, g, "prn-1")
	}

	members, _, err := s.ListMembers(t.Context(), "grp-a", principal.Query{After: "alice", Low: "", High: "", Limit: 1})
	if err != nil {
		t.Fatalf("list members: %v", err)
	}

	equalStrings(t, "members after alice", memberSubjects(members), []string{"bob"})

	groups, _, err := s.ListGroupsOf(t.Context(), "prn-1", principal.Query{After: "", Low: "be", High: "bf", Limit: 10})
	if err != nil {
		t.Fatalf("list groups of: %v", err)
	}

	equalStrings(t, "groups of alice with be", groupNames(groups), []string{"beacon", "Beta"})
}

// auditLister is the read side of the audit log, when the store keeps it.
type auditLister interface {
	List(ctx context.Context, ns namespace.Name, page audit.Page) (audit.Listing, error)
}

func testChangeAppendsEntry(t *testing.T, s principal.Store) {
	t.Helper()

	entries, ok := s.(auditLister)
	if !ok {
		t.Skip("the store keeps no audit log to read")
	}

	e := entry()
	e.Object = "principal:prn-1"

	if err := s.CreatePrincipal(t.Context(), newPrincipal("prn-1", "alice", principal.KindPerson), e); err != nil {
		t.Fatalf("create: %v", err)
	}

	listing, err := entries.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	if len(listing.Entries) != 1 || listing.Entries[0].Object != "principal:prn-1" || listing.Entries[0].Sequence != 1 {
		t.Fatalf("entries = %+v, want the entry of the create", listing.Entries)
	}
}

func testRefusedEntryChangesNothing(t *testing.T, s principal.Store) {
	t.Helper()

	ctx := t.Context()

	err := s.CreatePrincipal(ctx, newPrincipal("prn-x", "xavier", principal.KindPerson), refused())
	wantErr(t, "create with a refused entry", err, audit.ErrInvalidEntry)

	_, err = s.GetPrincipal(ctx, "prn-x")
	wantErr(t, "principal of a refused create", err, principal.ErrNotFound)

	err = s.CreateGroup(ctx, newGroup("grp-x", "x"), refused())
	wantErr(t, "group create with a refused entry", err, audit.ErrInvalidEntry)

	_, err = s.GetGroup(ctx, "grp-x")
	wantErr(t, "group of a refused create", err, principal.ErrNotFound)

	p := newPrincipal("prn-1", "alice", principal.KindPerson)
	mustCreatePrincipal(t, s, p)
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))
	mustCreateGroup(t, s, newGroup("grp-2", "dev"))
	mustAddMember(t, s, "grp-1", "prn-1")

	changed := p
	changed.DisplayName = "Mallory"
	changed.UpdatedAt = at(8)

	refusals := []struct {
		write func() error
		name  string
	}{
		{func() error { return s.UpdatePrincipal(ctx, changed, refused()) }, "update"},
		{func() error { return s.DeletePrincipal(ctx, "prn-1", refused()) }, "delete principal"},
		{func() error { return s.DeleteGroup(ctx, "grp-1", refused()) }, "delete group"},
		{func() error { return s.RemoveMember(ctx, "grp-1", "prn-1", refused()) }, "remove member"},
		{func() error {
			return s.AddMember(
				ctx,
				principal.Membership{
					AddedAt: at(2), SuspendedAt: time.Time{}, GroupID: "grp-2", PrincipalID: "prn-1",
					Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive,
					Pinned: false,
				},
				refused(),
				nil,
			)
		}, "add member"},
	}

	for _, r := range refusals {
		wantErr(t, r.name+" with a refused entry", r.write(), audit.ErrInvalidEntry)
	}

	got, err := s.GetPrincipal(ctx, "prn-1")
	if err != nil {
		t.Fatalf("get after the refusals: %v", err)
	}

	equalPrincipal(t, got, p)

	groups, _, err := s.ListGroupsOf(ctx, "prn-1", all())
	if err != nil {
		t.Fatalf("list groups of: %v", err)
	}

	equalStrings(t, "groups after the refusals", groupNames(groups), []string{"ops"})
}

func testConcurrentCreates(t *testing.T, s principal.Store) {
	t.Helper()

	const racers = 8

	ctx := t.Context()

	count := func(create func(i int) error) (int, error) {
		var (
			wg   sync.WaitGroup
			mu   sync.Mutex
			won  int
			fail error
		)

		for i := range racers {
			wg.Go(func() {
				err := create(i)

				mu.Lock()
				defer mu.Unlock()

				switch {
				case err == nil:
					won++
				case !errors.Is(err, principal.ErrAlreadyExists):
					fail = err
				default:
				}
			})
		}

		wg.Wait()

		return won, fail
	}

	won, err := count(func(i int) error {
		return s.CreatePrincipal(ctx, newPrincipal(fmt.Sprintf("prn-%d", i), "alice", principal.KindPerson), entry())
	})
	if err != nil || won != 1 {
		t.Fatalf("concurrent creates of one subject: %d won, error %v; want 1 and nil", won, err)
	}

	won, err = count(func(i int) error {
		name := []string{"ops", "OPS", "Ops", "oPs"}[i%4]

		return s.CreateGroup(ctx, newGroup(fmt.Sprintf("grp-%d", i), name), entry())
	})
	if err != nil || won != 1 {
		t.Fatalf("concurrent creates of one group name: %d won, error %v; want 1 and nil", won, err)
	}
}

func testTotalsAndCounts(t *testing.T, s principal.Store) {
	t.Helper()

	for i, subject := range []string{"alice", "bob", "carol"} {
		mustCreatePrincipal(t, s, newPrincipal(fmt.Sprintf("prn-%d", i), subject, principal.KindPerson))
	}

	mustCreateGroup(t, s, newGroup("grp-1", "ops"))
	mustCreateGroup(t, s, newGroup("grp-2", "dev"))
	mustAddMember(t, s, "grp-1", "prn-0")
	mustAddMember(t, s, "grp-2", "prn-0")
	mustAddMember(t, s, "grp-1", "prn-1")

	checkPrincipalTotals(t, s)
	checkGroupTotals(t, s)
}

// onePerPage asks for the first row of a list.
func onePerPage() principal.Query {
	return principal.Query{After: "", Low: "", High: "", Limit: 1}
}

// checkPrincipalTotals reads the principal lists of testTotalsAndCounts.
func checkPrincipalTotals(t *testing.T, s principal.Store) {
	t.Helper()

	ctx := t.Context()

	ps, total, err := s.ListPrincipals(ctx, onePerPage(), "")
	if err != nil || total != 3 || len(ps) != 1 || ps[0].GroupCount != 2 {
		t.Fatalf("first principal = %+v, total %d, %v; want alice in 2 groups of 3 principals", ps, total, err)
	}

	_, total, err = s.ListPrincipals(ctx, principal.Query{After: "alice", Low: "b", High: "c", Limit: 10}, "")
	if err != nil || total != 1 {
		t.Fatalf("total of prefix b = %d, %v; want 1 with no regard to the cursor", total, err)
	}

	_, total, err = s.ListGroupsOf(ctx, "prn-0", onePerPage())
	if err != nil || total != 2 {
		t.Fatalf("groups of alice: total %d, %v; want 2", total, err)
	}

	p, err := s.GetPrincipal(ctx, "prn-2")
	if err != nil || p.GroupCount != 0 {
		t.Fatalf("principal carol = %+v, %v; want no group", p, err)
	}
}

// checkGroupTotals reads the group lists of testTotalsAndCounts.
func checkGroupTotals(t *testing.T, s principal.Store) {
	t.Helper()

	ctx := t.Context()

	gs, total, err := s.ListGroups(ctx, onePerPage())
	if err != nil || total != 2 || len(gs) != 1 || gs[0].Name != "dev" || gs[0].MemberCount != 1 {
		t.Fatalf("first group = %+v, total %d, %v; want dev with 1 member of 2 groups", gs, total, err)
	}

	ms, total, err := s.ListMembers(ctx, "grp-1", onePerPage())
	if err != nil || total != 2 || len(ms) != 1 || ms[0].Principal.GroupCount != 2 {
		t.Fatalf("first member = %+v, total %d, %v; want alice of 2 members", ms, total, err)
	}

	g, err := s.GetGroup(ctx, "grp-1")
	if err != nil || g.MemberCount != 2 {
		t.Fatalf("group ops = %+v, %v; want 2 members", g, err)
	}
}

func testDirectoryGroup(t *testing.T, s principal.Store) {
	t.Helper()

	ctx := t.Context()

	p := newPrincipal("prn-1", "alice", principal.KindPerson)
	p.Source = principal.SourceSCIM
	mustCreatePrincipal(t, s, p)

	g := newGroup("grp-1", "engineering")
	g.Source = principal.SourceLDAP
	mustCreateGroup(t, s, g)

	got, err := s.GetPrincipal(ctx, "prn-1")
	if err != nil || got.Source != principal.SourceSCIM {
		t.Fatalf("principal source = %+v, %v; want scim", got, err)
	}

	err = s.AddMember(ctx, principal.Membership{
		AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: "grp-1", PrincipalID: "prn-1",
		Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive, Pinned: false,
	}, entry(), nil)
	wantErr(t, "add to a directory group", err, principal.ErrManagedByDirectory)

	if err == nil || !strings.Contains(err.Error(), "ldap") {
		t.Fatalf("error %q does not name the directory", err)
	}

	err = s.RemoveMember(ctx, "grp-1", "prn-1", entry())
	wantErr(t, "remove from a directory group", err, principal.ErrManagedByDirectory)

	groups, _, err := s.ListGroups(ctx, all())
	if err != nil || len(groups) != 1 || groups[0].Source != principal.SourceLDAP {
		t.Fatalf("groups = %+v, %v; want engineering from ldap", groups, err)
	}
}

func testMembershipSourceState(t *testing.T, s principal.Store) {
	t.Helper()

	ctx := t.Context()

	seedSourceStateMembers(t, s)

	members, _, err := s.ListMembers(ctx, "grp-1", all())
	if err != nil {
		t.Fatalf("list members: %v", err)
	}

	if len(members) != 2 {
		t.Fatalf("members = %d, want 2", len(members))
	}

	bySubject := map[string]principal.Member{}
	for i := range members {
		bySubject[members[i].Principal.Subject] = members[i]
	}

	checkSourceStateAlice(t, bySubject["alice"])
	checkSourceStateBob(t, bySubject["bob"])
}

func seedSourceStateMembers(t *testing.T, s principal.Store) {
	t.Helper()

	ctx := t.Context()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreatePrincipal(t, s, newPrincipal("prn-2", "bob", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))

	scim := principal.Membership{
		AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: "grp-1", PrincipalID: "prn-1",
		Source: principal.MembershipSourceSCIM, SourceID: "scim-token-1",
		State: principal.MembershipStateActive, Pinned: false,
	}

	if err := s.AddMember(ctx, scim, entry(), nil); err != nil {
		t.Fatalf("add a membership from scim: %v", err)
	}

	claimed := principal.Membership{
		AddedAt: at(1), SuspendedAt: at(2), GroupID: "grp-1", PrincipalID: "prn-2",
		Source: principal.MembershipSourceClaim, SourceID: "idp-1",
		State: principal.MembershipStateSuspended, Pinned: true,
	}

	if err := s.AddMember(ctx, claimed, entry(), nil); err != nil {
		t.Fatalf("add a suspended membership from a claim: %v", err)
	}
}

func checkSourceStateAlice(t *testing.T, got principal.Member) {
	t.Helper()

	if got.Source != principal.MembershipSourceSCIM {
		t.Fatalf("alice source = %q, want scim", got.Source)
	}

	if got.SourceID != "scim-token-1" {
		t.Fatalf("alice source ID = %q, want scim-token-1", got.SourceID)
	}

	if got.State != principal.MembershipStateActive {
		t.Fatalf("alice state = %q, want active", got.State)
	}

	if !got.SuspendedAt.IsZero() {
		t.Fatalf("alice suspend time = %v, want zero", got.SuspendedAt)
	}

	if got.Pinned {
		t.Fatal("alice pinned = true, want false")
	}

	if !got.AddedAt.Equal(at(1)) || got.GroupID != "grp-1" || got.PrincipalID != "prn-1" {
		t.Fatalf("alice membership = %+v, want added at %v in grp-1", got, at(1))
	}
}

func checkSourceStateBob(t *testing.T, got principal.Member) {
	t.Helper()

	if got.Source != principal.MembershipSourceClaim {
		t.Fatalf("bob source = %q, want claim", got.Source)
	}

	if got.SourceID != "idp-1" {
		t.Fatalf("bob source ID = %q, want idp-1", got.SourceID)
	}

	if got.State != principal.MembershipStateSuspended {
		t.Fatalf("bob state = %q, want suspended", got.State)
	}

	if !got.SuspendedAt.Equal(at(2)) {
		t.Fatalf("bob suspend time = %v, want %v", got.SuspendedAt, at(2))
	}

	if !got.Pinned {
		t.Fatal("bob pinned = false, want true")
	}
}

func testLocalMembershipDefaults(t *testing.T, s principal.Store) {
	t.Helper()

	ctx := t.Context()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))

	m := principal.Membership{
		AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: "grp-1", PrincipalID: "prn-1",
		Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive, Pinned: false,
	}
	if err := s.AddMember(ctx, m, entry(), nil); err != nil {
		t.Fatalf("add a local membership: %v", err)
	}

	checkLocalActive(t, s, "grp-1")

	mustCreatePrincipal(t, s, newPrincipal("prn-2", "bob", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-2", "dev"))

	empty := principal.Membership{
		AddedAt: at(2), SuspendedAt: time.Time{}, GroupID: "grp-2", PrincipalID: "prn-2",
		Source: "", SourceID: "", State: "", Pinned: false,
	}
	if addErr := s.AddMember(ctx, empty, entry(), nil); addErr != nil {
		t.Fatalf("add a membership with no source or state: %v", addErr)
	}

	checkLocalActive(t, s, "grp-2")
}

// checkLocalActive proves that groupID holds one local active member with
// no source ID, no suspend time and no pin.
func checkLocalActive(t *testing.T, s principal.Store, groupID string) {
	t.Helper()

	members, _, err := s.ListMembers(t.Context(), groupID, all())
	if err != nil {
		t.Fatalf("list members: %v", err)
	}

	if len(members) != 1 {
		t.Fatalf("members = %d, want 1", len(members))
	}

	got := members[0]

	if got.Source != principal.MembershipSourceLocal || got.SourceID != "" ||
		got.State != principal.MembershipStateActive || !got.SuspendedAt.IsZero() || got.Pinned {
		t.Fatalf("membership = %+v, want local active with no source ID or suspend time", got)
	}
}

func testBreakGlassRoundTrip(t *testing.T, s principal.Store) {
	t.Helper()

	ctx := t.Context()

	want := newPrincipal("prn-1", "alice@example.com", principal.KindPerson)
	want.BreakGlass = true
	mustCreatePrincipal(t, s, want)

	got, err := s.GetPrincipal(ctx, want.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	equalPrincipal(t, got, want)

	bySubject, err := s.GetPrincipalBySubject(ctx, want.Subject)
	if err != nil {
		t.Fatalf("get by subject: %v", err)
	}

	equalPrincipal(t, bySubject, want)

	rows, _, err := s.ListPrincipals(ctx, all(), "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("list holds %d principals, want 1", len(rows))
	}

	equalPrincipal(t, rows[0], want)

	if err := want.Validate(); err != nil {
		t.Fatalf("validate a break-glass person: %v", err)
	}
}

func testInvalidMembership(t *testing.T, s principal.Store) {
	t.Helper()

	ctx := t.Context()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))

	cases := []struct {
		name string
		m    principal.Membership
	}{
		{
			"unknown source",
			principal.Membership{
				AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: "grp-1", PrincipalID: "prn-1",
				Source: "ldap", SourceID: "", State: principal.MembershipStateActive, Pinned: false,
			},
		},
		{
			"unknown state",
			principal.Membership{
				AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: "grp-1", PrincipalID: "prn-1",
				Source: principal.MembershipSourceLocal, SourceID: "", State: "ended", Pinned: false,
			},
		},
		{
			"local with a source ID",
			principal.Membership{
				AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: "grp-1", PrincipalID: "prn-1",
				Source: principal.MembershipSourceLocal, SourceID: "idp-1",
				State: principal.MembershipStateActive, Pinned: false,
			},
		},
		{
			"claim with no source ID",
			principal.Membership{
				AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: "grp-1", PrincipalID: "prn-1",
				Source: principal.MembershipSourceClaim, SourceID: "",
				State: principal.MembershipStateActive, Pinned: false,
			},
		},
		{
			"active with a suspend time",
			principal.Membership{
				AddedAt: at(1), SuspendedAt: at(2), GroupID: "grp-1", PrincipalID: "prn-1",
				Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive,
				Pinned: false,
			},
		},
		{
			"suspended with no suspend time",
			principal.Membership{
				AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: "grp-1", PrincipalID: "prn-1",
				Source: principal.MembershipSourceSCIM, SourceID: "scim-token-1",
				State: principal.MembershipStateSuspended, Pinned: false,
			},
		},
	}

	for i := range cases {
		err := s.AddMember(ctx, cases[i].m, entry(), nil)
		wantErr(t, cases[i].name, err, principal.ErrInvalid)
	}

	members, _, err := s.ListMembers(ctx, "grp-1", all())
	if err != nil {
		t.Fatalf("list members: %v", err)
	}

	if len(members) != 0 {
		t.Fatalf("members = %d, want none after refused adds", len(members))
	}
}

func testBreakGlassKind(t *testing.T, s principal.Store) {
	t.Helper()

	ctx := t.Context()

	for _, kind := range []principal.Kind{principal.KindService, principal.KindWorkload} {
		p := newPrincipal("prn-"+string(kind), "owner-"+string(kind), kind)
		p.BreakGlass = true

		if err := p.Validate(); !errors.Is(err, principal.ErrBreakGlassKind) {
			t.Fatalf("validate a break-glass %s: err = %v, want ErrBreakGlassKind", kind, err)
		}

		err := s.CreatePrincipal(ctx, p, entry())
		wantErr(t, "create a break-glass "+string(kind), err, principal.ErrBreakGlassKind)
	}
}
