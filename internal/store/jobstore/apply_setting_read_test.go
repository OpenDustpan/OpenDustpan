package jobstore_test

import (
	"context"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/jobstore"
	"opendustpan.org/opendustpan/pkg/job"
)

// countingTx is a jobstore.Tx that counts the reads of the setting. The
// embedded settingTx serves the values.
type countingTx struct {
	settingTx

	reads *int
}

// SettingValue implements jobstore.Tx and counts the call.
func (f countingTx) SettingValue(ctx context.Context, key string) (string, bool, error) {
	*f.reads++

	return f.settingTx.SettingValue(ctx, key)
}

// TestApplyReadsTheSettingOnlyForAnApproval proves that a write reads the
// setting freshAuthEnabled only when its change approves a destroy, and reads
// it at most once. Only the approval of a destroy checks the fresh step, so a
// cancel, a reject and the approval of another operation must not pay the
// read. The test fails when the read becomes unconditional again.
func TestApplyReadsTheSettingOnlyForAnApproval(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(testClockMs).UTC()

	approval := func(verdict job.Verdict) *job.Approval {
		return &job.Approval{
			Gate: "", For: nil, Subject: "ada", Agent: "", Verdict: verdict, Comment: "", Plan: nil,
			At: now, Auth: job.ConfirmAuthMFA, AuthAt: now,
		}
	}

	tests := []struct {
		name      string
		operation job.Operation
		event     job.Event
		approval  *job.Approval
		wantReads int
	}{
		{
			name:      "a cancel carries no approval, so it reads no setting",
			operation: job.OperationDestroy,
			event:     job.EventCancel,
			approval:  nil,
			wantReads: 0,
		},
		{
			name:      "a reject of a destroy needs no fresh step, so it reads no setting",
			operation: job.OperationDestroy,
			event:     job.EventRejected,
			approval:  approval(job.VerdictReject),
			wantReads: 0,
		},
		{
			name:      "an approval of an apply needs no fresh step, so it reads no setting",
			operation: job.OperationApply,
			event:     job.EventApproved,
			approval:  approval(job.VerdictApprove),
			wantReads: 0,
		},
		{
			name:      "an approval of a destroy reads the setting once",
			operation: job.OperationDestroy,
			event:     job.EventApproved,
			approval:  approval(job.VerdictApprove),
			wantReads: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			j := testJob(testUnit(t), now)
			j.State = job.StateAwaitingApproval
			j.Spec.Operation = tc.operation

			fake := newFakeTx(now)
			fake.put(t, &j)

			reads := 0
			base := settingTx{fakeTx: fake, settings: map[string]string{"freshAuthEnabled": "true"}, err: nil}
			tx := countingTx{settingTx: base, reads: &reads}

			s := jobstore.New(changingRunner{tx: tx, beforeWrite: func() {}})
			s.SetFreshResolver(testResolver())

			c := changeOf(j.ID, j.Version, tc.event)
			c.Approval = tc.approval

			if _, err := s.Apply(t.Context(), c, testFence(), nil); err != nil {
				t.Fatalf("apply %s: %v", tc.event, err)
			}

			if reads != tc.wantReads {
				t.Errorf("the write read the setting %d times, want %d", reads, tc.wantReads)
			}
		})
	}
}
