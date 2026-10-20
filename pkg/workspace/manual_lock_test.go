package workspace_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// errManualLockRead is the failure of a store that cannot read a manual lock.
var errManualLockRead = errors.New("manual lock store down")

// fakeManualLocks answers the manual lock of one unit, or an error. It records
// the units that the service asked about.
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

// answering returns a port that answers with lock.
func answering(lock manuallock.Lock) *fakeManualLocks {
	return &fakeManualLocks{lock: lock, err: nil, asked: nil, passJob: "", jobs: nil}
}

// failing returns a port that answers with err.
func failing(err error) *fakeManualLocks {
	return &fakeManualLocks{lock: heldCollectionLock(), err: err, asked: nil, passJob: "", jobs: nil}
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
		ManualLocks:        locks,
		Workspaces:         f.store,
		States:             f.store,
		Blobs:              f.blobs,
		Namespaces:         f.namespaces,
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

			wantUnit := collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: f.ws.Ref.Name.String()}
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

// TestLockRequestOfAnEarlierJobPhasePasses proves the job ID of the request
// reaches the manual lock port, that a request the port lets pass takes the
// tool lock, and that a request with another job ID or none meets the lock.
func TestLockRequestOfAnEarlierJobPhasePasses(t *testing.T) {
	t.Parallel()

	requests := []struct {
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

				_, err := s.LockWithIDForJob(t.Context(), f.ws.ID, "job-1", "plan", "lock-1", jobID)

				return err
			},
		},
	}

	for _, req := range requests {
		for _, tc := range []struct {
			name      string
			jobID     string
			wantError bool
		}{
			{name: "job that started before the lock", jobID: "job-1", wantError: false},
			{name: "another job", jobID: "job-2", wantError: true},
			{name: "no job token", jobID: "", wantError: true},
		} {
			t.Run(req.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				f := newFixture(t)
				locks := answering(heldCollectionLock())
				locks.passJob = "job-1"
				svc := withManualLocks(t, f, locks)

				err := req.call(t, svc, f, tc.jobID)
				if tc.wantError {
					checkLockedError(t, err)
				} else if err != nil {
					t.Fatalf("lock request of the earlier phase = %v, want it to pass", err)
				}

				if len(locks.jobs) != 1 || locks.jobs[0] != tc.jobID {
					t.Errorf("job IDs asked = %q, want [%q]", locks.jobs, tc.jobID)
				}

				got, getErr := svc.GetByID(t.Context(), f.ws.ID)
				if getErr != nil || got.Lock.Locked() == tc.wantError {
					t.Errorf("tool lock after the request = %+v, %v; held = %v, want %v",
						got.Lock, getErr, got.Lock.Locked(), !tc.wantError)
				}
			})
		}
	}
}
