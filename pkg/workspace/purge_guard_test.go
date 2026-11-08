package workspace_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var errGuardRead = errors.New("registry unavailable")

// stateBlobCount returns the number of state blobs under prefix.
func stateBlobCount(t *testing.T, f fixture, prefix string) int {
	t.Helper()

	listed, err := blobtest.ListAll(t.Context(), f.blobs, prefix)
	if err != nil {
		t.Fatalf("list state blobs: %v", err)
	}

	return len(listed)
}

// requireWaitForWork fails the test unless err is a wait for work.
func requireWaitForWork(t *testing.T, err error, name string, work blobguard.Work) {
	t.Helper()

	wait, isWait := errors.AsType[*blobguard.WaitError](err)
	if !isWait || wait.Work != work || !errors.Is(err, blobguard.ErrWait) {
		t.Fatalf("delete while %s runs: err = %v, want a WaitError for %v", name, err, work)
	}
}

// requireLeaseReleased fails the test unless the delete asked for a lease and
// no lease is live.
func requireLeaseReleased(t *testing.T, guard *blobguardtest.Leases, when string) {
	t.Helper()

	if guard.Takes() == 0 {
		t.Fatalf("the delete took no lease %s, want one take for each delete", when)
	}

	if live, err := guard.LiveLeases(t.Context()); err != nil || live != 0 {
		t.Fatalf("live leases %s = %d, %v, want none", when, live, err)
	}
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

			guard := blobguardtest.NewLeases(time.Now)
			f := newFixtureGuarded(t, openFixtureBlobs(t), guard)
			prefix := purgedWorkspace(t, f)

			guard.Start(work)

			requireWaitForWork(t, f.svc.DeletePurgedBlobs(t.Context(), f.ws.NamespaceID, f.ws.ID), name, work)
			requireLeaseReleased(t, guard, "while "+name+" runs")

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

	failing := blobguardtest.NewLeases(time.Now)
	failing.Fail(errGuardRead)

	unknown := blobguardtest.NewLeases(time.Now)
	unknown.Start(blobguard.Work{Kind: "other", ID: "x-1"})

	tests := []struct {
		name  string
		guard blobguard.Leases
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

			listed, listErr := blobtest.ListAll(t.Context(), f.blobs, prefix)
			if listErr != nil || len(listed) == 0 {
				t.Fatalf("state blobs with %s = %d, %v, want them kept", tc.name, len(listed), listErr)
			}
		})
	}
}

// TestDeletePurgedBlobsReleasesTheLease proves that each delete takes a lease
// and releases it: the blobs go, and no lease is live after the call.
func TestDeletePurgedBlobsReleasesTheLease(t *testing.T) {
	t.Parallel()

	guard := blobguardtest.NewLeases(time.Now)
	f := newFixtureGuarded(t, openFixtureBlobs(t), guard)
	prefix := purgedWorkspace(t, f)

	if err := f.svc.DeletePurgedBlobs(t.Context(), f.ws.NamespaceID, f.ws.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if left := stateBlobCount(t, f, prefix); left != 0 {
		t.Fatalf("state blobs after the delete = %d, want none", left)
	}

	requireLeaseReleased(t, guard, "after the delete")
}

// TestDeletePurgedBlobsReadsTheGuardBeforeEachDelete proves that a restore
// which starts between two deletes of one purge keeps the blob that is left:
// the guard is read again before each delete.
func TestDeletePurgedBlobsReadsTheGuardBeforeEachDelete(t *testing.T) {
	t.Parallel()

	guard := blobguardtest.NewLeases(time.Now)
	f := newFixtureGuarded(t, openFixtureBlobs(t), guard)
	prefix := purgedWorkspace(t, f)

	extra, err := blob.ParseKey(prefix + "extra")
	if err != nil {
		t.Fatalf("parse extra key: %v", err)
	}

	if err = f.blobs.Put(t.Context(), extra, strings.NewReader("extra")); err != nil {
		t.Fatalf("write extra blob: %v", err)
	}

	guard.OnRead(func() {
		if guard.Calls() == 2 {
			guard.Start(blobguard.Work{Kind: blobguard.Restore, ID: "rst-1"})
		}
	})

	err = f.svc.DeletePurgedBlobs(t.Context(), f.ws.NamespaceID, f.ws.ID)
	if !errors.Is(err, blobguard.ErrWait) {
		t.Fatalf("delete with a restore that starts in the loop: err = %v, want a wait", err)
	}

	if left := stateBlobCount(t, f, prefix); left != 1 {
		t.Fatalf("state blobs left after the restore started = %d, want exactly one", left)
	}
}
