package jobtest_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/job/jobtest"
)

// confirmSpy is a job.RecordStore that records the stored values of each
// confirm of a log chunk. It fails the first confirm after it recorded the
// values, as a store does when the connection ends before the commit.
type confirmSpy struct {
	job.RecordStore

	mu   sync.Mutex
	seen []blob.Digest
}

// ConfirmLogChunk implements job.RecordStore.
func (c *confirmSpy) ConfirmLogChunk(ctx context.Context, pc job.PhaseClaim, cc job.ChunkConfirm) error {
	c.mu.Lock()
	c.seen = append(c.seen, cc.Stored)
	first := len(c.seen) == 1
	c.mu.Unlock()

	if first {
		return errInjected
	}

	return c.RecordStore.ConfirmLogChunk(ctx, pc, cc)
}

// TestRecorderChunkDigestOfStoredBlob proves that the stored size and MD5 that
// the recorder gives to the confirm are those of the blob in the blob store,
// also for a retry. The seal of each attempt differs, and the first blob stays,
// so the retry must read the digest from the stored blob.
func TestRecorderChunkDigestOfStoredBlob(t *testing.T) {
	t.Parallel()

	db := newMemDB()
	keys := newMemKeyIndex(db)
	db.itemKeys = keys

	inner, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := inner.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	spy := &confirmSpy{RecordStore: db, mu: sync.Mutex{}, seen: nil}

	h := jobtest.NewRecorderHarness(t, jobtest.RecorderParts{
		Harness: jobtest.Harness{
			Jobs: db, Durable: db.durable, Freezes: db, Claimer: job.NewClaimer(db, db.durable, db), Namespaces: db,
			Domain: db,
		},
		Records:  spy,
		ItemKeys: keys,
		Blobs:    inner,
		Wrapper:  nil,
	})
	a := planAssignment(t, &h)

	if beginErr := h.Recorder.BeginStep(t.Context(), a.Claim, "plan"); beginErr != nil {
		t.Fatalf("BeginStep = %v", beginErr)
	}

	err = h.Recorder.AppendLog(t.Context(), a.Claim, "plan", 0, strings.NewReader("line\n"))
	if !errors.Is(err, errInjected) {
		t.Fatalf("AppendLog = %v, want the failure of the confirm", err)
	}

	if err = h.Recorder.AppendLog(t.Context(), a.Claim, "plan", 0, strings.NewReader("line\n")); err != nil {
		t.Fatalf("retry of the chunk = %v", err)
	}

	var chunkKey string

	for _, key := range listJobBlobs(t, &h) {
		if strings.HasSuffix(key, "/00000000") {
			chunkKey = key
		}
	}

	parsed, err := blob.ParseKey(chunkKey)
	if err != nil {
		t.Fatalf("key of the chunk blob %q: %v", chunkKey, err)
	}

	rc, err := inner.Get(t.Context(), parsed)
	if err != nil {
		t.Fatalf("get the chunk blob: %v", err)
	}

	want, digestErr := blob.DigestOf(rc)
	if closeErr := rc.Close(); digestErr != nil || closeErr != nil {
		t.Fatalf("digest of the chunk blob: %v", errors.Join(digestErr, closeErr))
	}

	spy.mu.Lock()
	defer spy.mu.Unlock()

	if len(spy.seen) != 2 {
		t.Fatalf("confirms = %d, want 2", len(spy.seen))
	}

	for i, got := range spy.seen {
		if got != want {
			t.Errorf("confirm %d got %+v, want the digest %+v of the stored blob", i+1, got, want)
		}
	}
}
