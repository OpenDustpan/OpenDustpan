package jobstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/jobstore"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// kindTx is a jobstore.Tx whose principals are a map from subject to kind. A
// subject that is not in the map has no principal row. It embeds settingTx, so
// the setting is read from its settings map.
type kindTx struct {
	settingTx

	principals map[string]identity.Kind
}

// PrincipalKind implements jobstore.Tx.
func (f kindTx) PrincipalKind(_ context.Context, subject string) (identity.Kind, bool, error) {
	k, ok := f.principals[subject]

	return k, ok, nil
}

// TestApproveDestroyKindRule proves that the store refuses an approval of a
// destroy with the authentication none unless the approver is a person. The
// setting is off, so the only rule that can refuse is the kind rule. The
// approval of a fresh MFA proof is not changed by the kind of the approver.
func TestApproveDestroyKindRule(t *testing.T) {
	t.Parallel()

	const subject = "ada"

	tests := []struct {
		name    string
		kind    identity.Kind
		hasRow  bool
		auth    job.ConfirmAuth
		wantErr error
	}{
		{"person with none accepted", identity.KindPerson, true, job.ConfirmAuthNone, nil},
		{"service with none refused", identity.KindService, true, job.ConfirmAuthNone, job.ErrAuthStale},
		{"workload with none refused", identity.KindWorkload, true, job.ConfirmAuthNone, job.ErrAuthStale},
		{"agent with none refused", identity.KindAgent, true, job.ConfirmAuthNone, job.ErrAuthStale},
		{"unknown kind with none refused", identity.Kind("robot"), true, job.ConfirmAuthNone, job.ErrAuthStale},
		{"no principal row with none refused", "", false, job.ConfirmAuthNone, job.ErrAuthStale},
		{"service with fresh MFA accepted", identity.KindService, true, job.ConfirmAuthMFA, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			now := time.UnixMilli(testClockMs).UTC()

			j := testJob(testUnit(t), now)
			j.State = job.StateAwaitingApproval
			j.Spec.Operation = job.OperationDestroy

			fake := newFakeTx(now)
			fake.put(t, &j)

			principals := map[string]identity.Kind{}
			if tc.hasRow {
				principals[subject] = tc.kind
			}

			base := settingTx{fakeTx: fake, settings: map[string]string{"freshAuthEnabled": "false"}, err: nil}
			tx := kindTx{
				settingTx:  base,
				principals: principals,
			}

			s := jobstore.New(changingRunner{tx: tx, beforeWrite: func() {}})
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
