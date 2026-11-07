package sqlite

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
)

// activeMemberEntry returns a valid audit entry of the kind change in the scope
// @server.
func activeMemberEntry() audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1_700_000_000_000).UTC(), Namespace: audit.ServerScope, Subject: "admin", IDPSubject: "",
		TokenID: "", Action: "server:manage-principals", Object: "principal:prn-test", Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "active-member-test",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

// TestActiveMemberIgnoresAMembershipThatOutlivesItsGroup is the hostile test of
// the filter on the group row: the membership is active and not ended, and the
// group row is marked deleted. DeleteGroup also ends the memberships of the
// group, so the port cannot make this row. A restore of rows from another
// version could hold it, and then the membership must give no rights.
func TestActiveMemberIgnoresAMembershipThatOutlivesItsGroup(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	at := time.UnixMilli(1_700_000_000_000).UTC()
	s := openInternalTemplated(t)

	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-1", Subject: "alice", DisplayName: "alice",
		Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
	}
	if err := s.CreatePrincipal(ctx, p, activeMemberEntry()); err != nil {
		t.Fatalf("create the principal: %v", err)
	}

	g := principal.Group{
		CreatedAt: at, UpdatedAt: at, ID: "grp-1", Name: "ops", Description: "", Source: principal.SourceLocal,
		MemberCount: 0,
	}
	if err := s.CreateGroup(ctx, g, activeMemberEntry()); err != nil {
		t.Fatalf("create the group: %v", err)
	}

	m := principal.Membership{
		AddedAt: at, SuspendedAt: time.Time{}, GroupID: "grp-1", PrincipalID: "prn-1",
		Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive, Pinned: false,
	}
	if err := s.AddMember(ctx, m, activeMemberEntry(), nil); err != nil {
		t.Fatalf("add the member: %v", err)
	}

	// The control: before the group row is marked deleted, the member counts.
	got, err := s.IsActiveMemberOfAny(ctx, "prn-1", []string{"grp-1"})
	if err != nil || !got {
		t.Fatalf("IsActiveMemberOfAny before the mark = %v, %v; want true", got, err)
	}

	// Mark the group row deleted and leave the membership alone.
	_, err = s.conn.ExecContext(ctx, "UPDATE principal_groups SET deleted_at_ms = ? WHERE id = ?",
		at.UnixMilli(), "grp-1")
	if err != nil {
		t.Fatalf("mark the group deleted: %v", err)
	}

	var open int

	err = s.conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM group_members WHERE group_id = ? AND removed_at_ms IS NULL AND state = 'active'",
		"grp-1").Scan(&open)
	if err != nil || open != 1 {
		t.Fatalf("active memberships of the deleted group = %d, %v; want 1 (the test needs the row)", open, err)
	}

	got, err = s.IsActiveMemberOfAny(ctx, "prn-1", []string{"grp-1"})
	if err != nil || got {
		t.Fatalf("IsActiveMemberOfAny after the mark = %v, %v; want false", got, err)
	}
}
