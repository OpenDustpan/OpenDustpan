package storetest

import (
	"context"
	"crypto/sha256"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/stack"
)

// SupersededMarkAger is an optional store capability for the cases of the
// sweep of superseded marks: it moves the marks of a stack before a time with
// a direct SQL update, so a case needs no wait.
type SupersededMarkAger interface {
	// AgeSupersededMarks moves every mark of stackID before at.
	AgeSupersededMarks(ctx context.Context, stackID stack.ID, at time.Time) error
}

// RunSupersededSweep runs the cases of the sweep of superseded checkpoint
// blobs against a store, with the real blob store and a guard. The cases
// prove that a running backup or restore holds the sweep, that a key that a
// row names again keeps its blob, that a mark that another sweep dropped first
// keeps its blob, that a missing blob counts as deleted, that a young mark and
// a mark of a soft-deleted stack stay, and that the sweep goes page by page.
func RunSupersededSweep(t *testing.T, newStore BlobDeleteFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, r *blobDeleteRig)
		name string
	}{
		{name: "a running backup holds the sweep", test: testSupersededSweepBackup},
		{name: "a running restore holds the sweep", test: testSupersededSweepRestore},
		{name: "a key that a checkpoint row names again keeps its blob", test: testSupersededSweepNamed},
		{name: "a mark that another sweep dropped first keeps its blob", test: testSupersededSweepDroppedTwice},
		{name: "a mark whose blob is missing counts as deleted", test: testSupersededSweepMissingBlob},
		{name: "a mark newer than the grace period stays", test: testSupersededSweepYoung},
		{name: "a mark of a soft-deleted stack is not read", test: testSupersededSweepDeletedStack},
		{name: "more marks than the limit are all swept in order", test: testSupersededSweepPaging},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newBlobDeleteRig(t, newStore(t)))
		})
	}
}

// onlyMark returns the only mark of the stack of c, or fails the test.
func (r *blobDeleteRig) onlyMark(t *testing.T, c *stackCase) stack.SupersededBlob {
	t.Helper()

	marks, err := r.store.SupersededBlobs(t.Context(), c.stack.ID)
	if err != nil || len(marks) != 1 {
		t.Fatalf("marks of the stack = %+v, %v; want one", marks, err)
	}

	return marks[0]
}

// requireMark checks that the mark of key is still stored.
func (r *blobDeleteRig) requireMark(t *testing.T, c *stackCase, key blob.Key, when string) {
	t.Helper()

	marks, err := r.store.SupersededBlobs(t.Context(), c.stack.ID)
	if err != nil {
		t.Fatalf("marks of the stack %s: %v", when, err)
	}

	if !slices.ContainsFunc(marks, func(mark stack.SupersededBlob) bool { return mark.Key == key }) {
		t.Fatalf("mark of %q %s is gone, want it stored", key, when)
	}
}

// requireNoMark checks that the mark of key is gone.
func (r *blobDeleteRig) requireNoMark(t *testing.T, c *stackCase, key blob.Key, when string) {
	t.Helper()

	marks, err := r.store.SupersededBlobs(t.Context(), c.stack.ID)
	if err != nil {
		t.Fatalf("marks of the stack %s: %v", when, err)
	}

	if slices.ContainsFunc(marks, func(mark stack.SupersededBlob) bool { return mark.Key == key }) {
		t.Fatalf("mark of %q %s is stored, want it gone", key, when)
	}
}

// ageMarks moves every mark of the stack of c past the grace period, on the
// clock of the database.
func (r *blobDeleteRig) ageMarks(t *testing.T, c *stackCase) {
	t.Helper()

	ager, ok := r.store.(SupersededMarkAger)
	if !ok {
		t.Skip("the store cannot age superseded marks")
	}

	at := markClock(t, r.store).Add(-2 * blob.UnnamedGracePeriod)

	if err := ager.AgeSupersededMarks(t.Context(), c.stack.ID, at); err != nil {
		t.Fatalf("age the marks of the stack: %v", err)
	}
}

// requireSwept checks the result of one sweep.
func requireSwept(
	t *testing.T, res stack.SupersededSweepResult, err error, want stack.SupersededSweepResult, when string,
) {
	t.Helper()

	if err != nil || res != want {
		t.Fatalf("sweep %s = %+v, %v; want %+v, nil", when, res, err, want)
	}
}

// testSupersededSweepBackup holds the sweep while a backup copies the marked
// blob, then lets the sweep delete it after the backup ended.
func testSupersededSweepBackup(t *testing.T, r *blobDeleteRig) {
	t.Helper()

	c := r.newStackCase(t)
	r.save(t, c, 1)

	named := r.checkpointKey(t, c)

	// The backup snapshot starts while a checkpoint row still names the blob,
	// so the backup carries it. The next save marks the blob while the backup
	// copies it.
	b := r.startHeldBackup(t)
	r.save(t, c, 2)

	mark := r.onlyMark(t, c)
	if mark.Key != named {
		t.Fatalf("mark = %q, want the blob of the first save %q", mark.Key, named)
	}

	r.ageMarks(t, c)

	_, err := r.updates.SweepSuperseded(t.Context())
	requireWaits(t, err, blobguard.Backup, "sweep during the backup")
	r.requireStored(t, []blob.Key{mark.Key}, "after the sweep during the backup")
	r.requireMark(t, c, mark.Key, "after the sweep during the backup")

	r.finish(t, b, []blob.Key{mark.Key})

	res, err := r.updates.SweepSuperseded(t.Context())
	requireSwept(t, res, err, stack.SupersededSweepResult{Deleted: 1, Kept: 0, Failed: 0}, "after the backup ended")
	r.requireDeleted(t, []blob.Key{mark.Key}, "after the backup ended")
	r.requireNoMark(t, c, mark.Key, "after the backup ended")
}

// testSupersededSweepRestore holds the sweep while a restore runs, then lets
// the sweep delete the blob after the restore ended.
func testSupersededSweepRestore(t *testing.T, r *blobDeleteRig) {
	t.Helper()

	ctx := t.Context()

	ops, ok := r.store.(backup.RestoreOperations)
	if !ok {
		t.Skip("the store does not implement the restore operation ports")
	}

	c := r.newStackCase(t)
	r.save(t, c, 1)
	r.save(t, c, 2)

	mark := r.onlyMark(t, c)
	r.ageMarks(t, c)

	id := backup.MintRestoreID()
	if err := r.store.StartRestore(ctx, newRestoreRecord(id, "bkp-op", now()), restoreOpStart(id)); err != nil {
		t.Fatalf("start restore: %v", err)
	}

	_, err := r.updates.SweepSuperseded(ctx)
	requireWaits(t, err, blobguard.Restore, "sweep while the restore runs")
	r.requireStored(t, []blob.Key{mark.Key}, "while the restore runs")
	r.requireMark(t, c, mark.Key, "while the restore runs")

	claimed := mustClaimRestoreOp(t, r.store.DurableOperationsPort(), "instance-a")
	_, err = ops.FinishRestore(ctx, claimed.Claim.Step(backup.RestoreStepFinish), backup.RestoreEnd{
		ID: id, State: backup.RestoreSucceeded, Error: "", EndedAt: now(), DevReport: nil, LeaveMaintenance: false,
	})
	if err != nil {
		t.Fatalf("finish restore: %v", err)
	}

	if completeErr := r.store.DurableOperationsPort().Complete(ctx, claimed.Claim); completeErr != nil {
		t.Fatalf("complete the operation of the restore: %v", completeErr)
	}

	res, err := r.updates.SweepSuperseded(ctx)
	requireSwept(t, res, err, stack.SupersededSweepResult{Deleted: 1, Kept: 0, Failed: 0}, "after the restore ended")
	r.requireDeleted(t, []blob.Key{mark.Key}, "after the restore ended")
	r.requireNoMark(t, c, mark.Key, "after the restore ended")
}

// testSupersededSweepNamed writes a checkpoint row that names the marked key
// again, and checks that the sweep keeps the blob and drops the mark.
func testSupersededSweepNamed(t *testing.T, r *blobDeleteRig) {
	t.Helper()

	ctx := t.Context()

	c := r.newStackCase(t)
	r.save(t, c, 1)
	r.save(t, c, 2)

	mark := r.onlyMark(t, c)
	at := markClock(t, r.store)
	deployment := []byte(`{"deployment":{"named":true}}`)

	cp := stack.Checkpoint{
		NamespaceID:      "",
		StackID:          "",
		SavedAt:          at,
		Features:         nil,
		Deployment:       deployment,
		Update:           c.started.Update.ID,
		Ref:              c.ref,
		Lease:            c.started.Update.Lease.Digest,
		SchemaVersion:    3,
		Sequence:         0,
		Digest:           sha256.Sum256(deployment),
		Invalid:          false,
		BlobKey:          mark.Key,
		BlobSize:         mark.Size,
		BlobMD5:          [16]byte{},
		AttemptStartedAt: at,
	}

	if _, err := r.store.PutCheckpoint(ctx, cp, r.ns.Fence()); err != nil {
		t.Fatalf("write the checkpoint row that names the marked key: %v", err)
	}

	r.ageMarks(t, c)

	res, err := r.updates.SweepSuperseded(ctx)
	requireSwept(t, res, err, stack.SupersededSweepResult{Deleted: 0, Kept: 1, Failed: 0},
		"with a row that names the marked key")
	r.requireStored(t, []blob.Key{mark.Key}, "with a row that names the marked key")
	r.requireNoMark(t, c, mark.Key, "with a row that names the marked key")
}

// testSupersededSweepDroppedTwice drops the same mark twice and checks that
// the second call reports keep with no error, and that the blob is still
// stored.
func testSupersededSweepDroppedTwice(t *testing.T, r *blobDeleteRig) {
	t.Helper()

	c := r.newStackCase(t)
	r.save(t, c, 1)
	r.save(t, c, 2)

	mark := r.onlyMark(t, c)

	keep, err := r.store.DropSupersededMark(t.Context(), c.stack.ID, mark.Key)
	if err != nil {
		t.Fatalf("first drop of the mark: %v", err)
	}

	if keep {
		t.Fatal("first drop of the mark reports keep, want the mark dropped")
	}

	keep, err = r.store.DropSupersededMark(t.Context(), c.stack.ID, mark.Key)
	if err != nil || !keep {
		t.Fatalf("second drop of the mark = %v, %v; want keep true, nil", keep, err)
	}

	r.requireStored(t, []blob.Key{mark.Key}, "after the second drop of the mark")
	r.requireNoMark(t, c, mark.Key, "after the second drop of the mark")
}

// testSupersededSweepMissingBlob checks that a mark whose blob is not in the
// blob store counts as deleted, and that its mark is dropped.
func testSupersededSweepMissingBlob(t *testing.T, r *blobDeleteRig) {
	t.Helper()

	c := r.newStackCase(t)
	r.save(t, c, 1)
	r.save(t, c, 2)

	mark := r.onlyMark(t, c)

	if err := r.live.Delete(t.Context(), mark.Key); err != nil {
		t.Fatalf("delete the marked blob from the blob store: %v", err)
	}

	r.ageMarks(t, c)

	res, err := r.updates.SweepSuperseded(t.Context())
	requireSwept(t, res, err, stack.SupersededSweepResult{Deleted: 1, Kept: 0, Failed: 0}, "with a missing blob")
	r.requireNoMark(t, c, mark.Key, "with a missing blob")
}

// testSupersededSweepYoung checks that a mark newer than the grace period is
// not read, so its blob stays.
func testSupersededSweepYoung(t *testing.T, r *blobDeleteRig) {
	t.Helper()

	c := r.newStackCase(t)
	r.save(t, c, 1)
	r.save(t, c, 2)

	mark := r.onlyMark(t, c)

	res, err := r.updates.SweepSuperseded(t.Context())
	requireSwept(t, res, err, stack.SupersededSweepResult{Deleted: 0, Kept: 0, Failed: 0}, "with a young mark")
	r.requireStored(t, []blob.Key{mark.Key}, "with a young mark")
	r.requireMark(t, c, mark.Key, "with a young mark")
}

// testSupersededSweepDeletedStack checks that the sweep does not read the
// marks of a soft-deleted stack, so its blob stays.
func testSupersededSweepDeletedStack(t *testing.T, r *blobDeleteRig) {
	t.Helper()

	ctx := t.Context()

	c := r.newStackCase(t)
	r.save(t, c, 1)
	r.save(t, c, 2)

	mark := r.onlyMark(t, c)

	r.complete(t, c)

	err := r.store.ForceDeleteStack(ctx, c.ref, r.ns.Fence(), inventory.Deletion{
		Entry:      r.blobDeleteEntry("unit:manage", "pulumi:acme/web/dev"),
		PurgeAfter: purgeAt(),
	})
	if err != nil {
		t.Fatalf("soft-delete the stack: %v", err)
	}

	r.ageMarks(t, c)

	olderThan := markClock(t, r.store)
	marks, err := r.store.SupersededMarks(ctx, stack.SupersededCursor{StackID: "", Key: ""}, olderThan, 100)
	if err != nil {
		t.Fatalf("read the superseded marks: %v", err)
	}

	if slices.ContainsFunc(marks, func(mark stack.SupersededMark) bool { return mark.StackID == c.stack.ID }) {
		t.Fatalf("marks of a soft-deleted stack = %+v, want none", marks)
	}

	res, err := r.updates.SweepSuperseded(ctx)
	requireSwept(t, res, err, stack.SupersededSweepResult{Deleted: 0, Kept: 0, Failed: 0},
		"with a soft-deleted stack")
	r.requireStored(t, []blob.Key{mark.Key}, "with a soft-deleted stack")
}

// testSupersededSweepPaging checks that the port reads the marks in (stack ID,
// key) order, one page at a time, and that the sweep deletes every mark.
func testSupersededSweepPaging(t *testing.T, r *blobDeleteRig) {
	t.Helper()

	ctx := t.Context()

	const saves = 102

	c := r.newStackCase(t)

	for seq := int64(1); seq <= saves; seq++ {
		r.save(t, c, seq)
	}

	first := r.supersededMarks(t, c, 100)
	if len(first) != 100 {
		t.Fatalf("first page of marks = %d, want 100", len(first))
	}

	last := first[len(first)-1]

	next, err := r.store.SupersededMarks(
		ctx, stack.SupersededCursor{StackID: last.StackID, Key: last.Key}, markClock(t, r.store).Add(time.Minute), 100,
	)
	if err != nil {
		t.Fatalf("read the page after the cursor: %v", err)
	}

	if len(next) != saves-1-len(first) {
		t.Fatalf("second page of marks = %d, want %d", len(next), saves-1-len(first))
	}

	all := append(slices.Clone(first), next...)

	if !slices.IsSortedFunc(all, func(a, b stack.SupersededMark) int {
		if order := strings.Compare(string(a.StackID), string(b.StackID)); order != 0 {
			return order
		}

		return strings.Compare(string(a.Key), string(b.Key))
	}) {
		t.Fatalf("marks are not ordered by stack ID and key: %+v", all)
	}

	r.ageMarks(t, c)

	res, err := r.updates.SweepSuperseded(ctx)
	requireSwept(t, res, err, stack.SupersededSweepResult{Deleted: saves - 1, Kept: 0, Failed: 0},
		"with more marks than one page")

	left, err := r.store.SupersededMarks(ctx, stack.SupersededCursor{StackID: "", Key: ""}, markClock(t, r.store), 100)
	if err != nil || len(left) != 0 {
		t.Fatalf("marks after the sweep = %+v, %v; want none", left, err)
	}
}

// supersededMarks returns one page of the marks of the stack of c.
func (r *blobDeleteRig) supersededMarks(t *testing.T, c *stackCase, limit int) []stack.SupersededMark {
	t.Helper()

	marks, err := r.store.SupersededMarks(
		t.Context(), stack.SupersededCursor{StackID: "", Key: ""}, markClock(t, r.store).Add(time.Minute), limit,
	)
	if err != nil {
		t.Fatalf("read the superseded marks: %v", err)
	}

	var out []stack.SupersededMark

	for _, mark := range marks {
		if mark.StackID == c.stack.ID {
			out = append(out, mark)
		}
	}

	return out
}
