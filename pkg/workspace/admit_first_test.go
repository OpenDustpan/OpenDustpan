package workspace_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestAdmitFirstRequests proves the checks that run before a first LOCK or POST
// creates a workspace: each refusal is the error that the request meets after
// the create, and the check writes nothing.
func TestAdmitFirstRequests(t *testing.T) {
	t.Parallel()

	newRef := func(namespaceName string) workspace.Ref {
		name, err := workspace.ParseName("fresh")
		if err != nil {
			t.Fatalf("parse name: %v", err)
		}

		return workspace.Ref{Namespace: namespace.Name(namespaceName), Name: name}
	}

	lock := func(holder, lockID string) workspace.LockRequest {
		return workspace.LockRequest{Holder: holder, Reason: "plan", LockID: lockID, JobID: ""}
	}

	lockAt := func(ref workspace.Ref, holder, lockID string) func(*workspace.Service) error {
		return func(s *workspace.Service) error {
			return s.AdmitFirstLock(t.Context(), ref, lock(holder, lockID))
		}
	}

	writeAt := func(ref workspace.Ref, lockID string) func(*workspace.Service) error {
		return func(s *workspace.Service) error {
			return s.AdmitFirstWrite(t.Context(), ref, workspace.StreamInput{Holder: "ci", LockID: lockID, MD5: ""})
		}
	}

	acme, nowhere := newRef("acme"), newRef("nowhere")

	cases := []struct {
		name string
		call func(*workspace.Service) error
		ref  workspace.Ref
		want error
		// nsLocked puts a manual lock on the namespace.
		nsLocked bool
	}{
		{"LOCK that meets no refusal", lockAt(acme, "ci", "lock-1"), acme, nil, false},
		{
			"LOCK under a manual lock of the namespace", lockAt(acme, "ci", "lock-1"), acme,
			workspace.ErrLocked, true,
		},
		{"LOCK with a lock ID that is not valid", lockAt(acme, "ci", "a b"), acme, workspace.ErrInvalidLockID, false},
		{"LOCK with no holder", lockAt(acme, "", "lock-1"), acme, workspace.ErrNotLocked, false},
		{
			"LOCK in a namespace that does not exist",
			lockAt(nowhere, "ci", "lock-1"),
			nowhere,
			namespace.ErrNotFound,
			false,
		},
		{"POST with no lock ID", writeAt(acme, ""), acme, workspace.ErrOverrideRefused, false},
		{"POST that names a lock nobody holds", writeAt(acme, "x"), acme, workspace.ErrLockMismatch, false},
		{"POST in a namespace that does not exist", writeAt(nowhere, ""), nowhere, namespace.ErrNotFound, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)

			svc := f.svc
			if tc.nsLocked {
				svc = withManualLocks(t, f, answering(heldNamespaceLock()))
			}

			err := tc.call(svc)
			if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("admit = %v, want %v", err, tc.want)
			}

			if _, getErr := svc.Get(t.Context(), tc.ref); !errors.Is(getErr, workspace.ErrNotFound) {
				t.Fatalf("workspace after the check: Get = %v, want ErrNotFound", getErr)
			}
		})
	}
}

// TestAdmitFirstLockAppliesTheJobPhase proves the rule of the manual lock for
// the first LOCK of a new workspace: a job whose phase started before the lock
// of the namespace passes it, and the create goes on; a job whose phase started
// after the lock, a job with no known phase and a request with no job meet it.
func TestAdmitFirstLockAppliesTheJobPhase(t *testing.T) {
	t.Parallel()

	lockAt := heldNamespaceLock().Since

	cases := []struct {
		name  string
		jobID string
		want  error
	}{
		{"job that started before the lock", "job-before", nil},
		{"job that started after the lock", "job-after", workspace.ErrLocked},
		{"job that started at the time of the lock", "job-same", workspace.ErrLocked},
		{"job with no known phase", "job-unknown", workspace.ErrLocked},
		{"request with no job", "", workspace.ErrLocked},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			locks := answering(heldNamespaceLock())
			locks.phases = map[string]time.Time{
				"job-before": lockAt.Add(-time.Hour),
				"job-after":  lockAt.Add(time.Hour),
				"job-same":   lockAt,
			}
			svc := withManualLocks(t, f, locks)

			name, err := workspace.ParseName("fresh")
			if err != nil {
				t.Fatalf("parse name: %v", err)
			}

			ref := workspace.Ref{Namespace: "acme", Name: name}
			req := workspace.LockRequest{Holder: "ci", Reason: "plan", LockID: "lock-1", JobID: tc.jobID}

			got := svc.AdmitFirstLock(t.Context(), ref, req)
			if tc.want == nil && got != nil || tc.want != nil && !errors.Is(got, tc.want) {
				t.Fatalf("AdmitFirstLock = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCreatePassesTheJobPhaseToTheStore proves that Create hands the start of
// the phase of the job to the store, so the store applies the rule of the
// manual lock in the transaction of the create. A request with no job, and a
// job with no known phase, hand over the zero time, which passes no lock. The
// lock of a collection does not cover a workspace that does not exist yet, so
// the service lets each create reach the store.
func TestCreatePassesTheJobPhaseToTheStore(t *testing.T) {
	t.Parallel()

	lockAt := heldNamespaceLock().Since

	cases := []struct {
		name  string
		jobID string
		want  time.Time
	}{
		{"job with a phase", "job-1", lockAt.Add(-time.Hour)},
		{"job with no known phase", "job-2", time.Time{}},
		{"request with no job", "", time.Time{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			locks := answering(heldCollectionLock())
			locks.phases = map[string]time.Time{"job-1": lockAt.Add(-time.Hour)}
			svc := withManualLocks(t, f, locks)

			name, err := workspace.ParseName("fresh")
			if err != nil {
				t.Fatalf("parse name: %v", err)
			}

			ctx := workspace.WithJob(t.Context(), tc.jobID)

			ref := workspace.Ref{Namespace: "acme", Name: name}

			_, err = svc.Create(ctx, ref, memPlacement("acme"))
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			if got := f.store.lastLockPass; !got.Equal(tc.want) {
				t.Fatalf("the store got the lock pass %v, want %v", got, tc.want)
			}
		})
	}
}
