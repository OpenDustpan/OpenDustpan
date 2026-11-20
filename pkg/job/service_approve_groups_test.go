package job_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

var errGroupsDown = errors.New("principal store is down")

// fakeGroups is a job.GroupReader. members maps a subject to the IDs of its
// groups. Like the real reader, it compares IDs and never names.
type fakeGroups struct {
	members map[string][]string
	// suspended and removed map a subject to the IDs of the groups in which
	// the membership is not active. Like the real reader, the fake ignores them.
	suspended map[string][]string
	removed   map[string][]string
	err       error
	asked     [][]string
}

func (f *fakeGroups) MemberOfAny(_ context.Context, subject string, groupIDs []string) (bool, error) {
	f.asked = append(f.asked, slices.Clone(groupIDs))

	if f.err != nil {
		return false, f.err
	}

	active := func(id string) bool {
		return slices.Contains(f.members[subject], id) &&
			!slices.Contains(f.suspended[subject], id) &&
			!slices.Contains(f.removed[subject], id)
	}

	return slices.ContainsFunc(groupIDs, active), nil
}

// groupGate returns a mod that gives the apply gate the groups names with the
// IDs ids.
func groupGate(names, ids []string) func(*job.Job) {
	return func(j *job.Job) {
		j.Phases[1].Gates[0].Groups, j.Phases[1].Gates[0].GroupIDs = names, ids
	}
}

// groupCase is one approval of a gate that names groups.
type groupCase struct {
	name    string
	names   []string
	ids     []string
	members map[string][]string
	// err is the failure of the reader, and nilPort removes the reader.
	err     error
	nilPort bool
	verdict job.Verdict
	// want is the error of the approval, or nil when it must succeed. asked is
	// the number of calls that the reader must get.
	want  error
	asked int
}

// groupCases are the approvals of carol on a gate of two approvals.
func groupCases() []groupCase {
	platform := []string{"platform"}
	oneID := []string{"grp-1"}
	member := map[string][]string{"carol": {"grp-1"}}

	return []groupCase{
		{"a member of the group", platform, oneID, member, nil, false, job.VerdictApprove, nil, 1},
		{
			"a non-member with job:approve", platform, oneID,
			map[string][]string{},
			nil, false, job.VerdictApprove,
			identity.ErrDenied, 1,
		},
		{
			"a member of another group", platform, oneID,
			map[string][]string{"carol": {"grp-2"}},
			nil, false,
			job.VerdictApprove, identity.ErrDenied, 1,
		},
		{
			"a member of a group with the same name and another ID", platform, oneID,
			map[string][]string{"carol": {"grp-9"}},
			nil, false, job.VerdictApprove, identity.ErrDenied, 1,
		},
		{
			"a group renamed after the gate was made",
			[]string{"old-name"},
			oneID, member, nil, false,
			job.VerdictApprove, nil, 1,
		},
		{
			"a member who left the group after the gate was made", platform, oneID,
			map[string][]string{"carol": {}},
			nil,
			false, job.VerdictApprove, identity.ErrDenied, 1,
		},
		{
			"a failure of the reader",
			platform,
			oneID,
			member,
			errGroupsDown,
			false,
			job.VerdictApprove,
			errGroupsDown,
			1,
		},
		{"no reader", platform, oneID, nil, nil, true, job.VerdictApprove, identity.ErrDenied, 0},
		{"names with no IDs", platform, nil, member, nil, false, job.VerdictApprove, identity.ErrDenied, 0},
		{
			"fewer IDs than names",
			[]string{"platform", "sre"},
			oneID, member, nil, false, job.VerdictApprove,
			identity.ErrDenied, 0,
		},
		{"a gate with no groups and no reader", nil, nil, nil, nil, true, job.VerdictApprove, nil, 0},
		{"a rejection by a non-member", platform, oneID, map[string][]string{}, nil, false, job.VerdictReject, nil, 0},
	}
}

func TestApproveChecksTheGroupsOfTheGate(t *testing.T) {
	t.Parallel()

	for _, tc := range groupCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkGroupCase(t, &tc)
		})
	}
}

// checkGroupCase runs the approval of tc and fails t when the answer, the
// writes or the calls of the reader are not what tc says.
func checkGroupCase(t *testing.T, tc *groupCase) {
	t.Helper()

	reader := &fakeGroups{members: tc.members, suspended: nil, removed: nil, err: tc.err, asked: nil}

	var port job.GroupReader = reader
	if tc.nilPort {
		port = nil
	}

	r := approveRigWithGroups(t, openPolicy(), port)
	r.seed(groupGate(tc.names, tc.ids))

	_, err := r.svc.Approve(t.Context(), actorOf("carol"), approveID, tc.verdict, r.binding(), "")
	if !errors.Is(err, tc.want) {
		t.Fatalf("Approve = %v, want %v", err, tc.want)
	}

	wantChanges := 1

	if tc.want != nil {
		wantChanges = 0

		// The handler audits only a *DeniedError, so each refusal has that type.
		if _, ok := errors.AsType[*job.DeniedError](err); !ok {
			t.Errorf("Approve = %v, want a *DeniedError that the handler audits", err)
		}
	}

	if len(r.store.applies) != wantChanges {
		t.Errorf("changes = %d, want %d", len(r.store.applies), wantChanges)
	}

	if len(reader.asked) != tc.asked {
		t.Fatalf("reader calls = %d, want %d", len(reader.asked), tc.asked)
	}

	if tc.asked > 0 && !slices.Equal(reader.asked[0], tc.ids) {
		t.Errorf("reader asked for %v, want the IDs %v of the gate", reader.asked[0], tc.ids)
	}
}

// TestApproveRefusesAnInactiveMemberOfTheGroup proves that a member whose
// membership is suspended or removed cannot approve, and that an active member
// of the same group can. The reader owns the rule of what is active, so the
// fake keeps the state of each membership and the approval must follow its
// answer.
func TestApproveRefusesAnInactiveMemberOfTheGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		suspended map[string][]string
		removed   map[string][]string
		want      error
	}{
		{"an active member", nil, nil, nil},
		{"a suspended member", map[string][]string{"carol": {"grp-1"}}, nil, identity.ErrDenied},
		{"a removed member", nil, map[string][]string{"carol": {"grp-1"}}, identity.ErrDenied},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			members := map[string][]string{"carol": {"grp-1"}}
			ids := []string{"grp-1"}
			names := []string{"platform"}

			reader := &fakeGroups{
				members: members, suspended: tc.suspended, removed: tc.removed, err: nil, asked: nil,
			}

			r := approveRigWithGroups(t, openPolicy(), reader)
			r.seed(groupGate(names, ids))

			_, err := r.svc.Approve(t.Context(), actorOf("carol"), approveID, job.VerdictApprove, r.binding(), "")
			if !errors.Is(err, tc.want) {
				t.Fatalf("Approve = %v, want %v", err, tc.want)
			}

			wantChanges := 1
			if tc.want != nil {
				wantChanges = 0

				if _, ok := errors.AsType[*job.DeniedError](err); !ok {
					t.Errorf("Approve = %v, want a *DeniedError that the handler audits", err)
				}
			}

			if len(r.store.applies) != wantChanges {
				t.Errorf("changes = %d, want %d", len(r.store.applies), wantChanges)
			}
		})
	}
}

// severalGates seeds the apply phase with two gates: "platform" holds group
// grp-a and needs one approval, "dba" holds group grp-b and needs one. A third
// gate named "open" names no group.
func severalGates(withOpen bool) func(*job.Job) {
	return func(j *job.Job) {
		gates := []job.Gate{
			{
				Type: job.GateApproval, Name: "platform", Approvals: 1, Groups: []string{"platform"},
				GroupIDs: []string{"grp-a"}, AllowSelf: false, Timeout: 0, BindPlan: true,
			},
			{
				Type: job.GateApproval, Name: "dba", Approvals: 1, Groups: []string{"dba"},
				GroupIDs: []string{"grp-b"}, AllowSelf: false, Timeout: 0, BindPlan: true,
			},
		}

		if withOpen {
			gates = []job.Gate{
				gates[0],
				{
					Type: job.GateApproval, Name: "open", Approvals: 1, Groups: nil, GroupIDs: nil,
					AllowSelf: false, Timeout: 0, BindPlan: true,
				},
			}
		}

		j.Phases[1].Gates = gates
	}
}

// TestApproveSeveralGates proves that an approval records the gates that hold
// the approver in For, and refuses an approver that no gate holds.
func TestApproveSeveralGates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		gates     func(*job.Job)
		subject   string
		members   map[string][]string
		suspended map[string][]string
		err       error
		nilPort   bool
		want      error
		wantFor   []string
		asked     int
	}{
		{
			name: "a member of group platform fills platform only", gates: severalGates(false), subject: "carol",
			members: map[string][]string{"carol": {"grp-a"}}, wantFor: []string{"platform"}, asked: 2,
		},
		{
			name: "a member of both groups holds both gates", gates: severalGates(false), subject: "carol",
			members: map[string][]string{"carol": {"grp-a", "grp-b"}}, wantFor: []string{"platform", "dba"}, asked: 2,
		},
		{
			name: "a member of neither group is refused", gates: severalGates(false), subject: "carol",
			members: map[string][]string{"carol": {"grp-z"}}, want: identity.ErrDenied, asked: 2,
		},
		{
			name: "an inactive member is refused", gates: severalGates(false), subject: "carol",
			members:   map[string][]string{"carol": {"grp-a"}},
			suspended: map[string][]string{"carol": {"grp-a"}}, want: identity.ErrDenied, asked: 2,
		},
		{
			name: "a failure of the reader is refused", gates: severalGates(false), subject: "carol",
			members: map[string][]string{"carol": {"grp-a"}}, err: errGroupsDown, want: errGroupsDown, asked: 1,
		},
		{
			name: "no reader is refused", gates: severalGates(false), subject: "carol", nilPort: true,
			want: identity.ErrDenied, asked: 0,
		},
		{
			name: "a gate with no groups holds a person in no group", gates: severalGates(true), subject: "carol",
			members: map[string][]string{"carol": {"grp-z"}}, wantFor: []string{"open"}, asked: 1,
		},
		{
			name: "the starter is refused when no gate allows it", gates: severalGates(false), subject: "alice",
			members: map[string][]string{"alice": {"grp-a", "grp-b"}}, want: job.ErrSelfApproval, asked: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reader := &fakeGroups{members: tc.members, suspended: tc.suspended, removed: nil, err: tc.err, asked: nil}

			var port job.GroupReader = reader
			if tc.nilPort {
				port = nil
			}

			r := approveRigWithGroups(t, openPolicy(), port)
			r.seed(tc.gates)

			_, err := r.svc.Approve(t.Context(), actorOf(tc.subject), approveID, job.VerdictApprove, r.binding(), "")
			if !errors.Is(err, tc.want) {
				t.Fatalf("Approve = %v, want %v", err, tc.want)
			}

			if len(reader.asked) != tc.asked {
				t.Errorf("reader calls = %d, want %d", len(reader.asked), tc.asked)
			}

			if tc.want != nil {
				if len(r.store.applies) != 0 {
					t.Fatalf("changes = %d, want none after a refusal", len(r.store.applies))
				}

				if errors.Is(tc.want, identity.ErrDenied) || errors.Is(tc.want, errGroupsDown) {
					denied, ok := errors.AsType[*job.DeniedError](err)
					if !ok {
						t.Fatalf("Approve = %v, want a *DeniedError that the handler audits", err)
					}

					if errors.Is(tc.want, identity.ErrDenied) && tc.members != nil && tc.err == nil &&
						!strings.Contains(denied.Error(), "platform") {
						t.Errorf("refusal = %q, want the names of the requirements", denied.Error())
					}
				}

				return
			}

			if len(r.store.applies) != 1 {
				t.Fatalf("changes = %d, want 1", len(r.store.applies))
			}

			if got := r.store.applies[0].Approval.For; !slices.Equal(got, tc.wantFor) {
				t.Fatalf("For = %v, want %v", got, tc.wantFor)
			}
		})
	}
}

// TestApproveSeveralGatesRepeat proves that a second approval by one person is
// refused with ErrAlreadyApproved, also when the person holds two gates.
func TestApproveSeveralGatesRepeat(t *testing.T) {
	t.Parallel()

	reader := &fakeGroups{
		members: map[string][]string{"carol": {"grp-a", "grp-b"}}, suspended: nil, removed: nil, err: nil, asked: nil,
	}

	r := approveRigWithGroups(t, openPolicy(), reader)
	r.seed(func(j *job.Job) {
		severalGates(false)(j)
		j.Phases[1].Approvals = []job.Approval{approvedBy("carol")}
		j.Phases[1].Approvals[0].For = []string{"platform", "dba"}
	})

	_, err := r.svc.Approve(t.Context(), actorOf("carol"), approveID, job.VerdictApprove, r.binding(), "")
	if !errors.Is(err, job.ErrAlreadyApproved) {
		t.Fatalf("Approve = %v, want ErrAlreadyApproved", err)
	}

	if len(r.store.applies) != 0 {
		t.Fatalf("changes = %d, want none", len(r.store.applies))
	}
}

// TestApproveSeveralGatesOnePersonFillsOneSlot proves that the event stays
// approval-recorded after the one person who holds both gates approves, and
// becomes approved only with a second person.
func TestApproveSeveralGatesOnePersonFillsOneSlot(t *testing.T) {
	t.Parallel()

	reader := &fakeGroups{
		members:   map[string][]string{"carol": {"grp-a", "grp-b"}, "dave": {"grp-b"}},
		suspended: nil, removed: nil, err: nil, asked: nil,
	}

	r := approveRigWithGroups(t, openPolicy(), reader)
	r.seed(severalGates(false))

	_, err := r.svc.Approve(t.Context(), actorOf("carol"), approveID, job.VerdictApprove, r.binding(), "")
	if err != nil {
		t.Fatalf("Approve by carol: %v", err)
	}

	if got := r.store.applies[0].Event; got != job.EventApprovalRecorded {
		t.Fatalf("event after one person who holds two gates = %s, want %s", got, job.EventApprovalRecorded)
	}

	requirements := ""

	for _, c := range r.store.applyEntries[0][0].Changes {
		if c.Key == "requirements" {
			requirements = c.New
		}
	}

	if requirements != "platform,dba" {
		t.Errorf("audit change requirements = %q, want platform,dba", requirements)
	}

	r2 := approveRigWithGroups(t, openPolicy(), reader)
	r2.seed(func(j *job.Job) {
		severalGates(false)(j)
		first := approvedBy("carol")
		first.For = []string{"platform", "dba"}
		j.Phases[1].Approvals = []job.Approval{first}
	})

	_, err = r2.svc.Approve(t.Context(), actorOf("dave"), approveID, job.VerdictApprove, r2.binding(), "")
	if err != nil {
		t.Fatalf("Approve by dave: %v", err)
	}

	if got := r2.store.applies[0].Event; got != job.EventApproved {
		t.Fatalf("event after carol and dave = %s, want %s", got, job.EventApproved)
	}
}

// TestApproveSeveralGatesStarterFillsOnlyGatesThatAllowIt proves that the
// starter, who holds the groups of two gates, can fill only the gate that
// allows the starter, and gets ErrSelfApproval when it holds only the gate that
// does not.
func TestApproveSeveralGatesStarterFillsOnlyGatesThatAllowIt(t *testing.T) {
	t.Parallel()

	allowPlatform := func(j *job.Job) {
		severalGates(false)(j)
		j.Phases[1].Gates[0].AllowSelf = true
	}

	tests := []struct {
		name    string
		members map[string][]string
		want    error
		wantFor []string
	}{
		{"the starter holds both gates", map[string][]string{"alice": {"grp-a", "grp-b"}}, nil, []string{"platform"}},
		{"the starter holds only the gate that refuses it", map[string][]string{"alice": {"grp-b"}}, job.ErrSelfApproval, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reader := &fakeGroups{members: tc.members, suspended: nil, removed: nil, err: nil, asked: nil}
			r := approveRigWithGroups(t, openPolicy(), reader)
			r.seed(allowPlatform)

			_, err := r.svc.Approve(t.Context(), actorOf("alice"), approveID, job.VerdictApprove, r.binding(), "")
			if !errors.Is(err, tc.want) {
				t.Fatalf("Approve = %v, want %v", err, tc.want)
			}

			if tc.want != nil {
				if len(r.store.applies) != 0 {
					t.Fatalf("changes = %d, want none", len(r.store.applies))
				}

				return
			}

			if got := r.store.applies[0].Approval.For; !slices.Equal(got, tc.wantFor) {
				t.Fatalf("For = %v, want %v", got, tc.wantFor)
			}
		})
	}
}
