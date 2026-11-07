package dustpan_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

var errPrincipalsDown = errors.New("principal store is down")

// membership is one membership of a principal in a group, as the fake store
// keeps it.
type membership struct {
	groupID string
	state   principal.MembershipState
}

// groupsStore is a principal.Store that answers the two reads of the adapter,
// with the rules of the port: only an active membership counts, and more than
// principal.MaxMembershipGroupIDs group IDs are ErrInvalid.
type groupsStore struct {
	principal.Store

	people      map[string]principal.Principal
	memberships map[string][]membership
	err         error
}

func (s *groupsStore) GetPrincipalBySubject(_ context.Context, subject string) (principal.Principal, error) {
	if s.err != nil {
		return principal.Principal{}, s.err
	}

	p, ok := s.people[subject]
	if !ok {
		return principal.Principal{}, principal.ErrNotFound
	}

	return p, nil
}

func (s *groupsStore) IsActiveMemberOfAny(_ context.Context, id string, groupIDs []string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}

	if len(groupIDs) > principal.MaxMembershipGroupIDs {
		return false, principal.ErrInvalid
	}

	for _, m := range s.memberships[id] {
		if m.state == principal.MembershipStateActive && slices.Contains(groupIDs, m.groupID) {
			return true, nil
		}
	}

	return false, nil
}

// person returns a principal record for subject.
func person(subject string) principal.Principal {
	return principal.Principal{
		CreatedAt: time.Time{}, UpdatedAt: time.Time{}, DisabledAt: time.Time{}, ID: "prn-" + subject,
		Subject: subject, DisplayName: subject, Kind: principal.KindPerson, Source: principal.SourceLocal,
		Namespace: "", GroupCount: 0, BreakGlass: false,
	}
}

// disabledPerson returns a principal record for subject that an administrator
// disabled.
func disabledPerson(subject string) principal.Principal {
	p := person(subject)
	p.DisabledAt = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	return p
}

func newGroupsStore() *groupsStore {
	active := principal.MembershipStateActive

	return &groupsStore{
		Store: nil,
		people: map[string]principal.Principal{
			"carol": person("carol"), "dave": disabledPerson("dave"), "bo": person("bo"),
		},
		memberships: map[string][]membership{
			"prn-carol": {{groupID: "grp-1", state: active}},
			"prn-dave":  {{groupID: "grp-1", state: active}},
			"prn-bo":    {{groupID: "grp-1", state: principal.MembershipStateSuspended}},
		},
		err: nil,
	}
}

func TestJobGroupsMatchesByID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		subject string
		ids     []string
		want    bool
	}{
		{"a member", "carol", []string{"grp-1"}, true},
		{"a member of one of several groups", "carol", []string{"grp-7", "grp-1"}, true},
		{"a member of another group", "carol", []string{"grp-2"}, false},
		{"no group in the question", "carol", nil, false},
		{"a disabled member", "dave", []string{"grp-1"}, false},
		{"a subject with no principal", "erin", []string{"grp-1"}, false},
		// A suspended membership gives no rights, so it is not a membership.
		{"a member whose membership is suspended", "bo", []string{"grp-1"}, false},
		// A group that someone made again with the name of the group of the gate
		// has another ID, so a gate that names grp-9 does not match it.
		{"a group with the name of the group of the gate and another ID", "carol", []string{"grp-9"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := dustpan.NewJobGroups(newGroupsStore()).MemberOfAny(t.Context(), tc.subject, tc.ids)
			if err != nil || got != tc.want {
				t.Fatalf("MemberOfAny = %v, %v; want %v and no error", got, err, tc.want)
			}
		})
	}
}

func TestJobGroupsFailsClosed(t *testing.T) {
	t.Parallel()

	store := newGroupsStore()
	store.err = errPrincipalsDown

	got, err := dustpan.NewJobGroups(store).MemberOfAny(t.Context(), "carol", []string{"grp-1"})
	if got || !errors.Is(err, errPrincipalsDown) {
		t.Fatalf("MemberOfAny with a failing store = %v, %v; want false and the failure", got, err)
	}

	got, err = dustpan.NewJobGroups(nil).MemberOfAny(t.Context(), "carol", []string{"grp-1"})
	if got || !errors.Is(err, dustpan.ErrNoPrincipalStore) {
		t.Fatalf("MemberOfAny with no store = %v, %v; want false and ErrNoPrincipalStore", got, err)
	}

	// More group IDs than the store takes: an error that denies, never true.
	ids := make([]string, principal.MaxMembershipGroupIDs+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("grp-%03d", i)
	}

	ids[0] = "grp-1"

	got, err = dustpan.NewJobGroups(newGroupsStore()).MemberOfAny(t.Context(), "carol", ids)
	if got || !errors.Is(err, principal.ErrInvalid) {
		t.Fatalf("MemberOfAny with too many group IDs = %v, %v; want false and ErrInvalid", got, err)
	}
}

// storeEntry returns a valid audit entry for a write to the principal store.
func storeEntry() audit.Entry {
	return audit.Entry{
		At: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Namespace: audit.ServerScope, Subject: "admin",
		IDPSubject: "", TokenID: "", Action: "server:manage-principals", Object: "principal:prn-test", Version: "",
		ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "jobgroups-test",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

// openSQLite opens a real store in a temporary directory.
func openSQLite(t *testing.T) *sqlite.Store {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open the store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close the store: %v", closeErr)
		}
	})

	return st
}

// seedMemberships makes the group grp-platform and three people in it: one with
// an active membership, one with a suspended one, and one whose membership
// ended.
func seedMemberships(t *testing.T, st *sqlite.Store) {
	t.Helper()

	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	group := principal.Group{
		CreatedAt: at, UpdatedAt: at, ID: "grp-platform", Name: "platform", Description: "",
		Source: principal.SourceLocal, MemberCount: 0,
	}

	if err := st.CreateGroup(t.Context(), group, storeEntry()); err != nil {
		t.Fatalf("create the group: %v", err)
	}

	for _, name := range []string{"active", "suspended", "ended"} {
		state, suspendedAt := principal.MembershipStateActive, time.Time{}
		if name == "suspended" {
			state, suspendedAt = principal.MembershipStateSuspended, at
		}

		m := principal.Membership{
			AddedAt: at, SuspendedAt: suspendedAt, GroupID: "grp-platform", PrincipalID: "prn-" + name,
			Source: principal.MembershipSourceLocal, SourceID: "", State: state, Pinned: false,
		}

		if err := st.CreatePrincipal(t.Context(), person(name), storeEntry()); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}

		if err := st.AddMember(t.Context(), m, storeEntry(), nil); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}

	if err := st.RemoveMember(t.Context(), "grp-platform", "prn-ended", storeEntry()); err != nil {
		t.Fatalf("remove the member: %v", err)
	}
}

// TestJobGroupsOnTheSQLiteStore reads the memberships of a real store: an
// active membership counts, and a suspended one, an ended one and one of a
// person with no record do not.
func TestJobGroupsOnTheSQLiteStore(t *testing.T) {
	t.Parallel()

	st := openSQLite(t)
	seedMemberships(t, st)

	reader := dustpan.NewJobGroups(st)

	for name, want := range map[string]bool{"active": true, "suspended": false, "ended": false, "nobody": false} {
		got, err := reader.MemberOfAny(t.Context(), name, []string{"grp-platform"})
		if err != nil || got != want {
			t.Errorf("MemberOfAny(%q) = %v, %v; want %v and no error", name, got, err, want)
		}
	}
}

// joinGroup makes a person with the subject and an active membership in the
// group, and creates the group when it does not exist yet.
func joinGroup(t *testing.T, st *sqlite.Store, groupID, subject string) {
	t.Helper()

	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	id := "prn-" + subject

	if err := st.CreatePrincipal(t.Context(), person(subject), storeEntry()); err != nil {
		t.Fatalf("create %s: %v", subject, err)
	}

	group := principal.Group{
		CreatedAt: at, UpdatedAt: at, ID: groupID, Name: groupID, Description: "", Source: principal.SourceLocal,
		MemberCount: 0,
	}
	if _, err := st.GetGroup(t.Context(), groupID); errors.Is(err, principal.ErrNotFound) {
		if createErr := st.CreateGroup(t.Context(), group, storeEntry()); createErr != nil {
			t.Fatalf("create group %s: %v", groupID, createErr)
		}
	}

	m := principal.Membership{
		AddedAt: at, SuspendedAt: time.Time{}, GroupID: groupID, PrincipalID: id,
		Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive, Pinned: false,
	}
	if err := st.AddMember(t.Context(), m, storeEntry(), nil); err != nil {
		t.Fatalf("add %s to %s: %v", subject, groupID, err)
	}
}

// TestJobGroupsMatchesTheSubjectExactly proves on a real store that the subject
// of the approver is compared exactly: another case of the letters, and a
// subject that has the member's subject as its prefix (or the other way round),
// are other people and are no members.
func TestJobGroupsMatchesTheSubjectExactly(t *testing.T) {
	t.Parallel()

	st := openSQLite(t)

	// Only "alice" is a member of grp-1, and only "Alice" is a member of grp-2.
	joinGroup(t, st, "grp-1", "alice")
	joinGroup(t, st, "grp-2", "Alice")

	// Two more people in no group: a longer subject and a shorter one.
	for _, subject := range []string{"alice2", "ali"} {
		if err := st.CreatePrincipal(t.Context(), person(subject), storeEntry()); err != nil {
			t.Fatalf("create %s: %v", subject, err)
		}
	}

	reader := dustpan.NewJobGroups(st)

	tests := []struct {
		subject string
		groupID string
		want    bool
	}{
		{"alice", "grp-1", true},
		{"alice", "grp-2", false},
		{"Alice", "grp-2", true},
		{"Alice", "grp-1", false},
		{"ALICE", "grp-1", false},
		{"ALICE", "grp-2", false},
		{"alice2", "grp-1", false},
		{"ali", "grp-1", false},
		{"alic", "grp-1", false},
	}

	for _, tc := range tests {
		got, err := reader.MemberOfAny(t.Context(), tc.subject, []string{tc.groupID})
		if err != nil || got != tc.want {
			t.Errorf("MemberOfAny(%q, %s) = %v, %v; want %v and no error", tc.subject, tc.groupID, got, err, tc.want)
		}
	}
}

// TestJobGroupsRefusesADisabledPersonOnTheSQLiteStore proves on a real store
// that a person with an active membership gets false once an administrator
// disables the account.
func TestJobGroupsRefusesADisabledPersonOnTheSQLiteStore(t *testing.T) {
	t.Parallel()

	st := openSQLite(t)
	joinGroup(t, st, "grp-1", "dora")

	reader := dustpan.NewJobGroups(st)

	got, err := reader.MemberOfAny(t.Context(), "dora", []string{"grp-1"})
	if err != nil || !got {
		t.Fatalf("MemberOfAny before the disable = %v, %v; want true", got, err)
	}

	if err = st.DisablePrincipal(t.Context(), "prn-dora", storeEntry()); err != nil {
		t.Fatalf("disable: %v", err)
	}

	got, err = reader.MemberOfAny(t.Context(), "dora", []string{"grp-1"})
	if err != nil || got {
		t.Fatalf("MemberOfAny after the disable = %v, %v; want false", got, err)
	}
}
