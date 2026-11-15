package workspace_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// errManualLockRead is the failure of a store that cannot read a manual lock.
var errManualLockRead = errors.New("manual lock store down")

// fakeManualLocks answers the manual lock of one unit, or an error. It records
// the units that the service asked about.
type fakeManualLocks struct {
	lock  manuallock.Lock
	err   error
	asked []manuallock.Unit
	// passJob, when set, is the job whose request the lock does not refuse.
	passJob string
	jobs    []string
	// phases holds the start of the phase of each job. A job passes a lock that
	// came after its phase, as the manual lock service lets it.
	phases map[string]time.Time
}

func (f *fakeManualLocks) CoveringFor(
	_ context.Context, unit manuallock.Unit, jobID string,
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

// answering returns a port that answers with lock.
func answering(lock manuallock.Lock) *fakeManualLocks {
	return &fakeManualLocks{lock: lock, err: nil, asked: nil, passJob: "", jobs: nil, phases: nil}
}

// failing returns a port that answers with err.
func failing(err error) *fakeManualLocks {
	return &fakeManualLocks{lock: heldCollectionLock(), err: err, asked: nil, passJob: "", jobs: nil, phases: nil}
}

// NamespaceLockFor answers the lock of the namespace for a unit that does not
// exist yet, and lets a job pass a lock that came after its phase.
func (f *fakeManualLocks) NamespaceLockFor(
	ctx context.Context, unit manuallock.Unit, jobID string,
) (manuallock.Lock, error) {
	f.asked = append(f.asked, unit)
	f.jobs = append(f.jobs, jobID)

	lock, err := f.NamespaceLock(ctx, unit.Ref.Namespace)
	if err != nil {
		return manuallock.Lock{}, err
	}

	if started := f.PhaseStart(ctx, unit, jobID); !started.IsZero() && started.Before(lock.Since) {
		return manuallock.Lock{}, manuallock.ErrNotLocked
	}

	return lock, nil
}

// PhaseStart answers the start of the phase of the job, or the zero time.
func (f *fakeManualLocks) PhaseStart(_ context.Context, _ manuallock.Unit, jobID string) time.Time {
	return f.phases[jobID]
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

// withManualLocks returns a service over the stores of f that asks locks.
func withManualLocks(t *testing.T, f fixture, locks workspace.ManualLocks) *workspace.Service {
	t.Helper()

	svc, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        locks,
		Workspaces:         f.store,
		States:             f.store,
		Blobs:              f.blobs,
		Namespaces:         f.namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             f.secrets,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      0,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// checkLockedError checks that err is the refusal of a manual lock: it matches
// both lock sentinels and names the object, the holder and the reason.
func checkLockedError(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, workspace.ErrLocked) || !errors.Is(err, manuallock.ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked and manuallock.ErrLocked", err)
	}

	if strings.Contains(err.Error(), "unit") {
		t.Errorf("err = %q, want the word workspace, not unit", err)
	}

	for _, want := range []string{"workspace acme/", "alice", "change window closed", "acme/payments"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to name %q", err, want)
		}
	}

	manual, ok := errors.AsType[*manuallock.LockedError](err)
	if !ok || manual == nil || manual.Unit.Namespace != "acme" || manual.Unit.Tool != workspace.InventoryTool ||
		manual.Unit.Name == "" {
		t.Errorf("refusal %v names no refused workspace of acme", err)
	}
}

// TestLockRequestsMeetTheManualLock proves both lock requests of OpenTofu fail
// while a manual lock covers the workspace, and that the error names the
// holder and the reason of the manual lock. The workspace stays unlocked.
func TestLockRequestsMeetTheManualLock(t *testing.T) {
	t.Parallel()

	requests := []struct {
		name string
		call func(*testing.T, *workspace.Service, fixture) error
	}{
		{
			name: "Lock",
			call: func(t *testing.T, s *workspace.Service, f fixture) error {
				t.Helper()

				_, err := s.Lock(t.Context(), f.ws.ID, "ci", "plan")

				return err
			},
		},
		{
			name: "LockWithID",
			call: func(t *testing.T, s *workspace.Service, f fixture) error {
				t.Helper()

				_, err := s.LockWithID(t.Context(), f.ws.ID, "ci", "plan", "lock-1")

				return err
			},
		},
	}

	for _, req := range requests {
		t.Run(req.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			locks := answering(heldCollectionLock())
			svc := withManualLocks(t, f, locks)

			checkLockedError(t, req.call(t, svc, f))

			wantUnit := manuallock.Unit{
				Ref:         collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: f.ws.Ref.Name.String()},
				NamespaceID: f.ws.NamespaceID,
				UnitID:      f.ws.ID.String(),
			}
			if len(locks.asked) != 1 || locks.asked[0] != wantUnit {
				t.Errorf("unit asked = %v, want [%v]", locks.asked, wantUnit)
			}

			got, getErr := svc.GetByID(t.Context(), f.ws.ID)
			if getErr != nil || got.Lock.Locked() {
				t.Errorf("workspace after the refused request = %+v, %v; want it unlocked", got.Lock, getErr)
			}
		})
	}
}

// TestLockRequestsWithoutAManualLock proves a workspace that no manual lock
// covers locks as before, with or without the port, and that a failure to read
// the manual lock refuses the request: the check fails closed.
func TestLockRequestsWithoutAManualLock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		locks    func() workspace.ManualLocks
		wantErr  error
		wantLock bool
	}{
		{name: "no port", locks: func() workspace.ManualLocks { return nil }, wantErr: nil, wantLock: true},
		{
			name:     "no manual lock covers the workspace",
			locks:    func() workspace.ManualLocks { return failing(manuallock.ErrNotLocked) },
			wantErr:  nil,
			wantLock: true,
		},
		{
			name:     "the manual lock cannot be read",
			locks:    func() workspace.ManualLocks { return failing(errManualLockRead) },
			wantErr:  errManualLockRead,
			wantLock: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			svc := withManualLocks(t, f, tc.locks())

			locked, err := svc.Lock(t.Context(), f.ws.ID, "ci", "plan")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}

			if locked.Lock.Locked() != tc.wantLock {
				t.Errorf("locked = %v, want %v", locked.Lock.Locked(), tc.wantLock)
			}

			if errors.Is(err, workspace.ErrLocked) {
				t.Errorf("err = %v: a read failure is not a lock", err)
			}
		})
	}
}

// TestManualLockLeavesAHeldToolLockAlone proves a tool lock taken before the
// manual lock stays usable: its holder unlocks it, and an unlock never asks the
// manual lock port.
func TestManualLockLeavesAHeldToolLockAlone(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.svc.Lock(t.Context(), f.ws.ID, "ci", "apply"); err != nil {
		t.Fatalf("lock before the manual lock: %v", err)
	}

	locks := answering(heldCollectionLock())
	svc := withManualLocks(t, f, locks)

	if _, err := svc.Unlock(t.Context(), f.ws.ID, "ci"); err != nil {
		t.Fatalf("unlock by the holder of the tool lock: %v", err)
	}

	if len(locks.asked) != 0 {
		t.Errorf("manual lock port asked %d times on an unlock, want 0", len(locks.asked))
	}

	if _, err := svc.Lock(t.Context(), f.ws.ID, "ci", "plan"); !errors.Is(err, manuallock.ErrLocked) {
		t.Errorf("lock after the unlock: err = %v, want the manual lock to refuse it", err)
	}
}

// jobLockRequests are the two lock requests of OpenTofu that carry a job ID.
func jobLockRequests() []struct {
	name string
	call func(*testing.T, *workspace.Service, fixture, string) error
} {
	return []struct {
		name string
		call func(*testing.T, *workspace.Service, fixture, string) error
	}{
		{
			name: "LockForJob",
			call: func(t *testing.T, s *workspace.Service, f fixture, jobID string) error {
				t.Helper()

				_, err := s.LockForJob(t.Context(), f.ws.ID, "job-1", "plan", jobID)

				return err
			},
		},
		{
			name: "LockWithIDForJob",
			call: func(t *testing.T, s *workspace.Service, f fixture, jobID string) error {
				t.Helper()

				_, err := s.LockWithIDForJob(t.Context(), f.ws.ID, workspace.LockRequest{
					Holder: "job-1", Reason: "plan", LockID: "lock-1", JobID: jobID,
				})

				return err
			},
		},
	}
}

// jobLockResult is the outcome of a lock request with a job ID against a manual
// lock that lets only job-1 pass.
type jobLockResult struct {
	err    error
	jobs   []string
	held   bool
	getErr error
}

// sendJobLockRequest sends one lock request with the job ID jobID against a
// manual lock that lets only job-1 pass.
func sendJobLockRequest(
	t *testing.T, call func(*testing.T, *workspace.Service, fixture, string) error, jobID string,
) jobLockResult {
	t.Helper()

	f := newFixture(t)
	locks := answering(heldCollectionLock())
	locks.passJob = "job-1"
	svc := withManualLocks(t, f, locks)

	err := call(t, svc, f, jobID)
	got, getErr := svc.GetByID(t.Context(), f.ws.ID)

	return jobLockResult{err: err, jobs: locks.jobs, held: got.Lock.Locked(), getErr: getErr}
}

// jobLockCase is one job ID that a lock request carries, and whether the manual
// lock refuses it.
type jobLockCase struct {
	name    string
	jobID   string
	refused bool
}

// checkJobLockResult checks the outcome of the lock request of tc.
func checkJobLockResult(t *testing.T, tc *jobLockCase, res jobLockResult) {
	t.Helper()

	if tc.refused {
		checkLockedError(t, res.err)
	} else if res.err != nil {
		t.Fatalf("lock request of the earlier phase = %v, want it to pass", res.err)
	}

	if len(res.jobs) != 1 || res.jobs[0] != tc.jobID {
		t.Errorf("job IDs asked = %q, want [%q]", res.jobs, tc.jobID)
	}

	if res.getErr != nil || res.held == tc.refused {
		t.Errorf("tool lock held = %v (%v) after the request, want %v", res.held, res.getErr, !tc.refused)
	}
}

// TestLockRequestOfAnEarlierJobPhasePasses proves the job ID of the request
// reaches the manual lock port, that a request the port lets pass takes the
// tool lock, and that a request with another job ID or none meets the lock.
func TestLockRequestOfAnEarlierJobPhasePasses(t *testing.T) {
	t.Parallel()

	jobs := []jobLockCase{
		{"job that started before the lock", "job-1", false},
		{"another job", "job-2", true},
		{"no job token", "", true},
	}

	for _, req := range jobLockRequests() {
		for _, tc := range jobs {
			t.Run(req.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				checkJobLockResult(t, &tc, sendJobLockRequest(t, req.call, tc.jobID))
			})
		}
	}
}

// renameWorkspace gives the workspace of f the name name, as a rename would,
// and keeps its ID and namespace ID.
func renameWorkspace(t *testing.T, f fixture, name workspace.Name) workspace.Workspace {
	t.Helper()

	f.store.mu.Lock()
	defer f.store.mu.Unlock()

	w := f.store.workspaces[f.ws.ID]
	w.Ref.Name = name
	f.store.workspaces[f.ws.ID] = w

	return w
}

// TestLockAfterARenamePassesTheLock proves that a lock request reads the stable
// namespace ID and workspace ID after a rename, so the phase of its job passes
// the manual lock under the new name.
func TestLockAfterARenamePassesTheLock(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	name, err := workspace.ParseName("web-2")
	if err != nil {
		t.Fatalf("parse name: %v", err)
	}

	w := renameWorkspace(t, f, name)

	locks := answering(heldCollectionLock())
	locks.passJob = "job-1"
	svc := withManualLocks(t, f, locks)

	if _, err := svc.LockForJob(t.Context(), w.ID, "ci", "plan", "job-1"); err != nil {
		t.Fatalf("lock after a rename = %v, want it to pass", err)
	}

	want := manuallock.Unit{
		Ref:         collection.UnitRef{Namespace: "acme", Tool: workspace.InventoryTool, Name: name.String()},
		NamespaceID: f.ws.NamespaceID,
		UnitID:      f.ws.ID.String(),
	}
	if len(locks.asked) != 1 || locks.asked[0] != want {
		t.Errorf("unit asked = %v, want [%v]", locks.asked, want)
	}
}

// TestDeleteMeetsTheManualLock proves the delete of a workspace fails while a
// manual lock covers it, names the holder and the reason, and leaves the
// workspace in place. A workspace that no manual lock covers is deleted, and a
// lock that cannot be read refuses the delete.
func TestDeleteMeetsTheManualLock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		locks   *fakeManualLocks
		wantErr error
	}{
		{"covered", answering(heldCollectionLock()), manuallock.ErrLocked},
		{"not covered", failing(manuallock.ErrNotLocked), nil},
		{"the lock cannot be read", failing(errManualLockRead), errManualLockRead},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			svc := withManualLocks(t, f, tc.locks)

			err := svc.Delete(t.Context(), f.ws.Ref, retentionEntry())
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Delete = %v, want %v", err, tc.wantErr)
			}

			_, getErr := svc.GetByID(t.Context(), f.ws.ID)
			if (tc.wantErr == nil) != errors.Is(getErr, workspace.ErrNotFound) {
				t.Errorf("workspace after Delete(%v): %v", err, getErr)
			}

			if errors.Is(tc.wantErr, manuallock.ErrLocked) {
				checkLockedError(t, err)
			}
		})
	}
}

// TestUnlockedWriteMeetsTheManualLock proves a state declare that holds no lock
// fails while a manual lock covers the workspace, even with the override on, and
// writes no override entry. A declare under the lock of the caller is not
// refused: the holder of a tool lock keeps writing.
func TestUnlockedWriteMeetsTheManualLock(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	allow := namespace.Overrides{Unlocked: true, Forced: false}

	if _, err := f.namespaces.SetOverrides(ctx, f.ws.Ref.Namespace, allow); err != nil {
		t.Fatalf("namespace switch: %v", err)
	}

	if _, err := f.svc.SetOverrides(ctx, f.ws.ID, allow); err != nil {
		t.Fatalf("unit switch: %v", err)
	}

	svc := withManualLocks(t, f, answering(heldCollectionLock()))
	recorded := 0

	_, err := svc.BeginState(ctx, f.ws.ID, "alice", newState("lin-1", 1, smallDoc),
		authority(allow, func(workspace.Override) error { recorded++; return nil }))
	checkLockedError(t, err)

	if recorded != 0 {
		t.Errorf("override entries = %d, want 0: a refused write records no override", recorded)
	}

	// A tool lock taken before the manual lock stays usable for its holder.
	if _, lockErr := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); lockErr != nil {
		t.Fatalf("lock before the manual lock: %v", lockErr)
	}

	pending, err := svc.BeginState(ctx, f.ws.ID, "alice", newState("lin-1", 1, smallDoc), authority(allow, nil))
	if err != nil {
		t.Fatalf("declare under the lock of the caller: %v", err)
	}

	if pending.Version.UnlockedWrite {
		t.Error("declare under the lock of the caller carries the unlocked-write flag")
	}
}

// TestRestoreMeetsTheManualLock proves a restore that has to take the tool lock
// first fails while a manual lock covers the workspace, and that a restore under
// the lock of the caller is not refused.
func TestRestoreMeetsTheManualLock(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	mustLock(t, f, "alice")

	first := pushVersion(t, f, 1, "{\"v\":1}")
	pushVersion(t, f, 2, "{\"v\":2}")

	mustUnlock(t, f)

	svc := withManualLocks(t, f, answering(heldCollectionLock()))

	_, err := svc.Restore(ctx, f.ws.ID, first.ID, "alice")
	checkLockedError(t, err)
	checkHistoryTotal(t, f, 2)

	if _, lockErr := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); lockErr != nil {
		t.Fatalf("lock before the manual lock: %v", lockErr)
	}

	if _, err := svc.Restore(ctx, f.ws.ID, first.ID, "alice"); err != nil {
		t.Fatalf("restore under the lock of the caller: %v", err)
	}
}

// TestStreamedUnlockedWriteMeetsTheManualLock proves the streamed write of the
// OpenTofu HTTP backend, which names no lock ID, fails while a manual lock
// covers the workspace, even with the unlocked-write override on. It records no
// override entry and stores no state version. A streamed write under the lock
// of the caller is not refused.
func TestStreamedUnlockedWriteMeetsTheManualLock(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	allow := namespace.Overrides{Unlocked: true, Forced: false}

	if _, err := f.namespaces.SetOverrides(ctx, f.ws.Ref.Namespace, allow); err != nil {
		t.Fatalf("namespace switch: %v", err)
	}

	if _, err := f.svc.SetOverrides(ctx, f.ws.ID, allow); err != nil {
		t.Fatalf("unit switch: %v", err)
	}

	svc := withManualLocks(t, f, answering(heldCollectionLock()))
	recorded := 0

	_, err := svc.WriteState(
		ctx,
		f.ws.ID,
		workspace.StreamInput{Holder: "alice", LockID: "", MD5: ""},
		streamAuthority(allow, func(workspace.Override, int64) error { recorded++; return nil }),
		strings.NewReader(streamDoc("lin-m", 1)),
	)
	checkLockedError(t, err)

	if recorded != 0 {
		t.Errorf("override entries = %d, want 0: a refused write records no override", recorded)
	}

	checkHistoryTotal(t, f, 0)

	// A tool lock taken before the manual lock stays usable for its holder.
	if _, lockErr := f.svc.LockWithID(ctx, f.ws.ID, "alice", "apply", "lock-1"); lockErr != nil {
		t.Fatalf("lock before the manual lock: %v", lockErr)
	}

	_, err = svc.WriteState(
		ctx,
		f.ws.ID,
		workspace.StreamInput{Holder: "alice", LockID: "lock-1", MD5: ""},
		streamAuthority(allow, nil),
		strings.NewReader(streamDoc("lin-m", 1)),
	)
	if err != nil {
		t.Fatalf("streamed write under the lock of the caller: %v", err)
	}
}

// getFailingStore is a store whose read of a workspace by name fails.
type getFailingStore struct {
	*memStore
}

func (getFailingStore) GetWorkspace(context.Context, workspace.Ref) (workspace.Workspace, error) {
	return workspace.Workspace{}, errManualLockRead
}

// TestDeleteFailsClosedWhenTheWorkspaceCannotBeRead proves a delete does not
// skip the manual lock check when the read of the workspace fails with an error
// other than not found: it returns that error and deletes nothing.
func TestDeleteFailsClosedWhenTheWorkspaceCannotBeRead(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	svc, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        failing(manuallock.ErrNotLocked),
		Workspaces:         getFailingStore{f.store},
		States:             f.store,
		Blobs:              f.blobs,
		Namespaces:         f.namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             f.secrets,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      0,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	if delErr := svc.Delete(t.Context(), f.ws.Ref, retentionEntry()); !errors.Is(delErr, errManualLockRead) {
		t.Fatalf("Delete = %v, want the read error", delErr)
	}

	if _, getErr := f.svc.GetByID(t.Context(), f.ws.ID); getErr != nil {
		t.Errorf("workspace after the refused delete: %v", getErr)
	}
}

// TestRestoreMeetsTheNamespaceLock proves the restore of a deleted workspace
// fails while a manual lock covers its namespace, before the store is reached:
// the store check is not the only layer. The workspace stays deleted. A
// namespace with no lock restores it, and a lock that cannot be read refuses
// the restore.
func TestRestoreMeetsTheNamespaceLock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		locks        *fakeManualLocks
		wantErr      error
		wantRestored bool
	}{
		{
			"namespace locked",
			&fakeManualLocks{lock: heldNamespaceLock(), err: nil, asked: nil, passJob: "", jobs: nil, phases: nil},
			manuallock.ErrLocked, false,
		},
		{"namespace not locked", failing(manuallock.ErrNotLocked), nil, true},
		{"lock cannot be read", failing(errManualLockRead), errManualLockRead, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)

			if err := f.svc.Delete(t.Context(), f.ws.Ref, retentionEntry()); err != nil {
				t.Fatalf("Delete: %v", err)
			}

			svc := withManualLocks(t, f, tc.locks)

			_, err := svc.RestoreDeleted(t.Context(), f.ws.Ref.Namespace, f.ws.ID, "", retentionEntry())
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("RestoreDeleted = %v, want %v", err, tc.wantErr)
			}

			_, getErr := svc.GetByID(t.Context(), f.ws.ID)
			if restored := getErr == nil; restored != tc.wantRestored {
				t.Errorf("workspace live after RestoreDeleted(%v) = %v, want %v", err, restored, tc.wantRestored)
			}

			if errors.Is(tc.wantErr, manuallock.ErrLocked) {
				checkNamesAlice(t, err)
			}
		})
	}
}

// checkNamesAlice checks that err holds a LockedError that names alice.
func checkNamesAlice(t *testing.T, err error) {
	t.Helper()

	var locked *manuallock.LockedError
	if !errors.As(err, &locked) || locked.Lock.Holder != "alice" {
		t.Errorf("err = %v, want a LockedError that names the holder", err)
	}
}
