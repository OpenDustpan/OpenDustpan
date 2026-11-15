package jobtest_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/job/jobtest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// confirmSpy is a job.RecordStore that records each confirm of a log chunk
// before it passes it on. A confirm whose number n (from 1) makes hook return
// an error fails with that error, and the spy passes nothing on: the hook
// can also block, to order two writers.
type confirmSpy struct {
	job.RecordStore

	mu   sync.Mutex
	seen []job.ChunkConfirm
	hook func(n int, cc job.ChunkConfirm) error
}

// ConfirmLogChunk implements job.RecordStore.
func (c *confirmSpy) ConfirmLogChunk(ctx context.Context, pc job.PhaseClaim, cc job.ChunkConfirm) (bool, error) {
	c.mu.Lock()
	c.seen = append(c.seen, cc)
	n := len(c.seen)
	c.mu.Unlock()

	if c.hook != nil {
		if err := c.hook(n, cc); err != nil {
			return false, err
		}
	}

	return c.RecordStore.ConfirmLogChunk(ctx, pc, cc)
}

// confirms returns the confirms that the spy saw.
func (c *confirmSpy) confirms() []job.ChunkConfirm {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.Clone(c.seen)
}

// replacingStore is a blob.Store whose Put replaces the object of an existing
// key with no error, as a store that ignores If-None-Match does.
type replacingStore struct {
	blob.Store
}

// Put implements blob.Store.
func (s *replacingStore) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read the body: %w", err)
	}

	err = s.Store.Put(ctx, key, bytes.NewReader(data))
	if !errors.Is(err, blob.ErrExists) {
		return err
	}

	if err = s.Delete(ctx, key); err != nil {
		return fmt.Errorf("replace %s: %w", key, err)
	}

	return s.Store.Put(ctx, key, bytes.NewReader(data))
}

// newSpyHarness returns a recorder harness over the confirm spy and a blob
// store in a temporary directory that replaces an object on a second write of
// its key, as a store that ignores conditional writes does. It returns the
// harness, the spy and the store under the replacing wrapper.
func newSpyHarness(
	t *testing.T, hook func(n int, cc job.ChunkConfirm) error,
) (jobtest.RecorderHarness, *confirmSpy, *blobfs.Store) {
	t.Helper()

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

	spy := &confirmSpy{RecordStore: db, mu: sync.Mutex{}, seen: nil, hook: hook}

	h := jobtest.NewRecorderHarness(t, jobtest.RecorderParts{
		Harness: jobtest.Harness{
			Jobs:       db,
			Durable:    db.durable,
			Freezes:    db,
			Claimer:    job.NewClaimer(db, db.durable, db, jobtest.NoGates{}),
			Namespaces: db,
			Domain:     db,
			WriteState: nil,
		},
		Records:  spy,
		ItemKeys: keys,
		Blobs:    &replacingStore{Store: inner},
		Wrapper:  nil,
	})

	return h, spy, inner
}

// chunkZeroKeys returns the keys of the blobs of chunk 0 of the job: the blob of
// every attempt.
func chunkZeroKeys(t *testing.T, h *jobtest.RecorderHarness) []string {
	t.Helper()

	var out []string

	for _, key := range listJobBlobs(t, h) {
		if strings.Contains(key, "/log/") && strings.Contains(key, "/00000000") {
			out = append(out, key)
		}
	}

	return out
}

// digestOfKey returns the digest of the blob under key in the store.
func digestOfKey(t *testing.T, store blob.Store, key string) blob.Digest {
	t.Helper()

	parsed, err := blob.ParseKey(key)
	if err != nil {
		t.Fatalf("key of the chunk blob %q: %v", key, err)
	}

	rc, err := store.Get(t.Context(), parsed)
	if err != nil {
		t.Fatalf("get the chunk blob: %v", err)
	}

	digest, digestErr := blob.DigestOf(rc)
	if closeErr := rc.Close(); digestErr != nil || closeErr != nil {
		t.Fatalf("digest of the chunk blob: %v", errors.Join(digestErr, closeErr))
	}

	return digest
}

// chunkZeroRow returns the blob key that the row of chunk 0 of the log of the
// step plan holds.
func chunkZeroRow(t *testing.T, h *jobtest.RecorderHarness, a *job.Assignment) zeroRow {
	t.Helper()

	version := mustGetJob(t, h, a.Claim.Job).Phases[0].Steps[0].Log.ID

	rec, err := h.Records.Log(t.Context(), "acme", version)
	if err != nil || len(rec.ChunkKeys) != 1 {
		t.Fatalf("Log = %+v, %v; want one chunk", rec, err)
	}

	return zeroRow{version: version, key: rec.ChunkKeys[0]}
}

// zeroRow is the log version of chunk 0 and the blob key that its row holds.
type zeroRow struct {
	version string
	key     string
}

// TestRecorderChunkRetryUsesItsOwnKey proves that a retry of a chunk after a
// confirm that failed writes a blob under a new key, and that the row and the
// digest agree with that blob. The first blob is named by no row and stays for
// the orphan cleanup.
func TestRecorderChunkRetryUsesItsOwnKey(t *testing.T) {
	t.Parallel()

	h, spy, inner := newSpyHarness(t, func(n int, _ job.ChunkConfirm) error {
		if n == 1 {
			return errInjected
		}

		return nil
	})
	a := planAssignment(t, &h)

	if err := h.Recorder.BeginStep(t.Context(), a.Claim, "plan"); err != nil {
		t.Fatalf("BeginStep = %v", err)
	}

	err := h.Recorder.AppendLog(t.Context(), a.Claim, "plan", 0, strings.NewReader("line\n"))
	if !errors.Is(err, errInjected) {
		t.Fatalf("AppendLog = %v, want the failure of the confirm", err)
	}

	if err = h.Recorder.AppendLog(t.Context(), a.Claim, "plan", 0, strings.NewReader("line\n")); err != nil {
		t.Fatalf("retry of the chunk = %v", err)
	}

	seen := spy.confirms()
	if len(seen) != 2 || seen[0].BlobKey == "" || seen[0].BlobKey == seen[1].BlobKey {
		t.Fatalf("confirms = %+v, want two with two keys", seen)
	}

	row := chunkZeroRow(t, &h, a).key
	if row != seen[1].BlobKey {
		t.Fatalf("row key = %q, want the key of the retry %q", row, seen[1].BlobKey)
	}

	// The blob of the failed attempt stays: the confirm error does not prove a
	// rollback.
	keys := chunkZeroKeys(t, &h)
	if len(keys) != 2 {
		t.Fatalf("blobs of chunk 0 = %q, want the blob of each attempt", keys)
	}

	at := slices.IndexFunc(keys, func(k string) bool { return strings.HasSuffix(k, row) })
	if at < 0 {
		t.Fatalf("no blob for the key %q of the row in %q", row, keys)
	}

	if got := digestOfKey(t, inner, keys[at]); got != seen[1].Stored {
		t.Errorf("digest of the blob of the row = %+v, the confirm said %+v", got, seen[1].Stored)
	}
}

// TestRecorderChunkRaceOnReplacingStore runs two attempts of one chunk on a
// blob store that replaces an object, and holds the confirm of the first
// attempt until the second attempt committed. The row, the blob and the digest
// of the row agree after the race, and the first attempt deleted only its own
// blob.
func TestRecorderChunkRaceOnReplacingStore(t *testing.T) {
	t.Parallel()

	reached := make(chan struct{})
	release := make(chan struct{})

	h, spy, inner := newSpyHarness(t, func(n int, _ job.ChunkConfirm) error {
		if n == 1 {
			close(reached)
			<-release
		}

		return nil
	})
	a := planAssignment(t, &h)

	if err := h.Recorder.BeginStep(t.Context(), a.Claim, "plan"); err != nil {
		t.Fatalf("BeginStep = %v", err)
	}

	first := make(chan error, 1)

	go func() {
		first <- h.Recorder.AppendLog(t.Context(), a.Claim, "plan", 0, strings.NewReader("line\n"))
	}()

	<-reached

	// The second attempt reserves a row that is not confirmed, stores its own
	// blob and commits first.
	if err := h.Recorder.AppendLog(t.Context(), a.Claim, "plan", 0, strings.NewReader("line\n")); err != nil {
		t.Fatalf("second attempt = %v", err)
	}

	close(release)

	if err := <-first; err != nil {
		t.Fatalf("first attempt = %v, want nil: losing the race is not an error", err)
	}

	checkRaceOutcome(t, &h, spy, inner, a)
}

// checkRaceOutcome checks that the row of chunk 0 names the blob of the attempt
// that committed first, that this blob is the only blob of the chunk, and that
// its digest is the one the row holds.
func checkRaceOutcome(
	t *testing.T, h *jobtest.RecorderHarness, spy *confirmSpy, store blob.Store, a *job.Assignment,
) {
	t.Helper()

	seen := spy.confirms()
	if len(seen) != 2 || seen[0].BlobKey == seen[1].BlobKey {
		t.Fatalf("confirms = %+v, want two with two keys", seen)
	}

	// The second attempt reached the store first, so it is the second confirm
	// in the list and it won.
	zero := chunkZeroRow(t, h, a)
	version, row := zero.version, zero.key
	if row != seen[1].BlobKey {
		t.Fatalf("row key = %q, want the key of the attempt that committed first (%q)", row, seen[1].BlobKey)
	}

	keys := chunkZeroKeys(t, h)
	if len(keys) != 1 || !strings.HasSuffix(keys[0], row) {
		t.Fatalf("blobs of chunk 0 = %q, want only the blob of the row %q", keys, row)
	}

	if got := digestOfKey(t, store, keys[0]); got != seen[1].Stored {
		t.Errorf("digest of the blob of the row = %+v, the row says %+v", got, seen[1].Stored)
	}

	rc, err := h.Reader.OpenLog(t.Context(), "acme", version)
	if err != nil {
		t.Fatalf("OpenLog = %v", err)
	}

	if got, readErr := io.ReadAll(rc); readErr != nil || string(got) != "line\n" {
		t.Errorf("log = %q, %v; want %q", got, readErr, "line\n")
	}
}

// countingJobs counts the reads of a job.
type countingJobs struct {
	job.Store

	reads *atomic.Int64
}

// Get implements job.Store.
func (c *countingJobs) Get(ctx context.Context, id job.ID) (job.Job, error) {
	c.reads.Add(1)

	return c.Store.Get(ctx, id)
}

// countingRecords counts the calls of the record store that each open one
// database transaction.
type countingRecords struct {
	job.RecordStore

	calls map[string]*atomic.Int64
}

// ReserveLogChunk implements job.RecordStore.
func (c *countingRecords) ReserveLogChunk(
	ctx context.Context, pc job.PhaseClaim, step string, ch job.ChunkReserve, nl *job.NewLog,
) (job.ChunkPlan, error) {
	c.calls["ReserveLogChunk"].Add(1)

	return c.RecordStore.ReserveLogChunk(ctx, pc, step, ch, nl)
}

// ConfirmLogChunk implements job.RecordStore.
func (c *countingRecords) ConfirmLogChunk(ctx context.Context, pc job.PhaseClaim, cc job.ChunkConfirm) (bool, error) {
	c.calls["ConfirmLogChunk"].Add(1)

	return c.RecordStore.ConfirmLogChunk(ctx, pc, cc)
}

// Log implements job.RecordStore.
func (c *countingRecords) Log(ctx context.Context, ns namespace.Name, version string) (job.ArtifactRecord, error) {
	c.calls["Log"].Add(1)

	return c.RecordStore.Log(ctx, ns, version)
}

// TestRecorderAppendLogRoundTrips pins the database round trips of the hot
// path: AppendLog of a chunk reads the job once and runs two transactions, the
// reserve and the confirm. The decision of which attempt wins is made in the
// confirm, so it adds no round trip.
func TestRecorderAppendLogRoundTrips(t *testing.T) {
	t.Parallel()

	reads := new(atomic.Int64)
	records := &countingRecords{RecordStore: nil, calls: map[string]*atomic.Int64{
		"ReserveLogChunk": new(atomic.Int64), "ConfirmLogChunk": new(atomic.Int64), "Log": new(atomic.Int64),
	}}

	db := newMemDB()
	keys := newMemKeyIndex(db)
	db.itemKeys = keys
	records.RecordStore = db

	inner, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() { _ = inner.Close() }) //nolint:errcheck // Test cleanup.

	h := jobtest.NewRecorderHarness(t, jobtest.RecorderParts{
		Harness: jobtest.Harness{
			Jobs: &countingJobs{Store: db, reads: reads}, Durable: db.durable, Freezes: db,
			Claimer: job.NewClaimer(db, db.durable, db, jobtest.NoGates{}), Namespaces: db, Domain: db,
			WriteState: nil,
		},
		Records: records, ItemKeys: keys, Blobs: inner, Wrapper: nil,
	})
	a := planAssignment(t, &h)

	if err = h.Recorder.BeginStep(t.Context(), a.Claim, "plan"); err != nil {
		t.Fatalf("BeginStep = %v", err)
	}

	for seq := range 3 {
		reads.Store(0)

		for _, n := range records.calls {
			n.Store(0)
		}

		if err = h.Recorder.AppendLog(t.Context(), a.Claim, "plan", seq, strings.NewReader("line\n")); err != nil {
			t.Fatalf("AppendLog(%d) = %v", seq, err)
		}

		got := fmt.Sprintf("job reads %d, reserves %d, confirms %d, log reads %d", reads.Load(),
			records.calls["ReserveLogChunk"].Load(), records.calls["ConfirmLogChunk"].Load(),
			records.calls["Log"].Load())
		if want := "job reads 1, reserves 1, confirms 1, log reads 0"; got != want {
			t.Errorf("AppendLog(%d) made %s; want %s", seq, got, want)
		}
	}
}
