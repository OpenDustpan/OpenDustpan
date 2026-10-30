package workspace_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var errGuardRead = errors.New("registry unavailable")

// stateBlobCount returns the number of state blobs under prefix.
func stateBlobCount(t *testing.T, f fixture, prefix string) int {
	t.Helper()

	listed, err := f.blobs.List(t.Context(), prefix)
	if err != nil {
		t.Fatalf("list state blobs: %v", err)
	}

	return len(listed)
}

// TestDeletePurgedBlobsWaitsForRunningWork proves that the delete of the
// state blobs of a purged workspace keeps every blob while a backup or a
// restore runs, answers an error that names the work, and deletes the blobs
// after the work ended.
func TestDeletePurgedBlobsWaitsForRunningWork(t *testing.T) {
	t.Parallel()

	for name, work := range map[string]blobguard.Work{
		"a backup":  {Kind: blobguard.Backup, ID: "bkp-1"},
		"a restore": {Kind: blobguard.Restore, ID: "rst-1"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			guard := blobguardtest.NewGuard()
			f := newFixtureGuarded(t, openFixtureBlobs(t), guard)
			prefix := purgedWorkspace(t, f)

			guard.Start(work)

			err := f.svc.DeletePurgedBlobs(t.Context(), f.ws.NamespaceID, f.ws.ID)

			wait, isWait := errors.AsType[*blobguard.WaitError](err)
			if !isWait || wait.Work != work || !errors.Is(err, blobguard.ErrWait) {
				t.Fatalf("delete while %s runs: err = %v, want a WaitError for %v", name, err, work)
			}

			if stateBlobCount(t, f, prefix) == 0 {
				t.Fatalf("state blobs while %s runs = 0, want them kept", name)
			}

			guard.End()

			if err := f.svc.DeletePurgedBlobs(t.Context(), f.ws.NamespaceID, f.ws.ID); err != nil {
				t.Fatalf("delete after the work ended: %v", err)
			}

			if left := stateBlobCount(t, f, prefix); left != 0 {
				t.Fatalf("state blobs after the work ended = %d, want none", left)
			}
		})
	}
}

// TestDeletePurgedBlobsFailsClosed proves that a failed guard read, an answer
// with an unknown kind and a service with no guard delete nothing and answer an
// error that is not a wait.
func TestDeletePurgedBlobsFailsClosed(t *testing.T) {
	t.Parallel()

	failing := blobguardtest.NewGuard()
	failing.Fail(errGuardRead)

	unknown := blobguardtest.NewGuard()
	unknown.Start(blobguard.Work{Kind: "other", ID: "x-1"})

	tests := []struct {
		name  string
		guard blobguard.Watch
		want  error
	}{
		{name: "a failed read", guard: failing, want: errGuardRead},
		{name: "an unknown kind", guard: unknown, want: blobguard.ErrUnknown},
		{name: "no guard", guard: nil, want: blobguard.ErrNoWatch},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixtureGuarded(t, openFixtureBlobs(t), tc.guard)
			prefix := purgedWorkspace(t, f)

			err := f.svc.DeletePurgedBlobs(t.Context(), f.ws.NamespaceID, f.ws.ID)
			if !errors.Is(err, tc.want) || errors.Is(err, blobguard.ErrWait) {
				t.Fatalf("delete with %s: err = %v, want %v and no wait", tc.name, err, tc.want)
			}

			if errors.Is(err, workspace.ErrBlobsRemain) {
				t.Fatalf("delete with %s: err = %v, want no ErrBlobsRemain", tc.name, err)
			}

			listed, listErr := f.blobs.List(t.Context(), prefix)
			if listErr != nil || len(listed) == 0 {
				t.Fatalf("state blobs with %s = %d, %v, want them kept", tc.name, len(listed), listErr)
			}
		})
	}
}
