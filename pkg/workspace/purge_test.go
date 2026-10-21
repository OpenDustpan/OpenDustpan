package workspace_test

import (
	"errors"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// purgedWorkspace pushes one state version to the fixture workspace, then
// deletes and purges the workspace. It returns the prefix of its state
// blobs, which still hold the state after the purge of the rows.
func purgedWorkspace(t *testing.T, f fixture) string {
	t.Helper()

	if _, err := f.svc.Lock(t.Context(), f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if err := f.push(t, newState("lineage-1", 1, `{"v":1}`), `{"v":1}`); err != nil {
		t.Fatalf("push state: %v", err)
	}

	if _, err := f.svc.Unlock(t.Context(), f.ws.ID, "alice"); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	if err := f.svc.Delete(t.Context(), f.ws.Ref, retentionEntry()); err != nil {
		t.Fatalf("delete: %v", err)
	}

	purge := retentionEntry()
	purge.Action = "unit:purge"

	if _, err := f.svc.PurgeDeleted(t.Context(), "acme", f.ws.ID, purge); err != nil {
		t.Fatalf("purge: %v", err)
	}

	prefix := f.stateDir(t, f.ws.ID)

	listed, err := f.blobs.List(t.Context(), prefix)
	if err != nil || len(listed) == 0 {
		t.Fatalf("state blobs after the purge of the rows = %d, %v, want the pushed state", len(listed), err)
	}

	return prefix
}

// TestDeletePurgedBlobsRemovesTheState proves that no state blob of a purged
// workspace outlives the blob delete, and that a transient failure is
// retried for each blob.
func TestDeletePurgedBlobsRemovesTheState(t *testing.T) {
	t.Parallel()

	flaky := &flakyBlobs{Store: openFixtureBlobs(t), mu: sync.Mutex{}, fails: 2}
	f := newFixtureWithBlobs(t, flaky)
	f.blobs = flaky

	prefix := purgedWorkspace(t, f)

	if err := f.svc.DeletePurgedBlobs(t.Context(), f.ws.NamespaceID, f.ws.ID); err != nil {
		t.Fatalf("delete purged blobs: %v", err)
	}

	listed, err := f.blobs.List(t.Context(), prefix)
	if err != nil || len(listed) != 0 {
		t.Fatalf("state blobs after the delete = %d, %v, want none", len(listed), err)
	}
}

// TestDeletePurgedBlobsReportsARemainingBlob proves that a blob that stays
// after every retry answers ErrBlobsRemain, so the caller keeps the blob
// purge record, and that a later run removes it.
func TestDeletePurgedBlobsReportsARemainingBlob(t *testing.T) {
	t.Parallel()

	flaky := &flakyBlobs{Store: openFixtureBlobs(t), mu: sync.Mutex{}, fails: 0}
	f := newFixtureWithBlobs(t, flaky)
	f.blobs = flaky

	prefix := purgedWorkspace(t, f)

	flaky.mu.Lock()
	flaky.fails = 1000
	flaky.mu.Unlock()

	err := f.svc.DeletePurgedBlobs(t.Context(), f.ws.NamespaceID, f.ws.ID)
	if !errors.Is(err, workspace.ErrBlobsRemain) {
		t.Fatalf("delete purged blobs: err = %v, want ErrBlobsRemain", err)
	}

	flaky.mu.Lock()
	flaky.fails = 0
	flaky.mu.Unlock()

	if err = f.svc.DeletePurgedBlobs(t.Context(), f.ws.NamespaceID, f.ws.ID); err != nil {
		t.Fatalf("second delete of purged blobs: %v", err)
	}

	listed, err := f.blobs.List(t.Context(), prefix)
	if err != nil || len(listed) != 0 {
		t.Fatalf("state blobs after the second delete = %d, %v, want none", len(listed), err)
	}
}
