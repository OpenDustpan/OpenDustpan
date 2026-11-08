package stack_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/stack"
)

// errNoDatabaseClock reports a store that cannot read the clock of the
// database.
var errNoDatabaseClock = errors.New("the clock of the database does not answer")

// errMarksRead reports a store that cannot read the superseded marks.
var errMarksRead = errors.New("the marks read failed")

// requireSweepResult checks the result of one sweep.
func requireSweepResult(
	t *testing.T, res stack.SupersededSweepResult, err error, want stack.SupersededSweepResult, when string,
) {
	t.Helper()

	if err != nil || res != want {
		t.Fatalf("sweep %s = %+v, %v; want %+v, nil", when, res, err, want)
	}
}

// requireBlobStored checks that the blob under key is in the store.
func requireBlobStored(t *testing.T, f updatesFixture, key blob.Key, when string) {
	t.Helper()

	rc, err := f.blobs.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("blob %q %s: %v, want it stored", key, when, err)
	}

	if closeErr := rc.Close(); closeErr != nil {
		t.Fatalf("close blob %q: %v", key, closeErr)
	}
}

// requireBlobGone checks that no blob is stored under key.
func requireBlobGone(t *testing.T, f updatesFixture, key blob.Key, when string) {
	t.Helper()

	if _, err := f.blobs.Get(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("blob %q %s: %v, want ErrNotFound", key, when, err)
	}
}

// marksOf returns the marks of the fixture stack.
func marksOf(t *testing.T, f updatesFixture) []stack.SupersededBlob {
	t.Helper()

	marks, err := f.store.SupersededBlobs(t.Context(), "stk-test")
	if err != nil {
		t.Fatalf("marks of the stack: %v", err)
	}

	return marks
}

// onlyMarkOf returns the only mark of the fixture stack.
func onlyMarkOf(t *testing.T, f updatesFixture) stack.SupersededBlob {
	t.Helper()

	marks := marksOf(t, f)
	if len(marks) != 1 {
		t.Fatalf("marks of the stack = %+v, want one", marks)
	}

	return marks[0]
}

// saveTwoCheckpoints starts an update and saves two checkpoints, so the
// second save marks the blob of the first.
func saveTwoCheckpoints(t *testing.T, f updatesFixture) (stack.Started, stack.SupersededBlob) {
	t.Helper()

	s := f.started(t, stack.KindUpdate)
	f.saveSeq(t, s, 1, `{"a":1}`)
	f.saveSeq(t, s, 2, `{"a":2}`)

	return s, onlyMarkOf(t, f)
}

// TestSupersededSweepWaitsForWork proves that the sweep deletes nothing while
// a backup or a restore runs, and deletes the marked blob after the work
// ended.
func TestSupersededSweepWaitsForWork(t *testing.T) {
	t.Parallel()

	for name, work := range runningWorks() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			clk := newClock()
			guard := blobguardtest.NewLeases(clk.Now)
			f := newUpdatesGuarded(t, clk, nil, nil, guard)

			_, mark := saveTwoCheckpoints(t, f)
			f.clock.Advance(2 * blob.UnnamedGracePeriod)

			guard.Start(work)

			_, err := f.svc.SweepSuperseded(t.Context())
			if !errors.Is(err, blobguard.ErrWait) {
				t.Fatalf("sweep while %s runs: %v, want blobguard.ErrWait", name, err)
			}

			requireBlobStored(t, f, mark.Key, "while "+name+" runs")

			if got := marksOf(t, f); len(got) != 1 || got[0].Key != mark.Key {
				t.Fatalf("marks while %s runs = %+v, want the mark of %q", name, got, mark.Key)
			}

			guard.End()

			res, err := f.svc.SweepSuperseded(t.Context())
			requireSweepResult(t, res, err,
				stack.SupersededSweepResult{Deleted: 1, DeletedBytes: mark.Size, Kept: 0, Failed: 0},
				"after "+name+" ended")
			requireBlobGone(t, f, mark.Key, "after "+name+" ended")

			if got := marksOf(t, f); len(got) != 0 {
				t.Fatalf("marks after %s ended = %+v, want none", name, got)
			}
		})
	}
}

// TestSupersededSweepKeepsNamedBlob proves that a checkpoint row that names
// the marked key again keeps the blob, and that the sweep drops the mark.
func TestSupersededSweepKeepsNamedBlob(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	s, mark := saveTwoCheckpoints(t, f)

	deployment := []byte(`{"deployment":{"named":true}}`)
	cp := stack.Checkpoint{
		NamespaceID:      "",
		StackID:          "",
		SavedAt:          f.clock.Now(),
		Features:         nil,
		Deployment:       deployment,
		Update:           s.Update.ID,
		Ref:              f.ref,
		Lease:            s.Update.Lease.Digest,
		SchemaVersion:    3,
		Sequence:         0,
		Digest:           sha256.Sum256(deployment),
		Invalid:          false,
		BlobKey:          mark.Key,
		BlobSize:         mark.Size,
		BlobMD5:          [16]byte{},
		AttemptStartedAt: f.clock.Now(),
	}

	if _, err := f.store.PutCheckpoint(t.Context(), cp, f.nsStore.ns.Fence()); err != nil {
		t.Fatalf("write the checkpoint row that names the marked key: %v", err)
	}

	f.clock.Advance(2 * blob.UnnamedGracePeriod)

	res, err := f.svc.SweepSuperseded(t.Context())
	requireSweepResult(t, res, err, stack.SupersededSweepResult{Deleted: 0, DeletedBytes: 0, Kept: 1, Failed: 0},
		"with a row that names the marked key")
	requireBlobStored(t, f, mark.Key, "with a row that names the marked key")

	if got := marksOf(t, f); slices.ContainsFunc(got, func(m stack.SupersededBlob) bool { return m.Key == mark.Key }) {
		t.Fatalf("marks after the sweep = %+v, want the mark of %q gone", got, mark.Key)
	}
}

// TestSupersededSweepMissingBlob proves that a mark whose blob is not in the
// blob store counts as deleted, and that the sweep drops the mark.
func TestSupersededSweepMissingBlob(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	_, mark := saveTwoCheckpoints(t, f)

	if err := f.blobs.Delete(t.Context(), mark.Key); err != nil {
		t.Fatalf("delete the marked blob: %v", err)
	}

	f.clock.Advance(2 * blob.UnnamedGracePeriod)

	res, err := f.svc.SweepSuperseded(t.Context())
	requireSweepResult(t, res, err,
		stack.SupersededSweepResult{Deleted: 1, DeletedBytes: mark.Size, Kept: 0, Failed: 0}, "with a missing blob")

	if got := marksOf(t, f); len(got) != 0 {
		t.Fatalf("marks after the sweep = %+v, want none", got)
	}
}

// TestSupersededSweepCountsDeletedBytes proves that the run counts the sizes
// of the marked blobs that it deletes, and not the size of a blob that a row
// names again.
func TestSupersededSweepCountsDeletedBytes(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	s := f.started(t, stack.KindUpdate)
	f.saveSeq(t, s, 1, `{"a":1}`)
	f.saveSeq(t, s, 2, `{"a":2,"pad":"`+strings.Repeat("0123456789abcdef", 8)+`"}`)
	f.saveSeq(t, s, 3, `{"a":3,"pad":"`+strings.Repeat("0123456789abcdef", 32)+`"}`)
	f.saveSeq(t, s, 4, `{"a":4}`)

	marks := marksOf(t, f)
	if len(marks) != 3 {
		t.Fatalf("marks before the sweep = %+v, want three", marks)
	}

	if marks[0].Size == marks[1].Size || marks[1].Size == marks[2].Size || marks[0].Size == marks[2].Size {
		t.Fatalf("mark sizes = %d, %d, %d; want different sizes", marks[0].Size, marks[1].Size, marks[2].Size)
	}

	deleted, missing, kept := marks[0], marks[1], marks[2]

	if err := f.blobs.Delete(t.Context(), missing.Key); err != nil {
		t.Fatalf("delete the missing blob: %v", err)
	}

	deployment := []byte(`{"deployment":{"named":true}}`)
	cp := stack.Checkpoint{
		NamespaceID:      "",
		StackID:          "",
		SavedAt:          f.clock.Now(),
		Features:         nil,
		Deployment:       deployment,
		Update:           s.Update.ID,
		Ref:              f.ref,
		Lease:            s.Update.Lease.Digest,
		SchemaVersion:    3,
		Sequence:         0,
		Digest:           sha256.Sum256(deployment),
		Invalid:          false,
		BlobKey:          kept.Key,
		BlobSize:         kept.Size,
		BlobMD5:          [16]byte{},
		AttemptStartedAt: f.clock.Now(),
	}

	if _, err := f.store.PutCheckpoint(t.Context(), cp, f.nsStore.ns.Fence()); err != nil {
		t.Fatalf("write the checkpoint row that names the kept key: %v", err)
	}

	f.clock.Advance(2 * blob.UnnamedGracePeriod)

	res, err := f.svc.SweepSuperseded(t.Context())
	requireSweepResult(t, res, err,
		stack.SupersededSweepResult{Deleted: 2, DeletedBytes: deleted.Size + missing.Size, Kept: 1, Failed: 0},
		"with three marks of different sizes")

	requireBlobGone(t, f, deleted.Key, "after the sweep")
	requireBlobGone(t, f, missing.Key, "after the sweep")
	requireBlobStored(t, f, kept.Key, "after the sweep")
}

// TestSupersededSweepKeepsBlobOfADroppedMark proves that the sweep keeps the
// blob when another sweep dropped the mark first: that sweep owns the delete.
func TestSupersededSweepKeepsBlobOfADroppedMark(t *testing.T) {
	t.Parallel()

	f := newUpdatesWrapped(t, newClock(), nil, func(m *memUpdates) stack.CheckpointStore {
		return droppedBefore{memUpdates: m}
	})
	_, mark := saveTwoCheckpoints(t, f)

	f.clock.Advance(2 * blob.UnnamedGracePeriod)

	res, err := f.svc.SweepSuperseded(t.Context())
	requireSweepResult(t, res, err, stack.SupersededSweepResult{Deleted: 0, DeletedBytes: 0, Kept: 1, Failed: 0},
		"with a mark that another sweep dropped first")
	requireBlobStored(t, f, mark.Key, "with a mark that another sweep dropped first")

	if got := marksOf(t, f); len(got) != 0 {
		t.Fatalf("marks after the sweep = %+v, want none", got)
	}
}

// TestSupersededSweepStopsWhenTheLeaseEnds proves that a sweep whose lease
// ends during a page stops before the next drop, so the marks that are left
// and their blobs stay for the next run.
func TestSupersededSweepStopsWhenTheLeaseEnds(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	f := newUpdatesWrapped(t, newClock(), nil, func(m *memUpdates) stack.CheckpointStore {
		return &cancelAfterDrop{memUpdates: m, cancel: cancel, drops: 0}
	})

	s := f.started(t, stack.KindUpdate)
	f.saveSeq(t, s, 1, `{"a":1}`)
	f.saveSeq(t, s, 2, `{"a":2}`)
	f.saveSeq(t, s, 3, `{"a":3}`)

	before := marksOf(t, f)
	if len(before) != 2 {
		t.Fatalf("marks before the sweep = %+v, want two", before)
	}

	f.clock.Advance(2 * blob.UnnamedGracePeriod)

	_, err := f.svc.SweepSuperseded(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("sweep with a lease that ended: %v, want context.Canceled", err)
	}

	left := marksOf(t, f)
	if len(left) != 1 || left[0].Key != before[1].Key {
		t.Fatalf("marks after the sweep = %+v, want the mark of %q", left, before[1].Key)
	}

	requireBlobStored(t, f, before[1].Key, "after the lease ended")
}

// TestSupersededSweepReturnsTheCountsOfAFailedRun proves that a sweep whose
// second page fails returns the counts and the bytes of the first page with
// the error.
func TestSupersededSweepReturnsTheCountsOfAFailedRun(t *testing.T) {
	t.Parallel()

	const saves = 102

	f := newUpdatesWrapped(t, newClock(), nil, func(m *memUpdates) stack.CheckpointStore {
		return &failMarksAfter{memUpdates: m, err: errMarksRead, reads: 0}
	})

	s := f.started(t, stack.KindUpdate)

	for seq := int64(1); seq <= saves; seq++ {
		f.saveSeq(t, s, seq, `{"a":1}`)
	}

	before := marksOf(t, f)
	if len(before) != saves-1 {
		t.Fatalf("marks before the sweep = %d, want %d", len(before), saves-1)
	}

	var wantBytes int64

	for _, mark := range before[:len(before)-1] {
		wantBytes += mark.Size
	}

	f.clock.Advance(2 * blob.UnnamedGracePeriod)

	res, err := f.svc.SweepSuperseded(t.Context())
	if !errors.Is(err, errMarksRead) {
		t.Fatalf("sweep with a failed second page: %v, want errMarksRead", err)
	}

	want := stack.SupersededSweepResult{Deleted: saves - 2, DeletedBytes: wantBytes, Kept: 0, Failed: 0}
	if res != want {
		t.Fatalf("sweep with a failed second page = %+v, want %+v", res, want)
	}

	left := marksOf(t, f)
	if len(left) != 1 || left[0].Key != before[len(before)-1].Key {
		t.Fatalf("marks after the failed run = %+v, want the mark of %q", left, before[len(before)-1].Key)
	}

	requireBlobStored(t, f, before[len(before)-1].Key, "after the failed run")
}

// TestSupersededSweepKeepsYoungMark proves that a mark newer than the grace
// period is not read, so its blob stays.
func TestSupersededSweepKeepsYoungMark(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	_, mark := saveTwoCheckpoints(t, f)

	res, err := f.svc.SweepSuperseded(t.Context())
	requireSweepResult(t, res, err, stack.SupersededSweepResult{Deleted: 0, DeletedBytes: 0, Kept: 0, Failed: 0},
		"with a young mark")
	requireBlobStored(t, f, mark.Key, "with a young mark")

	if got := marksOf(t, f); len(got) != 1 || got[0].Key != mark.Key {
		t.Fatalf("marks with a young mark = %+v, want the mark of %q", got, mark.Key)
	}
}

// TestSupersededSweepSkipsDeletedStack proves that the sweep does not read the
// marks of a soft-deleted stack, so its blob stays.
func TestSupersededSweepSkipsDeletedStack(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	_, mark := saveTwoCheckpoints(t, f)

	f.store.dropStack(f.ref)
	f.clock.Advance(2 * blob.UnnamedGracePeriod)

	marks, err := f.store.SupersededMarks(t.Context(), stack.SupersededCursor{StackID: "", Key: ""}, f.clock.Now(), 100)
	if err != nil || len(marks) != 0 {
		t.Fatalf("marks of a soft-deleted stack = %+v, %v; want none", marks, err)
	}

	res, err := f.svc.SweepSuperseded(t.Context())
	requireSweepResult(t, res, err, stack.SupersededSweepResult{Deleted: 0, DeletedBytes: 0, Kept: 0, Failed: 0},
		"with a soft-deleted stack")
	requireBlobStored(t, f, mark.Key, "with a soft-deleted stack")
}

// TestSupersededSweepPages proves that the sweep deletes every mark when more
// marks exist than one page holds.
func TestSupersededSweepPages(t *testing.T) {
	t.Parallel()

	const saves = 102

	f := newUpdates(t)
	s := f.started(t, stack.KindUpdate)

	for seq := int64(1); seq <= saves; seq++ {
		f.saveSeq(t, s, seq, `{"a":1}`)
	}

	before := marksOf(t, f)
	if len(before) != saves-1 {
		t.Fatalf("marks before the sweep = %d, want %d", len(before), saves-1)
	}

	var wantBytes int64

	for _, mark := range before {
		wantBytes += mark.Size
	}

	f.clock.Advance(2 * blob.UnnamedGracePeriod)

	res, err := f.svc.SweepSuperseded(t.Context())
	requireSweepResult(t, res, err,
		stack.SupersededSweepResult{Deleted: saves - 1, DeletedBytes: wantBytes, Kept: 0, Failed: 0},
		"with more marks than one page")

	if got := marksOf(t, f); len(got) != 0 {
		t.Fatalf("marks after the sweep = %d, want none", len(got))
	}

	if left := updateBlobs(t, f, s.Update.ID); len(left) != 1 {
		t.Fatalf("blobs of the update after the sweep = %d, want the live checkpoint only", len(left))
	}
}

// TestSupersededSweepRefusesAnUntrustedClock proves that a sweep that cannot
// trust the clock deletes nothing and returns the error.
func TestSupersededSweepRefusesAnUntrustedClock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(*memUpdates, *clock)
	}{
		{name: "a failed read", setup: func(m *memUpdates, _ *clock) {
			m.dbErr = errNoDatabaseClock
		}},
		{name: "a skewed clock", setup: func(m *memUpdates, clk *clock) {
			m.dbNow = func() time.Time { return clk.Now().Add(blob.AttemptSkew + time.Minute) }
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdates(t)
			_, mark := saveTwoCheckpoints(t, f)
			f.clock.Advance(2 * blob.UnnamedGracePeriod)

			tc.setup(f.store, f.clock)

			res, err := f.svc.SweepSuperseded(t.Context())
			if !errors.Is(err, blob.ErrClockSkew) {
				t.Fatalf("sweep with %s: err = %v, want blob.ErrClockSkew", tc.name, err)
			}

			if res != (stack.SupersededSweepResult{Deleted: 0, DeletedBytes: 0, Kept: 0, Failed: 0}) {
				t.Fatalf("sweep with %s = %+v, want no counts", tc.name, res)
			}

			requireBlobStored(t, f, mark.Key, "with "+tc.name)

			if got := marksOf(t, f); len(got) != 1 || got[0].Key != mark.Key {
				t.Fatalf("marks with %s = %+v, want the mark of %q", tc.name, got, mark.Key)
			}
		})
	}
}

// droppedBefore is a checkpoint store that drops each mark before the store
// sees the call, as another sweep that owns the delete does.
type droppedBefore struct {
	*memUpdates
}

// DropSupersededMark implements stack.CheckpointStore.
func (d droppedBefore) DropSupersededMark(
	ctx context.Context, stackID stack.ID, key blob.Key,
) (bool, error) {
	if _, err := d.memUpdates.DropSupersededMark(ctx, stackID, key); err != nil {
		return false, err
	}

	return d.memUpdates.DropSupersededMark(ctx, stackID, key)
}

// cancelAfterDrop is a checkpoint store that cancels the context of the sweep
// after the first drop.
type cancelAfterDrop struct {
	*memUpdates

	cancel context.CancelFunc
	drops  int
}

// DropSupersededMark implements stack.CheckpointStore.
func (c *cancelAfterDrop) DropSupersededMark(
	ctx context.Context, stackID stack.ID, key blob.Key,
) (bool, error) {
	keep, err := c.memUpdates.DropSupersededMark(ctx, stackID, key)

	c.drops++
	if c.drops == 1 {
		c.cancel()
	}

	return keep, err
}

// failMarksAfter is a checkpoint store that fails the marks read after the
// first page, so a sweep stops on its second page.
type failMarksAfter struct {
	*memUpdates

	err   error
	reads int
}

// SupersededMarks implements stack.CheckpointStore.
func (f *failMarksAfter) SupersededMarks(
	ctx context.Context, after stack.SupersededCursor, olderThan time.Time, limit int,
) ([]stack.SupersededMark, error) {
	f.reads++
	if f.reads > 1 {
		return nil, f.err
	}

	return f.memUpdates.SupersededMarks(ctx, after, olderThan, limit)
}
