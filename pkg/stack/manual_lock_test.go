package stack_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
)

// errManualLockRead is the failure of a store that cannot read a manual lock.
var errManualLockRead = errors.New("manual lock store down")

// fakeManualLocks answers one manual lock, or an error. It records the units
// that the service asked about.
type fakeManualLocks struct {
	lock  manuallock.Lock
	err   error
	asked []collection.UnitRef
	// passJob, when set, is the job whose request the lock does not refuse.
	passJob string
	jobs    []string
}

func (f *fakeManualLocks) CoveringFor(
	_ context.Context, unit collection.UnitRef, jobID string,
) (manuallock.Lock, error) {
	f.asked = append(f.asked, unit)
	f.jobs = append(f.jobs, jobID)

	if f.passJob != "" && jobID == f.passJob {
		return manuallock.Lock{}, manuallock.ErrNotLocked
	}

	if f.err != nil {
		return manuallock.Lock{}, f.err
	}

	return f.lock, nil
}

// NamespaceLock answers the held lock when it is on a namespace, as the manual
// lock service does, and the configured error first.
func (f *fakeManualLocks) NamespaceLock(_ context.Context, _ namespace.Name) (manuallock.Lock, error) {
	if f.err != nil {
		return manuallock.Lock{}, f.err
	}

	if f.lock.Target.Kind != manuallock.KindNamespace {
		return manuallock.Lock{}, manuallock.ErrNotLocked
	}

	return f.lock, nil
}

// heldNamespaceLock is a manual lock of alice on the namespace acme.
func heldNamespaceLock() manuallock.Lock {
	return manuallock.Lock{
		Since:  time.Date(2026, time.October, 9, 9, 0, 0, 0, time.UTC),
		Target: manuallock.NamespaceTarget("acme"),
		Holder: "alice",
		Reason: "change window closed",
	}
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
	return &fakeManualLocks{lock: heldCollectionLock(), err: nil, asked: nil, passJob: "", jobs: nil}
}

// failing returns a port that answers with err.
func failing(err error) *fakeManualLocks {
	return &fakeManualLocks{lock: heldCollectionLock(), err: err, asked: nil, passJob: "", jobs: nil}
}

// planOf returns a plan of the kind, as a dry run or not.
func planOf(kind stack.UpdateKind, dryRun bool) stack.Plan {
	return stack.Plan{Kind: kind, DryRun: dryRun, CreatedBy: "", RestoredFrom: 0, GitHead: ""}
}

// withManualLocks returns the update service of f with the manual lock port locks.
func withManualLocks(t *testing.T, f updatesFixture, locks stack.ManualLocks) *stack.Updates {
	t.Helper()

	svc, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:             secrettest.NewMemorySealer(),
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        locks,
		Updates:            f.store,
		Tokens:             nil,
		Checkpoints:        f.store,
		Events:             f.store,
		Namespaces:         f.namespaces,
		Stacks:             f.store,
		Blobs:              f.blobs,
		Refusals:           stacktest.NewRefusals(),
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

	if strings.Contains(err.Error(), "unit") {
		t.Errorf("err = %q, want the word stack, not unit", err)
	}

	for _, want := range []string{"stack acme/web/dev", "alice", "change window closed", "acme/payments"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to name %q", err, want)
		}
	}

	wantUnit := collection.UnitRef{Namespace: "acme", Tool: "pulumi", Name: "web/dev"}
	if len(locks.asked) != 1 || locks.asked[0] != wantUnit {
		t.Errorf("unit asked = %v, want [%v]", locks.asked, wantUnit)
	}

	if manual, ok := errors.AsType[*manuallock.LockedError](err); !ok || manual == nil || manual.Unit != wantUnit {
		t.Errorf("refused unit of %v, want %v", err, wantUnit)
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

// TestStartOfAnEarlierJobPhasePasses proves the job ID of the request reaches
// the manual lock port, that the start the port lets pass runs, and that a
// start with another job ID or none meets the lock.
func TestStartOfAnEarlierJobPhasePasses(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		jobID     string
		wantError bool
	}{
		{name: "job that started before the lock", jobID: "job-1", wantError: false},
		{name: "another job", jobID: "job-2", wantError: true},
		{name: "no job token", jobID: "", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdates(t)
			locks := answering()
			locks.passJob = "job-1"
			svc := withManualLocks(t, f, locks)

			u, err := svc.Create(t.Context(), f.ref, planOf(stack.KindUpdate, false))
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			_, err = svc.StartBy(t.Context(), f.ref, u.ID, stack.StartedBy{TokenID: "", JobID: tc.jobID}, 0)
			if tc.wantError {
				checkRefusal(t, err, locks)
			} else if err != nil {
				t.Fatalf("start of the earlier phase = %v, want it to pass", err)
			}

			if len(locks.jobs) != 1 || locks.jobs[0] != tc.jobID {
				t.Errorf("job IDs asked = %q, want [%q]", locks.jobs, tc.jobID)
			}
		})
	}
}

// newLockedStackService returns the stack service over the store fake of the
// delete tests, with the manual lock port locks, and the ref it acts on.
func newLockedStackService(t *testing.T, locks stack.ManualLocks) (*stack.Service, *deleteStacks, stack.Ref) {
	t.Helper()

	now := time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: now, Name: "acme", PrimaryInstallation: "local", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	namespaces, err := namespace.NewService(
		namespace.Config{
			Store:        &memNamespaces{ns: ns, reads: atomic.Int64{}},
			Now:          func() time.Time { return now },
			Installation: "local",
		},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "dev")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	store := &deleteStacks{mu: sync.Mutex{}, guards: nil, queue: nil, restoreCalls: 0}

	svc, err := stack.NewService(stack.Config{
		Store: store, Namespaces: namespaces, Now: func() time.Time { return now }, State: nil, Retention: 0,
		ManualLocks: locks,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return svc, store, ref
}

// TestDeleteAndRenameMeetTheManualLock proves the delete, the forced delete and
// the rename of a stack fail while a manual lock covers it, before the store is
// reached, with an error that names the holder and the reason. A stack that no
// manual lock covers reaches the store, and a lock that cannot be read refuses
// the request.
func TestDeleteAndRenameMeetTheManualLock(t *testing.T) {
	t.Parallel()

	newName, err := stack.ParseName("prod")
	if err != nil {
		t.Fatalf("parse name: %v", err)
	}

	calls := []struct {
		name string
		call func(*stack.Service, stack.Ref) error
	}{
		{"Delete", func(s *stack.Service, ref stack.Ref) error { return s.Delete(t.Context(), ref, deleteEntry()) }},
		{
			"ForceDelete",
			func(s *stack.Service, ref stack.Ref) error { return s.ForceDelete(t.Context(), ref, deleteEntry()) },
		},
		{"Rename", func(s *stack.Service, ref stack.Ref) error {
			_, renameErr := s.Rename(t.Context(), ref, newName)

			return renameErr
		}},
	}

	for _, c := range calls {
		t.Run(c.name+"/covered", func(t *testing.T) {
			t.Parallel()

			locks := answering()
			svc, store, ref := newLockedStackService(t, locks)

			checkRefusal(t, c.call(svc, ref), locks)

			if got := store.calls(); got != 0 {
				t.Errorf("store delete calls = %d, want 0", got)
			}
		})

		t.Run(c.name+"/not covered", func(t *testing.T) {
			t.Parallel()

			svc, _, ref := newLockedStackService(t, failing(manuallock.ErrNotLocked))

			err := c.call(svc, ref)
			if errors.Is(err, manuallock.ErrLocked) || errors.Is(err, stack.ErrUpdateConflict) {
				t.Fatalf("err = %v, want the request to reach the store", err)
			}
		})

		t.Run(c.name+"/lock cannot be read", func(t *testing.T) {
			t.Parallel()

			svc, store, ref := newLockedStackService(t, failing(errManualLockRead))

			if err := c.call(svc, ref); !errors.Is(err, errManualLockRead) {
				t.Fatalf("err = %v, want the read failure", err)
			}

			if got := store.calls(); got != 0 {
				t.Errorf("store delete calls = %d, want 0", got)
			}
		})
	}
}

// TestImportAndRestoreMeetTheManualLock proves `stack import` and a restore run
// as updates of the kinds import and restore, so a manual lock that covers the
// stack refuses them at the start. The state stays as it was and no update is
// left running.
func TestImportAndRestoreMeetTheManualLock(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()

	finishVersion(t, f, `{"v":1}`)

	locks := answering()
	svc := withManualLocks(t, f, locks)

	calls := []struct {
		name string
		call func() error
	}{
		{"import", func() error {
			_, err := svc.Import(ctx, f.ref, "carol@example.com", checkpoint(0, `{"imported":true}`))

			return err
		}},
		{"restore", func() error {
			_, err := svc.Restore(ctx, f.ref, 1, "bob@example.com")

			return err
		}},
	}

	for _, c := range calls {
		checkRefusal(t, c.call(), locks)
		locks.asked = nil
		locks.jobs = nil
	}

	checkRestoreExport(t, f, `{"v":1}`)
	checkLockFree(t, f)
}

// TestRestoreMeetsTheNamespaceLock proves the restore of a deleted stack fails
// while a manual lock covers its namespace, before the store is reached: the
// store check is not the only layer. A namespace with no lock reaches the store,
// and a lock that cannot be read refuses the restore.
func TestRestoreMeetsTheNamespaceLock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		locks     *fakeManualLocks
		wantErr   error
		wantStore bool
	}{
		{
			"namespace locked",
			&fakeManualLocks{lock: heldNamespaceLock(), err: nil, asked: nil, passJob: "", jobs: nil},
			manuallock.ErrLocked, false,
		},
		{"namespace not locked", failing(manuallock.ErrNotLocked), stack.ErrNotFound, true},
		{"lock cannot be read", failing(errManualLockRead), errManualLockRead, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, store, _ := newLockedStackService(t, tc.locks)

			_, err := svc.RestoreDeleted(t.Context(), "acme", stack.ID("stk-1"), "", deleteEntry())
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("RestoreDeleted = %v, want %v", err, tc.wantErr)
			}

			if got := store.restores(); (got > 0) != tc.wantStore {
				t.Errorf("store restore calls = %d, want reached = %v", got, tc.wantStore)
			}

			if errors.Is(tc.wantErr, manuallock.ErrLocked) {
				var locked *manuallock.LockedError
				if !errors.As(err, &locked) || locked.Lock.Holder != "alice" {
					t.Errorf("err = %v, want a LockedError that names the holder", err)
				}
			}
		})
	}
}
