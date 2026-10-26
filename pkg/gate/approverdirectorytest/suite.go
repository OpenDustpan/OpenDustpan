// Package approverdirectorytest is the conformance suite for
// gate.ApproverDirectory. Every adapter runs it, and a custom adapter passed
// to the gate configuration service should too:
//
//	func TestApproverDirectoryConformance(t *testing.T) {
//		t.Parallel()
//		approverdirectorytest.Run(t, func(t *testing.T) approverdirectorytest.Fixture { return newMine(t) })
//	}
package approverdirectorytest

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Fixture is one empty directory with the writes that a case needs to set it
// up. The adapter under test is Directory; the writes go to the store behind it.
type Fixture struct {
	// Directory is the adapter under test.
	Directory gate.ApproverDirectory
	// Group makes a group called name, with no member, and returns its ID.
	Group func(t *testing.T, name string) string
	// Member makes a principal of kind with the subject, and adds it to the
	// group. A disabled principal is disabled after it joins. It returns the
	// ID of the principal.
	Member func(t *testing.T, group, subject string, kind principal.Kind, disabled bool) string
	// Remove ends the membership of the principal id in the group.
	Remove func(t *testing.T, group, id string)
	// Delete deletes the group with the ID id.
	Delete func(t *testing.T, id string)
	// Rename gives the group with the ID id a new name.
	Rename func(t *testing.T, id, name string)
}

// tooLong is a group name length above the limit of 100 bytes.
const tooLong = 101

// Factory returns a fresh, empty fixture for one subtest.
type Factory func(t *testing.T) Fixture

// Run runs every case of the suite against the fixtures that newFixture makes.
func Run(t *testing.T, newFixture Factory) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, f Fixture)
	}{
		{name: "a group with members answers their counts", test: testCounts},
		{name: "a name in another case finds the group", test: testCase},
		{name: "a longer name with the same prefix does not match", test: testPrefix},
		{name: "a group with no member answers zero and no error", test: testEmptyGroup},
		{name: "a group that does not exist is ErrNotFound", test: testUnknown},
		{name: "a name that is not valid is ErrInvalid", test: testInvalid},
		{name: "a member that left is not counted", test: testRemoved},
		{name: "a group made again with the same name is another group", test: testMadeAgain},
		{name: "a deleted group is not found by its ID", test: testDeleted},
		{name: "a renamed group keeps its ID and shows its new name", test: testRenamed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newFixture(t))
		})
	}
}

// wantCounts fails the test unless the directory answers want for name: it
// resolves the name, and then counts by the ID that it got.
func wantCounts(t *testing.T, f Fixture, name string, want gate.MemberCounts) {
	t.Helper()

	ref, err := f.Directory.ResolveGroup(t.Context(), name)
	if err != nil {
		t.Fatalf("ResolveGroup(%q) = %+v, %v; want a group", name, ref, err)
	}

	wantCountsByID(t, f, ref.ID, want)
}

// wantCountsByID fails the test unless the directory answers want for the
// group with the ID id.
func wantCountsByID(t *testing.T, f Fixture, id string, want gate.MemberCounts) {
	t.Helper()

	ref, got, err := f.Directory.GroupByID(t.Context(), id)
	if err != nil || got != want || ref.ID != id {
		t.Fatalf("GroupByID(%q) = %+v, %+v, %v; want ID %q and %+v", id, ref, got, err, id, want)
	}
}

// wantError fails the test unless the directory answers an error that matches
// want when it resolves name.
func wantError(t *testing.T, f Fixture, name string, want error) {
	t.Helper()

	got, err := f.Directory.ResolveGroup(t.Context(), name)
	if !errors.Is(err, want) {
		t.Fatalf("ResolveGroup(%q) = %+v, %v; want %v", name, got, err, want)
	}
}

// wantIDError fails the test unless the directory answers an error that
// matches want for the group with the ID id.
func wantIDError(t *testing.T, f Fixture, id string, want error) {
	t.Helper()

	ref, counts, err := f.Directory.GroupByID(t.Context(), id)
	if !errors.Is(err, want) {
		t.Fatalf("GroupByID(%q) = %+v, %+v, %v; want %v", id, ref, counts, err, want)
	}
}

// testCounts proves the counts: two persons, one service principal and one
// workload are enabled, and one disabled person and one disabled service
// principal do not count. Enabled counts the first four kinds, Persons the
// persons.
func testCounts(t *testing.T, f Fixture) {
	t.Helper()

	f.Group(t, "platform")
	f.Member(t, "platform", "alice", principal.KindPerson, false)
	f.Member(t, "platform", "bob", principal.KindPerson, false)
	f.Member(t, "platform", "deploy-bot", principal.KindService, false)
	f.Member(t, "platform", "ci-workload", principal.KindWorkload, false)
	f.Member(t, "platform", "carol", principal.KindPerson, true)
	f.Member(t, "platform", "old-bot", principal.KindService, true)

	const enabled, persons = 4, 2

	wantCounts(t, f, "platform", gate.MemberCounts{Enabled: enabled, Persons: persons})
}

// testCase proves that the name matches without regard to case, in both
// directions.
func testCase(t *testing.T, f Fixture) {
	t.Helper()

	f.Group(t, "Platform")
	f.Member(t, "Platform", "alice", principal.KindPerson, false)

	want := gate.MemberCounts{Enabled: 1, Persons: 1}
	wantCounts(t, f, "Platform", want)
	wantCounts(t, f, "platform", want)
	wantCounts(t, f, "PLATFORM", want)
}

// testPrefix proves that a group whose name starts with the asked name is not
// the group, and that the longer group keeps its own count.
func testPrefix(t *testing.T, f Fixture) {
	t.Helper()

	f.Group(t, "ops-extra")
	f.Member(t, "ops-extra", "alice", principal.KindPerson, false)
	f.Member(t, "ops-extra", "bob", principal.KindPerson, false)

	wantError(t, f, "ops", gate.ErrNotFound)
	wantError(t, f, "ops-ex", gate.ErrNotFound)

	f.Group(t, "ops")
	const members = 2

	wantCounts(t, f, "ops", gate.MemberCounts{Enabled: 0, Persons: 0})
	wantCounts(t, f, "ops-extra", gate.MemberCounts{Enabled: members, Persons: members})
}

// testEmptyGroup proves that a group with no member answers zero counts and no
// error: the group exists.
func testEmptyGroup(t *testing.T, f Fixture) {
	t.Helper()

	f.Group(t, "empty")
	wantCounts(t, f, "empty", gate.MemberCounts{Enabled: 0, Persons: 0})
}

// testUnknown proves that a valid name that no group has is ErrNotFound.
func testUnknown(t *testing.T, f Fixture) {
	t.Helper()

	f.Group(t, "platform")
	wantError(t, f, "nogroup", gate.ErrNotFound)
}

// testInvalid proves that a name that is not a group name is ErrInvalid, and
// never ErrNotFound: the caller learns the name is wrong, not that no group has it.
func testInvalid(t *testing.T, f Fixture) {
	t.Helper()

	for _, name := range []string{
		"", "<b>x</b>", " ops", "ops ", "ops team", "-ops", ".ops", "ops/team", "ops\x00", "ops\n",
		strings.Repeat("g", tooLong),
	} {
		wantError(t, f, name, gate.ErrInvalid)
	}
}

// testRemoved proves that a member who left the group is not counted.
func testRemoved(t *testing.T, f Fixture) {
	t.Helper()

	f.Group(t, "platform")
	f.Member(t, "platform", "alice", principal.KindPerson, false)
	leaver := f.Member(t, "platform", "bob", principal.KindPerson, false)

	const before = 2

	wantCounts(t, f, "platform", gate.MemberCounts{Enabled: before, Persons: before})

	f.Remove(t, "platform", leaver)
	wantCounts(t, f, "platform", gate.MemberCounts{Enabled: 1, Persons: 1})
}

// testMadeAgain proves that a group that someone deletes and makes again with
// the same name is another group: the old ID finds nothing, the new ID has its
// own members, and a name resolves to the new ID only.
func testMadeAgain(t *testing.T, f Fixture) {
	t.Helper()

	const remade = 2

	oldID := f.Group(t, "netops")
	f.Member(t, "netops", "alice", principal.KindPerson, false)
	wantCountsByID(t, f, oldID, gate.MemberCounts{Enabled: 1, Persons: 1})

	f.Delete(t, oldID)

	newID := f.Group(t, "netops")
	f.Member(t, "netops", "mallory", principal.KindPerson, false)
	f.Member(t, "netops", "mallory-bot", principal.KindService, false)

	if newID == oldID {
		t.Fatalf("the new group has the ID %q of the deleted group", oldID)
	}

	wantIDError(t, f, oldID, gate.ErrNotFound)
	wantCountsByID(t, f, newID, gate.MemberCounts{Enabled: remade, Persons: 1})

	ref, err := f.Directory.ResolveGroup(t.Context(), "netops")
	if err != nil || ref.ID != newID {
		t.Fatalf("ResolveGroup(netops) = %+v, %v; want the ID %q", ref, err, newID)
	}
}

// testDeleted proves that a deleted group, and an ID that no group ever had,
// answer ErrNotFound, and that the name of the deleted group finds nothing.
func testDeleted(t *testing.T, f Fixture) {
	t.Helper()

	tests := []struct {
		name string
		id   func() string
	}{
		{name: "a group that was deleted", id: func() string {
			id := f.Group(t, "gone")
			f.Member(t, "gone", "alice", principal.KindPerson, false)
			f.Delete(t, id)

			return id
		}},
		{name: "an ID that no group had", id: func() string { return "no-such-group-id" }},
		{name: "an empty ID", id: func() string { return "" }},
	}

	for _, tt := range tests {
		wantIDError(t, f, tt.id(), gate.ErrNotFound)
	}

	wantError(t, f, "gone", gate.ErrNotFound)
}

// testRenamed proves that a rename keeps the ID and the members, shows the new
// name, and that the old name finds nothing any more.
func testRenamed(t *testing.T, f Fixture) {
	t.Helper()

	id := f.Group(t, "netops")
	f.Member(t, "netops", "alice", principal.KindPerson, false)

	f.Rename(t, id, "network-ops")

	ref, counts, err := f.Directory.GroupByID(t.Context(), id)
	if err != nil || ref != (gate.GroupRef{ID: id, Name: "network-ops"}) || counts.Enabled != 1 {
		t.Fatalf("GroupByID(%q) = %+v, %+v, %v; want the name network-ops and 1 member", id, ref, counts, err)
	}

	wantError(t, f, "netops", gate.ErrNotFound)

	resolved, err := f.Directory.ResolveGroup(t.Context(), "network-ops")
	if err != nil || resolved.ID != id {
		t.Fatalf("ResolveGroup(network-ops) = %+v, %v; want the ID %q", resolved, err, id)
	}
}
