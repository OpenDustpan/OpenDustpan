package backup

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
)

// errFakeStoreUnavailable is the canned error of a failed Get of the claim.
var errFakeStoreUnavailable = errors.New("store unavailable")

// fakeClaimOps is a durable store that returns one operation from Get. Other
// methods are never called: the claim check reads only the operation.
type fakeClaimOps struct {
	durable.Store

	op  durable.Operation
	err error
}

// Get returns the canned operation or the canned error.
func (f *fakeClaimOps) Get(context.Context, durable.ID) (durable.Operation, error) {
	if f.err != nil {
		return durable.Operation{
			ID: "", Key: "", Kind: "", Version: 0, Input: nil, State: "", Steps: nil,
			Attempt: 0, Failures: 0, Expiries: 0, LastError: "",
			Claim: durable.Claim{
				Operation: "", Owner: "", Token: 0, ExpiresAt: time.Time{},
			},
			CancelRequested: false,
			NotBefore:       time.Time{}, CreatedAt: time.Time{}, UpdatedAt: time.Time{},
		}, f.err
	}

	return f.op, nil
}

// claimOp returns a running operation with the token and the cancel flag.
func claimOp(id durable.ID, state durable.State, token int64, cancel bool) durable.Operation {
	return durable.Operation{
		ID: id, Key: "op-claim-check", Kind: "backup", Version: 1, Input: nil, State: state,
		Steps: nil, Attempt: 1, Failures: 0, Expiries: 0, LastError: "",
		Claim:           durable.Claim{Operation: id, Owner: "test", Token: token, ExpiresAt: claimTestTime},
		CancelRequested: cancel, NotBefore: time.Time{}, CreatedAt: claimTestTime, UpdatedAt: claimTestTime,
	}
}

// pendingOp returns a pending operation with no claim.
func pendingOp(id durable.ID, state durable.State) durable.Operation {
	return durable.Operation{
		ID: id, Key: "op-claim-check", Kind: "backup", Version: 1, Input: nil, State: state,
		Steps: nil, Attempt: 0, Failures: 0, Expiries: 0, LastError: "",
		Claim: durable.Claim{
			Operation: "", Owner: "", Token: 0, ExpiresAt: time.Time{},
		},
		CancelRequested: false,
		NotBefore:       time.Time{}, CreatedAt: claimTestTime, UpdatedAt: claimTestTime,
	}
}

// claimService returns a service whose operations return op or err from Get.
func claimService(op durable.Operation, err error) *Service {
	return &Service{
		exporter: nil, kinds: nil, registry: nil, source: nil, opener: nil,
		target: nil, targetName: "", now: nil, version: "", maxSQL: 0, maxUnverified: 0,
		runner: nil, operations: &fakeClaimOps{Store: nil, op: op, err: err}, lease: 0,
		logger: slog.New(slog.DiscardHandler),
	}
}

// TestClaimCheckOf proves the real point-of-use check: a running operation
// with the same token passes, another token and a non-running operation get
// ErrClaimLost, a cancel gets ErrCanceled, and a failed Get returns its
// error.
func TestClaimCheckOf(t *testing.T) {
	t.Parallel()

	opID := durable.ID("op-claim-check")

	for _, tc := range []struct {
		name   string
		op     durable.Operation
		getErr error
		token  int64
		want   error
	}{
		{
			name: "running with the same token passes", op: claimOp(opID, durable.StateRunning, 7, false),
			getErr: nil, token: 7, want: nil,
		},
		{
			name: "another token loses the claim", op: claimOp(opID, durable.StateRunning, 7, false),
			getErr: nil, token: 8, want: durable.ErrClaimLost,
		},
		{
			name: "pending operation loses the claim", op: pendingOp(opID, durable.StatePending),
			getErr: nil, token: 0, want: durable.ErrClaimLost,
		},
		{
			name: "succeeded operation loses the claim", op: pendingOp(opID, durable.StateSucceeded),
			getErr: nil, token: 0, want: durable.ErrClaimLost,
		},
		{
			name: "cancel request cancels", op: claimOp(opID, durable.StateRunning, 7, true),
			getErr: nil, token: 7, want: durable.ErrCanceled,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := claimService(tc.op, tc.getErr)
			check := svc.claimCheckOf(durable.StepFence{Operation: opID, Token: tc.token, Step: StepWrite})

			if err := check(t.Context()); !errors.Is(err, tc.want) {
				t.Fatalf("claim check = %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("failed Get returns its error", func(t *testing.T) {
		t.Parallel()

		svc := claimService(pendingOp(opID, durable.StatePending), errFakeStoreUnavailable)
		check := svc.claimCheckOf(durable.StepFence{Operation: opID, Token: 7, Step: StepWrite})

		if err := check(t.Context()); !errors.Is(err, errFakeStoreUnavailable) {
			t.Fatalf("claim check = %v, want the error of Get", err)
		}
	})
}

// claimTestTime is the fixed time of the operation records that these tests
// build. No test here depends on the wall clock.
var claimTestTime = time.Unix(1_700_000_000, 0)
