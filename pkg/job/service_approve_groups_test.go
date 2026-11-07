package job_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

var errGroupsDown = errors.New("principal store is down")

// fakeGroups is a job.GroupReader. members maps a subject to the IDs of its
// groups. Like the real reader, it compares IDs and never names.
type fakeGroups struct {
	members map[string][]string
	err     error
	asked   [][]string
}

func (f *fakeGroups) MemberOfAny(_ context.Context, subject string, groupIDs []string) (bool, error) {
	f.asked = append(f.asked, slices.Clone(groupIDs))

	if f.err != nil {
		return false, f.err
	}

	return slices.ContainsFunc(groupIDs, func(id string) bool { return slices.Contains(f.members[subject], id) }), nil
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

	reader := &fakeGroups{members: tc.members, err: tc.err, asked: nil}

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
