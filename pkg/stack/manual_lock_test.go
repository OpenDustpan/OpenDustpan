package stack_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/stack"
)

// errManualLockRead is the failure of a store that cannot read a manual lock.
var errManualLockRead = errors.New("manual lock store down")

// fakeManualLocks answers one manual lock, or an error. It records the units
// that the service asked about.
type fakeManualLocks struct {
	lock  manuallock.Lock
	err   error
	asked []collection.UnitRef
}

func (f *fakeManualLocks) Covering(_ context.Context, unit collection.UnitRef) (manuallock.Lock, error) {
	f.asked = append(f.asked, unit)

	if f.err != nil {
		return manuallock.Lock{}, f.err
	}

	return f.lock, nil
}

// heldCollectionLock is a manual lock of alice on the collection acme/payments.
func heldCollectionLock() manuallock.Lock {
	return manuallock.Lock{
		Since:  time.Date(2026, time.October, 9, 9, 0, 0, 0, time.UTC),
		Target: manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: "payments"}),
		Holder: "alice",
		Reason: "change window closed",
	}
}

// answering returns a port that answers with the held lock.
func answering() *fakeManualLocks {
	return &fakeManualLocks{lock: heldCollectionLock(), err: nil, asked: nil}
}

// failing returns a port that answers with err.
func failing(err error) *fakeManualLocks {
	return &fakeManualLocks{lock: heldCollectionLock(), err: err, asked: nil}
}

// planOf returns a plan of the kind, as a dry run or not.
func planOf(kind stack.UpdateKind, dryRun bool) stack.Plan {
	return stack.Plan{Kind: kind, DryRun: dryRun, CreatedBy: "", RestoredFrom: 0}
}

// withManualLocks returns the update service of f with the manual lock port locks.
func withManualLocks(t *testing.T, f updatesFixture, locks stack.ManualLocks) *stack.Updates {
	t.Helper()

	svc, err := stack.NewUpdates(stack.UpdatesConfig{
		ManualLocks:        locks,
		Updates:            f.store,
		Tokens:             nil,
		Checkpoints:        f.store,
		Events:             f.store,
		Namespaces:         f.namespaces,
		Stacks:             f.store,
		Blobs:              f.blobs,
		Now:                f.clock.Now,
		Logger:             nil,
		MaxCheckpointBytes: 1024,
		Journal:            nil,
		Replayer:           nil,
	})
	if err != nil {
		t.Fatalf("new updates: %v", err)
	}

	return svc
}

// startPlan creates an update of plan on the stack of f and starts it.
func startPlan(t *testing.T, svc *stack.Updates, f updatesFixture, plan stack.Plan) (stack.Update, error) {
	t.Helper()

	u, err := svc.Create(t.Context(), f.ref, plan)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.Start(t.Context(), f.ref, u.ID)

	return u, err
}

// TestStartMeetsTheManualLock proves the start of a mutating update fails while
// a manual lock covers the stack, with an error that names the holder and the
// reason, and that a preview and a dry run still start.
func TestStartMeetsTheManualLock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		plan    stack.Plan
		refused bool
	}{
		{name: "update", plan: planOf(stack.KindUpdate, false), refused: true},
		{name: "refresh", plan: planOf(stack.KindRefresh, false), refused: true},
		{name: "destroy", plan: planOf(stack.KindDestroy, false), refused: true},
		{name: "preview", plan: planOf(stack.KindPreview, false), refused: false},
		{name: "dry run of an update", plan: planOf(stack.KindUpdate, true), refused: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdates(t)
			locks := answering()
			svc := withManualLocks(t, f, locks)

			u, err := startPlan(t, svc, f, tc.plan)
			if !tc.refused {
				if err != nil {
					t.Fatalf("Start: %v", err)
				}

				return
			}

			checkRefusal(t, err, locks)

			got, getErr := svc.Get(t.Context(), f.ref, u.ID)
			if getErr != nil || got.Status != stack.StatusNotStarted {
				t.Errorf("update after the refused start = %+v, %v; want it not started", got.Status, getErr)
			}
		})
	}
}

// checkRefusal checks that err is the refusal of a manual lock: it matches both
// sentinels, names the object, the holder and the reason, and the port got the
// unit of the stack.
func checkRefusal(t *testing.T, err error, locks *fakeManualLocks) {
	t.Helper()

	if !errors.Is(err, stack.ErrUpdateConflict) || !errors.Is(err, manuallock.ErrLocked) {
		t.Fatalf("err = %v, want ErrUpdateConflict and manuallock.ErrLocked", err)
	}

	for _, want := range []string{"alice", "change window closed", "acme/payments"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to name %q", err, want)
		}
	}

	wantUnit := collection.UnitRef{Namespace: "acme", Tool: "pulumi", Name: "web/dev"}
	if len(locks.asked) != 1 || locks.asked[0] != wantUnit {
		t.Errorf("unit asked = %v, want [%v]", locks.asked, wantUnit)
	}
}

// TestStartWithoutAManualLock proves a stack that no manual lock covers starts
// as before, with or without the port, and that a failure to read the manual
// lock refuses the start: the check fails closed.
func TestStartWithoutAManualLock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		locks   func() stack.ManualLocks
		wantErr error
	}{
		{name: "no port", locks: func() stack.ManualLocks { return nil }, wantErr: nil},
		{
			name:    "no manual lock covers the stack",
			locks:   func() stack.ManualLocks { return failing(manuallock.ErrNotLocked) },
			wantErr: nil,
		},
		{
			name:    "the manual lock cannot be read",
			locks:   func() stack.ManualLocks { return failing(errManualLockRead) },
			wantErr: errManualLockRead,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdates(t)
			svc := withManualLocks(t, f, tc.locks())

			_, err := startPlan(t, svc, f, planOf(stack.KindUpdate, false))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}

			if errors.Is(err, stack.ErrUpdateConflict) {
				t.Errorf("err = %v: a read failure is not a conflict", err)
			}
		})
	}
}
