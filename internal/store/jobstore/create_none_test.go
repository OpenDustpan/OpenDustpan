package jobstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/jobstore"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// createTx is a jobstore.Tx for Create. It counts no open job and stores each
// inserted row in the embedded fake, so the write path of Create runs. The
// principals come from the embedded kindTx.
type createTx struct {
	kindTx
}

// CountOpen implements jobstore.Tx: no job is open.
func (createTx) CountOpen(context.Context, namespace.Name, string) (int, error) { return 0, nil }

// InsertJob implements jobstore.Tx.
func (createTx) InsertJob(context.Context, jobstore.Row) error { return nil }

// TestCreateStoresNoneAuthApprovals proves that Create stores an approval with
// the authentication none for each operation and each approver kind. With the
// setting off the gate opens on such a job, so Create does not refuse it. The
// reconcile gate and Apply decide what the approval is worth.
func TestCreateStoresNoneAuthApprovals(t *testing.T) {
	t.Parallel()

	const subject = "ada"

	tests := []struct {
		name    string
		kind    identity.Kind
		op      job.Operation
		auth    job.ConfirmAuth
		wantErr error
	}{
		{
			name: "person with none on a destroy accepted", kind: identity.KindPerson,
			op: job.OperationDestroy, auth: job.ConfirmAuthNone, wantErr: nil,
		},
		{
			name: "person with none on an apply accepted", kind: identity.KindPerson,
			op: job.OperationApply, auth: job.ConfirmAuthNone, wantErr: nil,
		},
		{
			name: "service with none on a destroy accepted", kind: identity.KindService,
			op: job.OperationDestroy, auth: job.ConfirmAuthNone, wantErr: nil,
		},
		{
			name: "person with fresh MFA on an apply accepted", kind: identity.KindPerson,
			op: job.OperationApply, auth: job.ConfirmAuthMFA, wantErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			now := time.UnixMilli(testClockMs).UTC()

			j := testJob(testUnit(t), now)
			j.State = job.StateAwaitingApproval
			j.Spec.Operation = tc.op
			j.Phases[0].Approvals = []job.Approval{{
				Gate: "", For: nil, Subject: subject, Agent: "", Verdict: job.VerdictApprove, Comment: "", Plan: nil,
				At: now, Auth: tc.auth, AuthAt: time.Time{},
			}}
			if tc.auth == job.ConfirmAuthMFA {
				j.Phases[0].Approvals[0].AuthAt = now
			}

			fake := newFakeTx(now)
			base := settingTx{fakeTx: fake, settings: map[string]string{"freshAuthEnabled": "false"}, err: nil}
			principals := map[string]identity.Kind{subject: tc.kind}

			tx := createTx{kindTx{settingTx: base, principals: principals}}

			s := jobstore.New(changingRunner{tx: tx, beforeWrite: func() {}})

			_, _, err := s.Create(t.Context(), j, nil, testFence(), nil)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("create the job with the approval = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
