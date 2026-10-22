package workspace_test

import (
	"errors"
	"testing"

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
