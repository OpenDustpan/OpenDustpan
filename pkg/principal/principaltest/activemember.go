package principaltest

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/principal"
)

// addMemberInState adds the principal to the group with the membership state.
func addMemberInState(t *testing.T, s principal.Store, groupID, principalID string, state principal.MembershipState) {
	t.Helper()

	m := principal.Membership{
		AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: groupID, PrincipalID: principalID,
		Source: principal.MembershipSourceLocal, SourceID: "", State: state, Pinned: false,
	}

	if state == principal.MembershipStateSuspended {
		m.SuspendedAt = at(2)
	}

	if err := s.AddMember(t.Context(), m, entry(), nil); err != nil {
		t.Fatalf("add %q to %q as %s: %v", principalID, groupID, state, err)
	}
}

// testActiveMemberOfAny proves that only an active membership that is not
// ended, in a group that exists, counts.
func testActiveMemberOfAny(t *testing.T, s principal.Store) {
	t.Helper()

	for _, p := range []struct{ id, subject string }{
		{"prn-active", "active"},
		{"prn-suspended", "suspended"},
		{"prn-removed", "removed"},
		{"prn-other", "other"},
		{"prn-none", "none"},
		{"prn-gone", "gone"},
	} {
		mustCreatePrincipal(t, s, newPrincipal(p.id, p.subject, principal.KindPerson))
	}

	for _, g := range []struct{ id, name string }{{"grp-1", "ops"}, {"grp-2", "dev"}, {"grp-3", "old"}} {
		mustCreateGroup(t, s, newGroup(g.id, g.name))
	}

	addMemberInState(t, s, "grp-1", "prn-active", principal.MembershipStateActive)
	addMemberInState(t, s, "grp-1", "prn-suspended", principal.MembershipStateSuspended)
	addMemberInState(t, s, "grp-1", "prn-removed", principal.MembershipStateActive)
	addMemberInState(t, s, "grp-2", "prn-other", principal.MembershipStateActive)
	addMemberInState(t, s, "grp-3", "prn-gone", principal.MembershipStateActive)

	if err := s.RemoveMember(t.Context(), "grp-1", "prn-removed", entry()); err != nil {
		t.Fatalf("remove a member: %v", err)
	}

	if err := s.DeleteGroup(t.Context(), "grp-3", entry()); err != nil {
		t.Fatalf("delete a group: %v", err)
	}

	tests := []struct {
		name        string
		principalID string
		groupIDs    []string
		want        bool
	}{
		{"an active member", "prn-active", []string{"grp-1"}, true},
		{"an active member among other groups", "prn-active", []string{"grp-9", "grp-2", "grp-1"}, true},
		{"an active member of another group", "prn-active", []string{"grp-2"}, false},
		{"a suspended member", "prn-suspended", []string{"grp-1"}, false},
		{"a suspended member among other groups", "prn-suspended", []string{"grp-2", "grp-1"}, false},
		{"a member whose membership ended", "prn-removed", []string{"grp-1"}, false},
		{"a member of another group only", "prn-other", []string{"grp-1"}, false},
		{"a member of one of two groups", "prn-other", []string{"grp-1", "grp-2"}, true},
		{"a principal in no group", "prn-none", []string{"grp-1", "grp-2"}, false},
		{"a member of a group that was deleted", "prn-gone", []string{"grp-3"}, false},
		{"a principal that does not exist", "prn-missing", []string{"grp-1"}, false},
		{"a group that does not exist", "prn-active", []string{"grp-missing"}, false},
		{"no group ID", "prn-active", nil, false},
		{"the ID of a principal in place of a group", "prn-active", []string{"prn-active"}, false},
	}

	for _, tc := range tests {
		got, err := s.IsActiveMemberOfAny(t.Context(), tc.principalID, tc.groupIDs)
		if err != nil || got != tc.want {
			t.Errorf("%s: IsActiveMemberOfAny(%q, %v) = %v, %v; want %v and no error", tc.name, tc.principalID,
				tc.groupIDs, got, err, tc.want)
		}
	}
}

// testActiveMemberOfAnyBound proves the bound on the group IDs: the largest
// list works, and one more is ErrInvalid.
func testActiveMemberOfAnyBound(t *testing.T, s principal.Store) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreateGroup(t, s, newGroup("grp-last", "last"))
	addMemberInState(t, s, "grp-last", "prn-1", principal.MembershipStateActive)

	ids := make([]string, principal.MaxMembershipGroupIDs)
	for i := range ids {
		ids[i] = fmt.Sprintf("grp-none-%03d", i)
	}

	ids[len(ids)-1] = "grp-last"

	got, err := s.IsActiveMemberOfAny(t.Context(), "prn-1", ids)
	if err != nil || !got {
		t.Fatalf("IsActiveMemberOfAny with %d group IDs = %v, %v; want true", len(ids), got, err)
	}

	_, err = s.IsActiveMemberOfAny(t.Context(), "prn-1", slices.Concat(ids, []string{"grp-one-more"}))
	wantErr(t, "IsActiveMemberOfAny with one group ID too many", err, principal.ErrInvalid)
}
