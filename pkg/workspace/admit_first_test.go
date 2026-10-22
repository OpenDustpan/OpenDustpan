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

	cases := []struct {
		name string
		call func(*workspace.Service, workspace.Ref) error
		ref  workspace.Ref
		want error
		// nsLocked puts a manual lock on the namespace.
		nsLocked bool
	}{
		{
			name: "LOCK that meets no refusal",
			call: func(s *workspace.Service, r workspace.Ref) error {
				return s.AdmitFirstLock(t.Context(), r, lock("ci", "lock-1"))
			},
			ref: newRef("acme"), want: nil,
		},
		{
			name: "LOCK under a manual lock of the namespace",
			call: func(s *workspace.Service, r workspace.Ref) error {
				return s.AdmitFirstLock(t.Context(), r, lock("ci", "lock-1"))
			},
			ref: newRef("acme"), want: workspace.ErrLocked, nsLocked: true,
		},
		{
			name: "LOCK with a lock ID that is not valid",
			call: func(s *workspace.Service, r workspace.Ref) error {
				return s.AdmitFirstLock(t.Context(), r, lock("ci", "a b"))
			},
			ref: newRef("acme"), want: workspace.ErrInvalidLockID,
		},
		{
			name: "LOCK with no holder",
			call: func(s *workspace.Service, r workspace.Ref) error {
				return s.AdmitFirstLock(t.Context(), r, lock("", "lock-1"))
			},
			ref: newRef("acme"), want: workspace.ErrNotLocked,
		},
		{
			name: "LOCK in a namespace that does not exist",
			call: func(s *workspace.Service, r workspace.Ref) error {
				return s.AdmitFirstLock(t.Context(), r, lock("ci", "lock-1"))
			},
			ref: newRef("nowhere"), want: namespace.ErrNotFound,
		},
		{
			name: "POST with no lock ID",
			call: func(s *workspace.Service, r workspace.Ref) error {
				return s.AdmitFirstWrite(t.Context(), r, workspace.StreamInput{Holder: "ci", LockID: "", MD5: ""})
			},
			ref: newRef("acme"), want: workspace.ErrOverrideRefused,
		},
		{
			name: "POST that names a lock nobody holds",
			call: func(s *workspace.Service, r workspace.Ref) error {
				return s.AdmitFirstWrite(t.Context(), r, workspace.StreamInput{Holder: "ci", LockID: "x", MD5: ""})
			},
			ref: newRef("acme"), want: workspace.ErrLockMismatch,
		},
		{
			name: "POST in a namespace that does not exist",
			call: func(s *workspace.Service, r workspace.Ref) error {
				return s.AdmitFirstWrite(t.Context(), r, workspace.StreamInput{Holder: "ci", LockID: "", MD5: ""})
			},
			ref: newRef("nowhere"), want: namespace.ErrNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)

			svc := f.svc
			if tc.nsLocked {
				svc = withManualLocks(t, f, answering(heldNamespaceLock()))
			}

			err := tc.call(svc, tc.ref)
			if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("admit = %v, want %v", err, tc.want)
			}

			if _, getErr := svc.Get(t.Context(), tc.ref); !errors.Is(getErr, workspace.ErrNotFound) {
				t.Fatalf("workspace after the check: Get = %v, want ErrNotFound", getErr)
			}
		})
	}
}
