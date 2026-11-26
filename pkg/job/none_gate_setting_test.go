package job_test

import (
	"context"
	"testing"

	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/job"
)

// oneApproval is the approval gate of the seeded job with one slot, so that one
// approval by another person opens it.
func oneApproval() job.Gate {
	g := twoApprovals()
	g.Approvals = 1

	return g
}

// seedNoneGates stores a job whose apply phase has an approval gate of one slot
// and a confirmation gate. The authentication none, as the service records it
// for a destroy, is in the phase as a confirmation of alice, the starter.
func seedNoneGates(r *rig) {
	r.seed(func(j *job.Job) {
		j.Phases[1].Gates = []job.Gate{oneApproval(), job.ConfirmationGate(true)}
		j.Phases[1].Approvals = []job.Approval{withAuth(confirmation("alice", 0), job.ConfirmAuthNone)}
	})
}

// checkOnlyEvent checks that the store received one change with the event want.
func checkOnlyEvent(t *testing.T, r *rig, want job.Event) {
	t.Helper()

	if len(r.store.applies) != 1 {
		t.Fatalf("changes = %d, want 1", len(r.store.applies))
	}

	if got := r.store.applies[0].Event; got != want {
		t.Fatalf("event = %s, want %s", got, want)
	}
}

// TestApproveDoesNotOpenAGateWithNoneWhileTheSettingIsOn proves that Approve
// reads the setting for the gates of its verdict. The stored confirmation with
// the authentication none does not count while the setting is on, so the
// approval records the event of a partly filled phase and not the event that
// opens it. Without the read in Approve (a constant false), the confirmation
// counts and the event is EventApproved.
func TestApproveDoesNotOpenAGateWithNoneWhileTheSettingIsOn(t *testing.T) {
	t.Parallel()

	on := freshstep.PolicyFunc(func(context.Context) bool { return true })
	off := freshstep.PolicyFunc(func(context.Context) bool { return false })

	tests := []struct {
		name      string
		policy    freshstep.Policy
		wantEvent job.Event
	}{
		{"setting on, none does not count", on, job.EventApprovalRecorded},
		{"setting off, none counts", off, job.EventApproved},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := freshRig(t, tc.policy)
			seedNoneGates(r)

			_, err := r.svc.Approve(t.Context(), actorOf("dave"), approveID, job.VerdictApprove, r.binding(), "")
			if err != nil {
				t.Fatalf("Approve = %v, want nil", err)
			}

			checkOnlyEvent(t, r, tc.wantEvent)
		})
	}
}
