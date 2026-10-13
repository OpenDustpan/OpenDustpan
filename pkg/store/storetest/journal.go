package storetest

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// journalRow returns the row of a batch of update u that holds the given sequence
// IDs. The content is named by the sha byte, so two rows with one sequence ID and
// different sha bytes hold different content.
func journalRow(t *testing.T, s UpdatesStore, u stack.Update, sha byte, seqs ...int64) stack.JournalRow {
	t.Helper()

	st, err := s.GetStack(t.Context(), u.Ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	if len(seqs) == 0 {
		t.Fatal("a journal row needs a sequence ID")
	}

	low := seqs[0]
	for _, seq := range seqs[1:] {
		low = min(low, seq)
	}

	sum := [sha256.Size]byte{sha}
	ops := make([]int64, len(seqs))

	return stack.JournalRow{
		Ref:          u.Ref,
		Update:       u.ID,
		Lease:        u.Lease.Digest,
		BlobKey:      blob.Key(fmt.Sprintf("pulumi/%s/%s/journal/%d", st.ID, u.ID, low)),
		SHA256:       sum,
		MD5:          [16]byte{sha},
		Size:         1,
		SequenceIDs:  seqs,
		OperationIDs: ops,
		Verify:       nil,
	}
}

// testJournalPut shows the write of a batch row: a new batch gets the next arrival,
// a retry with the same content writes nothing, and a sequence ID that is stored
// with other content is a conflict. A refused batch stores none of its sequences.
func testJournalPut(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())

	first := journalRow(t, f.s, u, 1, 1, 2)
	if retried, err := f.s.PutJournalRow(t.Context(), first, f.fence); err != nil || retried {
		t.Fatalf("first batch: retried %t, err %v; want stored", retried, err)
	}

	second := journalRow(t, f.s, u, 2, 3)
	if retried, err := f.s.PutJournalRow(t.Context(), second, f.fence); err != nil || retried {
		t.Fatalf("second batch: retried %t, err %v; want stored", retried, err)
	}

	if retried, err := f.s.PutJournalRow(t.Context(), first, f.fence); err != nil || !retried {
		t.Fatalf("retry of the first batch: retried %t, err %v; want a retry with no error", retried, err)
	}

	conflict := journalRow(t, f.s, u, 9, 2)
	if _, err := f.s.PutJournalRow(t.Context(), conflict, f.fence); !errors.Is(err, stack.ErrJournalConflict) {
		t.Fatalf("sequence 2 with other content: err = %v, want ErrJournalConflict", err)
	}

	partial := journalRow(t, f.s, u, 1, 2, 7)
	if _, err := f.s.PutJournalRow(t.Context(), partial, f.fence); !errors.Is(err, stack.ErrJournalConflict) {
		t.Fatalf("partly stored batch: err = %v, want ErrJournalConflict", err)
	}

	// The refused batch stored nothing, so sequence 7 is still free.
	free := journalRow(t, f.s, u, 4, 7)
	if retried, err := f.s.PutJournalRow(t.Context(), free, f.fence); err != nil || retried {
		t.Fatalf("sequence 7 after a refusal: retried %t, err %v; want stored", retried, err)
	}
}

// testJournalRefusals shows that the journal refuses a write without a running
// update under the lease, a preview, an unknown stack, a stale fence and a blob key
// that names another stack.
func testJournalRefusals(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())

	forged := journalRow(t, f.s, u, 1, 1)
	forged.Lease = sha256.Sum256([]byte("another lease"))

	if _, err := f.s.PutJournalRow(t.Context(), forged, f.fence); !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Fatalf("batch under another lease: err = %v, want ErrWrongUpdateState", err)
	}

	preview := f.run(t, f.ref, "p1", stack.Plan{Kind: stack.KindPreview, DryRun: false, CreatedBy: "", RestoredFrom: 0})
	if _, err := f.s.PutJournalRow(t.Context(), journalRow(t, f.s, preview, 1, 1), f.fence); !errors.Is(
		err, stack.ErrWrongUpdateState,
	) {
		t.Fatalf("batch of a preview: err = %v, want ErrWrongUpdateState", err)
	}

	missing := journalRow(t, f.s, u, 1, 1)
	missing.Ref = stack.Ref{Namespace: f.ref.Namespace, Project: f.ref.Project, Stack: "never-created"}
	if _, err := f.s.PutJournalRow(t.Context(), missing, f.fence); !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Fatalf("batch of a missing stack: err = %v, want ErrWrongUpdateState", err)
	}

	stale := namespace.Fence{Namespace: f.fence.Namespace, FailoverVersion: f.fence.FailoverVersion - 1}
	if _, err := f.s.PutJournalRow(t.Context(), journalRow(t, f.s, u, 1, 1), stale); !errors.Is(
		err, namespace.ErrFenced,
	) {
		t.Fatalf("batch with a stale fence: err = %v, want namespace.ErrFenced", err)
	}

	wrongKey := journalRow(t, f.s, u, 1, 1)
	wrongKey.BlobKey = blob.Key("pulumi/other/" + u.ID.String() + "/journal/1")
	if _, err := f.s.PutJournalRow(t.Context(), wrongKey, f.fence); err == nil {
		t.Fatal("batch with a blob key of another stack: err = nil, want an error")
	}
}

// testJournalAfterCancel shows that a batch that arrives after the update is
// canceled is refused, as the events are.
func testJournalAfterCancel(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	late := journalRow(t, f.s, u, 1, 1)
	f.end(t, u, stack.StatusCanceled)

	if _, err := f.s.PutJournalRow(t.Context(), late, f.fence); !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Fatalf("batch after cancel: err = %v, want ErrWrongUpdateState", err)
	}
}

// putAtOnce sends each row to the store from its own goroutine, all at once, and
// returns the retried flag and the error of each call, in the order of rows.
func putAtOnce(t *testing.T, f fixture, rows []stack.JournalRow) ([]bool, []error) {
	t.Helper()

	retried := make([]bool, len(rows))
	errs := make([]error, len(rows))

	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := range rows {
		wg.Go(func() {
			<-start
			retried[i], errs[i] = f.s.PutJournalRow(t.Context(), rows[i], f.fence)
		})
	}

	close(start)
	wg.Wait()

	return retried, errs
}

// testJournalConcurrentRows shows that two batches of one update that arrive at
// once both store. The arrival numbers come from one read each, so a collision on
// the arrival would fail one of the calls.
func testJournalConcurrentRows(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	rows := []stack.JournalRow{journalRow(t, f.s, u, 1, 1), journalRow(t, f.s, u, 2, 2)}

	retried, errs := putAtOnce(t, f, rows)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent batch %d: %v, want stored", i, err)
		}

		if retried[i] {
			t.Fatalf("concurrent batch %d reported a retry, want stored", i)
		}
	}

	for i := range rows {
		if again, err := f.s.PutJournalRow(t.Context(), rows[i], f.fence); err != nil || !again {
			t.Fatalf("batch %v after the race: retried %t, err %v; want a retry", rows[i].SequenceIDs, again, err)
		}
	}
}

// testJournalConcurrentRetry sends one batch twice at once. One call stores the
// batch and the other reports a retry. Neither fails on the sequence key.
func testJournalConcurrentRetry(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	row := journalRow(t, f.s, u, 1, 1, 2)

	retried, errs := putAtOnce(t, f, []stack.JournalRow{row, row})

	stored, retries := 0, 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent copy %d: %v, want no error", i, err)
		}

		if retried[i] {
			retries++
		} else {
			stored++
		}
	}

	if stored != 1 || retries != 1 {
		t.Fatalf("two copies at once: %d stored and %d retried, want one of each", stored, retries)
	}
}

// withOperations sets the operation IDs of a batch row.
func withOperations(row stack.JournalRow, ops ...int64) stack.JournalRow {
	row.OperationIDs = ops

	return row
}

// testJournalVerify shows that the store calls the row's check with the operation IDs
// that the stored rows hold (ADR 43, decision 6.4), and that a refused batch stores
// nothing.
func testJournalVerify(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())

	first := withOperations(journalRow(t, f.s, u, 1, 1), 10, 11)
	if _, err := f.s.PutJournalRow(t.Context(), first, f.fence); err != nil {
		t.Fatalf("first batch: %v", err)
	}

	var seen []int64

	refused := withOperations(journalRow(t, f.s, u, 2, 2), 12)
	refused.Verify = func(known []int64) error {
		seen = append([]int64(nil), known...)

		return stack.ErrJournalBatch
	}

	if _, err := f.s.PutJournalRow(t.Context(), refused, f.fence); !errors.Is(err, stack.ErrJournalBatch) {
		t.Fatalf("refused batch: err = %v, want ErrJournalBatch", err)
	}

	slices.Sort(seen)
	if !slices.Equal(seen, []int64{10, 11}) {
		t.Fatalf("the check saw operations %v, want the stored [10 11]", seen)
	}

	// The refused batch stored nothing, so sequence 2 is still free.
	free := withOperations(journalRow(t, f.s, u, 3, 2), 13)
	if retried, err := f.s.PutJournalRow(t.Context(), free, f.fence); err != nil || retried {
		t.Fatalf("sequence 2 after a refusal: retried %t, err %v; want stored", retried, err)
	}

	accepted := withOperations(journalRow(t, f.s, u, 4, 3), 14)
	accepted.Verify = func([]int64) error { return nil }

	if retried, err := f.s.PutJournalRow(t.Context(), accepted, f.fence); err != nil || retried {
		t.Fatalf("accepted batch: retried %t, err %v; want stored", retried, err)
	}
}

// sortedKeys returns the keys in order, for comparison.
func sortedKeys(keys []blob.Key) []blob.Key {
	out := slices.Clone(keys)
	slices.Sort(out)

	return out
}

// testJournalBuild shows that the built checkpoint removes the journal of the update
// and returns the blob keys it removed, and that a refused build removes nothing.
func testJournalBuild(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	first := journalRow(t, f.s, u, 1, 1, 2)
	second := journalRow(t, f.s, u, 2, 3)

	rows := []stack.JournalRow{first, second}
	for i := range rows {
		if _, err := f.s.PutJournalRow(t.Context(), rows[i], f.fence); err != nil {
			t.Fatalf("journal batch: %v", err)
		}
	}

	want := []blob.Key{first.BlobKey, second.BlobKey}

	testJournalRefusedBuild(t, f, u, want)

	removed, err := f.s.PutBuiltCheckpoint(t.Context(), newCheckpoint(u, 0, "{}"), f.fence)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if !slices.Equal(sortedKeys(removed), sortedKeys(want)) {
		t.Fatalf("build removed keys %v, want %v", removed, want)
	}

	if after, err := f.s.JournalKeys(t.Context(), f.ref, u.ID); err != nil || len(after) != 0 {
		t.Fatalf("journal after the build = %v, %v; want empty", after, err)
	}

	// The rows and their sequences are gone, so the first batch stores again.
	if retried, err := f.s.PutJournalRow(t.Context(), first, f.fence); err != nil || retried {
		t.Fatalf("batch after the build: retried %t, err %v; want stored", retried, err)
	}
}

// testJournalRefusedBuild shows that a build refused by a stale fence or by another
// lease removes nothing from the journal of the update.
func testJournalRefusedBuild(t *testing.T, f fixture, u stack.Update, want []blob.Key) {
	t.Helper()

	stale := namespace.Fence{Namespace: f.fence.Namespace, FailoverVersion: f.fence.FailoverVersion - 1}

	_, staleErr := f.s.PutBuiltCheckpoint(t.Context(), newCheckpoint(u, 0, "{}"), stale)
	if !errors.Is(staleErr, namespace.ErrFenced) {
		t.Fatalf("build with a stale fence: err = %v, want namespace.ErrFenced", staleErr)
	}

	forged := newCheckpoint(u, 0, "{}")
	forged.Lease = sha256.Sum256([]byte("another lease"))

	_, forgedErr := f.s.PutBuiltCheckpoint(t.Context(), forged, f.fence)
	if !errors.Is(forgedErr, stack.ErrWrongUpdateState) {
		t.Fatalf("build under another lease: err = %v, want ErrWrongUpdateState", forgedErr)
	}

	got, keysErr := f.s.JournalKeys(t.Context(), f.ref, u.ID)
	if keysErr != nil || !slices.Equal(sortedKeys(got), sortedKeys(want)) {
		t.Fatalf("journal after refused builds = %v, %v; want %v", got, keysErr, want)
	}
}

// testJournalBuildRace starts a batch and the build of one update at once. A batch that
// stored before the build has its key among the keys that the build removed. A batch
// that the build beat is refused or stored after it, so its key is not lost.
func testJournalBuildRace(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	row := journalRow(t, f.s, u, 1, 1)

	var (
		wg        sync.WaitGroup
		putErr    error
		removed   []blob.Key
		buildErr  error
		startLine = make(chan struct{})
	)

	wg.Go(func() {
		<-startLine
		_, putErr = f.s.PutJournalRow(t.Context(), row, f.fence)
	})

	wg.Go(func() {
		<-startLine
		removed, buildErr = f.s.PutBuiltCheckpoint(t.Context(), newCheckpoint(u, 0, "{}"), f.fence)
	})

	close(startLine)
	wg.Wait()

	if buildErr != nil {
		t.Fatalf("build beside a batch: %v", buildErr)
	}

	if putErr != nil {
		return
	}

	// The batch stored. Either the build removed its key, or it stored after the build
	// and the journal still holds it. Its key is not lost in both cases.
	if slices.Contains(removed, row.BlobKey) {
		return
	}

	after, err := f.s.JournalKeys(t.Context(), f.ref, u.ID)
	if err != nil || !slices.Contains(after, row.BlobKey) {
		t.Fatalf("the batch stored, the build removed %v, and the journal holds %v: its key %s is lost",
			removed, after, row.BlobKey)
	}
}

// testBackupJournalBlob shows that the backup names the blob of each journal batch,
// with its MD5 and size, as it names a checkpoint blob (ADR 43, decision 6.8).
func testBackupJournalBlob(t *testing.T, newStore BackupFactory) {
	t.Helper()

	src := newStore(t)
	f := newFixture(t, src)
	u := f.run(t, f.ref, "u1", mutating())

	row := journalRow(t, src, u, 7, 1)
	row.Size = 33

	if _, err := src.PutJournalRow(t.Context(), row, f.fence); errors.Is(err, errors.ErrUnsupported) {
		t.Skip("the store keeps no journal rows")
	} else if err != nil {
		t.Fatalf("journal batch: %v", err)
	}

	sum, _ := exportBackup(t, src)

	want := []backup.BlobRef{{MD5: row.MD5, Key: string(row.BlobKey), Size: row.Size, Sealed: false}}
	if !slices.EqualFunc(sum.Blobs, want, sameBlobRef) {
		t.Fatalf("export blobs = %+v, want %+v", sum.Blobs, want)
	}
}
