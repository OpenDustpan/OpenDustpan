package jobstore_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/jobstore"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// TestNoneAuthIsRefusedOutsideADestroy proves that the write transaction refuses
// the authentication none for a change that is not an approval of a destroy,
// whatever the setting says and whatever the kind of the approver. The setting
// is off, so only the operation and the kind rules can refuse. The fresh MFA row
// shows that the same job and approver are accepted with a real proof.
func TestNoneAuthIsRefusedOutsideADestroy(t *testing.T) {
	t.Parallel()

	const subject = "ada"

	tests := []struct {
		name    string
		kind    identity.Kind
		auth    job.ConfirmAuth
		wantErr error
	}{
		{"person with none on an apply refused", identity.KindPerson, job.ConfirmAuthNone, job.ErrAuthStale},
		{"service with none on an apply refused", identity.KindService, job.ConfirmAuthNone, job.ErrAuthStale},
		{"person with fresh MFA on an apply accepted", identity.KindPerson, job.ConfirmAuthMFA, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			now := time.UnixMilli(testClockMs).UTC()

			j := testJob(testUnit(t), now)
			j.State = job.StateAwaitingApproval
			j.Spec.Operation = job.OperationApply

			fake := newFakeTx(now)
			fake.put(t, &j)

			base := settingTx{fakeTx: fake, settings: map[string]string{"freshAuthEnabled": "false"}, err: nil}
			tx := kindTx{
				settingTx:  base,
				principals: map[string]identity.Kind{subject: tc.kind},
			}

			s := jobstore.New(changingRunner{tx: tx, beforeWrite: func() {}}, idempotency.JobStartRetention())
			s.SetFreshResolver(testResolver())

			c := changeOf(j.ID, j.Version, job.EventApproved)
			c.Approval = &job.Approval{
				Gate: "", For: nil, Subject: subject, Agent: "", Verdict: job.VerdictApprove, Comment: "", Plan: nil,
				At: now, Auth: tc.auth, AuthAt: time.Time{},
			}
			if tc.auth == job.ConfirmAuthMFA {
				c.Approval.AuthAt = now
			}

			_, err := s.Apply(t.Context(), c, testFence(), nil)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("apply the approval = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
