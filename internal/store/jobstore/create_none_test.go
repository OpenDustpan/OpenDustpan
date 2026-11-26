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

// errPrincipalLookup is the lookup error of lookupErrTx.
var errPrincipalLookup = errors.New("principal lookup failed")

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

// lookupErrTx is a jobstore.Tx whose principal lookup fails.
type lookupErrTx struct {
	createTx
}

// PrincipalKind implements jobstore.Tx: the lookup fails.
func (lookupErrTx) PrincipalKind(context.Context, string) (identity.Kind, bool, error) {
	return "", false, errPrincipalLookup
}

// TestCreateNoneAuthKinds proves that Create refuses an approval of a destroy
// with the authentication none unless the approver is a person. The setting is
// off, so the kind is the only rule that can refuse. Create accepts the none
// authentication for another operation, because the gate opens on such a job
// when the setting is off, and Apply and the reconcile gate decide what the
// approval is worth.
func TestCreateNoneAuthKinds(t *testing.T) {
	t.Parallel()

	const subject = "ada"

	tests := []struct {
		name      string
		kind      identity.Kind
		hasRow    bool
		lookupErr bool
		op        job.Operation
		auth      job.ConfirmAuth
		wantErr   error
	}{
		{
			name: "person with none on a destroy accepted", kind: identity.KindPerson, hasRow: true,
			lookupErr: false, op: job.OperationDestroy, auth: job.ConfirmAuthNone, wantErr: nil,
		},
		{
			// Apply refuses none outside a destroy; Create accepts it so that the setting-off scenario stays reachable.
			name: "person with none on an apply accepted", kind: identity.KindPerson, hasRow: true,
			lookupErr: false, op: job.OperationApply, auth: job.ConfirmAuthNone, wantErr: nil,
		},
		{
			name: "person with fresh MFA on an apply accepted", kind: identity.KindPerson, hasRow: true,
			lookupErr: false, op: job.OperationApply, auth: job.ConfirmAuthMFA, wantErr: nil,
		},
		{
			name: "service with none on a destroy refused", kind: identity.KindService, hasRow: true,
			lookupErr: false, op: job.OperationDestroy, auth: job.ConfirmAuthNone, wantErr: job.ErrAuthStale,
		},
		{
			name: "workload with none on a destroy refused", kind: identity.KindWorkload, hasRow: true,
			lookupErr: false, op: job.OperationDestroy, auth: job.ConfirmAuthNone, wantErr: job.ErrAuthStale,
		},
		{
			name: "agent with none on a destroy refused", kind: identity.KindAgent, hasRow: true,
			lookupErr: false, op: job.OperationDestroy, auth: job.ConfirmAuthNone, wantErr: job.ErrAuthStale,
		},
		{
			name: "principal not found with none on a destroy refused", kind: "", hasRow: false,
			lookupErr: false, op: job.OperationDestroy, auth: job.ConfirmAuthNone, wantErr: job.ErrAuthStale,
		},
		{
			name: "lookup error with none on a destroy refused", kind: identity.KindPerson, hasRow: true,
			lookupErr: true, op: job.OperationDestroy, auth: job.ConfirmAuthNone, wantErr: job.ErrAuthStale,
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

			principals := map[string]identity.Kind{}
			if tc.hasRow {
				principals[subject] = tc.kind
			}

			base := settingTx{
				fakeTx:   newFakeTx(now),
				settings: map[string]string{"freshAuthEnabled": "false"},
				err:      nil,
			}
			ct := createTx{kindTx{settingTx: base, principals: principals}}

			var tx jobstore.Tx = ct
			if tc.lookupErr {
				tx = lookupErrTx{ct}
			}

			s := jobstore.New(changingRunner{tx: tx, beforeWrite: func() {}})

			_, _, err := s.Create(t.Context(), j, nil, testFence(), nil)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("create the job with the approval = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
